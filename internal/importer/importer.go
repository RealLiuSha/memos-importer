package importer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"memos-importer/internal/domain"
	"memos-importer/internal/memos"
	"memos-importer/internal/redact"
	"memos-importer/internal/source"
	"memos-importer/internal/store"
)

type MemosClient interface {
	CreateAttachment(ctx context.Context, req memos.CreateAttachmentRequest) (*memos.Attachment, error)
	CreateMemo(ctx context.Context, req memos.CreateMemoRequest) (*memos.Memo, error)
	UpdateMemo(ctx context.Context, name string, req memos.UpdateMemoRequest) (*memos.Memo, error)
}

type Engine struct {
	source     source.Source
	memos      MemosClient
	store      *store.Store
	broker     *Broker
	options    Options
	optionsErr error
}

func NewEngine(src source.Source, memosClient MemosClient, st *store.Store, broker *Broker, options Options) *Engine {
	options = options.Normalized()
	return &Engine{
		source:     src,
		memos:      memosClient,
		store:      st,
		broker:     broker,
		options:    options,
		optionsErr: options.Validate(),
	}
}

func (e *Engine) CreateJob(ctx context.Context, externalIDs []string) (string, error) {
	if e.optionsErr != nil {
		return "", e.optionsErr
	}
	externalIDs = uniqueExternalIDs(externalIDs)
	if len(externalIDs) == 0 {
		return "", errors.New("external_ids is required")
	}
	jobID := newID()
	optionsJSON, err := json.Marshal(e.options)
	if err != nil {
		return "", err
	}
	titleByID := make(map[string]string)
	if refs, err := e.source.ListDocuments(ctx); err == nil {
		for _, ref := range refs {
			titleByID[ref.ID] = ref.Title
		}
	}
	items := make([]store.ImportItem, 0, len(externalIDs))
	for _, id := range externalIDs {
		title := titleByID[id]
		if title == "" {
			title = id
		}
		items = append(items, store.ImportItem{ExternalID: id, Title: title, Status: "pending", Warnings: "[]"})
	}
	if err := e.store.CreateJob(ctx, store.Job{
		ID:        jobID,
		Source:    e.source.Name(),
		Status:    "pending",
		Options:   string(optionsJSON),
		CreatedAt: time.Now().UTC(),
	}, items); err != nil {
		return "", err
	}
	e.publish(Event{JobID: jobID, Type: "job_created"})
	return jobID, nil
}

func uniqueExternalIDs(externalIDs []string) []string {
	seen := make(map[string]bool, len(externalIDs))
	unique := make([]string, 0, len(externalIDs))
	for _, id := range externalIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}

func (e *Engine) RunJob(ctx context.Context, jobID string) error {
	if e.optionsErr != nil {
		_ = e.store.UpdateJobStatus(ctx, jobID, "failed", e.optionsErr.Error())
		return e.optionsErr
	}
	if err := e.store.UpdateJobStatus(ctx, jobID, "running", ""); err != nil {
		return err
	}
	e.publish(Event{JobID: jobID, Type: "job_running"})
	items, err := e.store.PendingItems(ctx, jobID)
	if err != nil {
		_ = e.store.UpdateJobStatus(context.Background(), jobID, "failed", err.Error())
		return err
	}
	jobs := make(chan store.ImportItem)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	workerCount := e.options.WorkerCount
	if workerCount > len(items) && len(items) > 0 {
		workerCount = len(items)
	}
	if workerCount <= 0 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := e.processItem(ctx, jobID, item); err != nil {
					mu.Lock()
					failures++
					mu.Unlock()
				}
			}
		}()
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			_ = e.store.UpdateJobStatus(context.Background(), jobID, "canceled", ctx.Err().Error())
			e.publish(Event{JobID: jobID, Type: "job_canceled", Payload: ctx.Err().Error()})
			return ctx.Err()
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		_ = e.store.UpdateJobStatus(context.Background(), jobID, "canceled", ctx.Err().Error())
		e.publish(Event{JobID: jobID, Type: "job_canceled", Payload: ctx.Err().Error()})
		return ctx.Err()
	}
	if failures > 0 {
		_ = e.store.UpdateJobStatus(ctx, jobID, "failed", fmt.Sprintf("%d item(s) failed", failures))
		e.publish(Event{JobID: jobID, Type: "job_failed", Payload: failures})
		return nil
	}
	_ = e.store.UpdateJobStatus(ctx, jobID, "done", "")
	e.publish(Event{JobID: jobID, Type: "job_done"})
	return nil
}

func (e *Engine) RetryFailed(ctx context.Context, jobID string) error {
	if e.optionsErr != nil {
		return e.optionsErr
	}
	failed, err := e.store.FailedItems(ctx, jobID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(failed))
	for _, item := range failed {
		ids = append(ids, item.ExternalID)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := e.store.ResetItems(ctx, jobID, ids); err != nil {
		return err
	}
	return e.RunJob(ctx, jobID)
}

func (e *Engine) processItem(ctx context.Context, jobID string, item store.ImportItem) error {
	item.Status = "running"
	item.Error = ""
	item.ErrorStage = ""
	_ = e.store.UpdateItem(ctx, item)
	e.publish(Event{JobID: jobID, Type: "item_running", ExternalID: item.ExternalID, Payload: item})

	doc, err := e.source.FetchDocument(ctx, item.ExternalID)
	if err != nil {
		if ctx.Err() != nil {
			return e.cancelItem(context.Background(), jobID, item, ctx.Err())
		}
		return e.failItem(ctx, jobID, item, "fetch", err)
	}
	if doc == nil {
		return e.failItem(ctx, jobID, item, "fetch", errors.New("source returned nil document"))
	}
	if ctx.Err() != nil {
		return e.cancelItem(context.Background(), jobID, item, ctx.Err())
	}
	item.Title = doc.Ref.Title
	warningsJSON := marshalWarnings(doc.Warnings)
	item.Warnings = warningsJSON

	content, attachments, err := e.prepareContent(ctx, doc)
	if err != nil {
		item.Warnings = warningsJSON
		if ctx.Err() != nil {
			return e.cancelItem(context.Background(), jobID, item, ctx.Err())
		}
		return e.failItem(ctx, jobID, item, "attachment", err)
	}
	if ctx.Err() != nil {
		return e.cancelItem(context.Background(), jobID, item, ctx.Err())
	}
	hash := contentHash(content, attachments)
	if e.options.ContentLengthLimit > 0 && int64(len([]byte(content))) > e.options.ContentLengthLimit {
		return e.failItem(ctx, jobID, item, "content_length", fmt.Errorf("content length %d exceeds memos limit %d", len([]byte(content)), e.options.ContentLengthLimit))
	}

	mapping, err := e.store.GetDocumentMapping(ctx, e.source.Name(), doc.Ref.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return e.failItem(ctx, jobID, item, "mapping", err)
	}
	status := "imported"
	var memoID string
	if mapping == nil {
		memo, err := e.memos.CreateMemo(ctx, memos.CreateMemoRequest{Memo: memos.Memo{
			Content:     content,
			Visibility:  e.options.Visibility,
			State:       "NORMAL",
			Pinned:      doc.Pinned,
			CreateTime:  doc.CreatedAt,
			Attachments: attachments,
		}})
		if err != nil {
			if ctx.Err() != nil {
				return e.cancelItem(context.Background(), jobID, item, ctx.Err())
			}
			return e.failItem(ctx, jobID, item, "create_memo", err)
		}
		memoID = memo.Name
	} else if documentUnchanged(mapping, doc, hash) {
		status = "skipped"
		memoID = mapping.MemoID
	} else if e.options.Strategy == StrategyOverwrite {
		memo, err := e.memos.UpdateMemo(ctx, mapping.MemoID, memos.UpdateMemoRequest{
			Memo: memos.Memo{Name: mapping.MemoID, Content: content, Attachments: attachments},
		})
		if err != nil {
			if ctx.Err() != nil {
				return e.cancelItem(context.Background(), jobID, item, ctx.Err())
			}
			return e.failItem(ctx, jobID, item, "update_memo", err)
		}
		status = "overwritten"
		memoID = memo.Name
		if memoID == "" {
			memoID = mapping.MemoID
		}
	} else {
		status = "skipped"
		memoID = mapping.MemoID
	}
	if status == "imported" || status == "overwritten" {
		if err := e.store.UpsertDocumentMapping(ctx, store.DocumentMapping{
			Source:          e.source.Name(),
			ExternalID:      doc.Ref.ID,
			MemoID:          memoID,
			SourceUpdatedAt: doc.UpdatedAt,
			ContentHash:     hash,
			ImportedAt:      time.Now().UTC(),
		}); err != nil {
			return e.failItem(ctx, jobID, item, "mapping", err)
		}
	}
	item.Status = status
	item.Error = ""
	item.ErrorStage = ""
	item.MemoID = memoID
	item.Warnings = warningsJSON
	if err := e.store.UpdateItem(ctx, item); err != nil {
		return err
	}
	e.publish(Event{JobID: jobID, Type: "item_" + status, ExternalID: item.ExternalID, Payload: item})
	return nil
}

func (e *Engine) prepareContent(ctx context.Context, doc *domain.Document) (string, []memos.Attachment, error) {
	content := ComposeContent(doc)
	replacements := make(map[string]string)
	memoAttachments := make([]memos.Attachment, 0, len(doc.Attachments))
	seenTokens := make(map[string]bool)
	for _, attachment := range doc.Attachments {
		if attachment.Token == "" {
			return "", nil, fmt.Errorf("attachment %s has empty token", attachment.ExternalID)
		}
		if seenTokens[attachment.Token] {
			return "", nil, fmt.Errorf("duplicate attachment token %s", attachment.Token)
		}
		seenTokens[attachment.Token] = true
		if !strings.Contains(content, attachment.Token) {
			return "", nil, fmt.Errorf("attachment token %s not found in content", attachment.Token)
		}
		mapping, err := e.store.GetAttachmentMapping(ctx, attachment.Source, attachment.ExternalID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", nil, err
		}
		if mapping == nil {
			uploaded, err := e.uploadAttachment(ctx, attachment)
			if err != nil {
				return "", nil, err
			}
			if uploaded.Name == "" {
				return "", nil, fmt.Errorf("memos attachment response missing name for %s", attachment.ExternalID)
			}
			mapping = &store.AttachmentMapping{
				Source:              attachment.Source,
				ExternalID:          attachment.ExternalID,
				MemosAttachmentName: uploaded.Name,
				UID:                 uploaded.UID,
				Filename:            sanitizeAttachmentFilename(firstNonEmpty(uploaded.Filename, attachment.Filename)),
				MimeType:            firstNonEmpty(uploaded.Type, attachment.MimeType),
				SizeBytes:           firstPositive(uploaded.Size, attachment.SizeBytes),
				ImportedAt:          time.Now().UTC(),
			}
			if mapping.UID == "" {
				mapping.UID = attachmentUIDFromName(mapping.MemosAttachmentName)
			}
			if mapping.UID == "" {
				return "", nil, fmt.Errorf("memos attachment response missing uid for %s", attachment.ExternalID)
			}
			if err := e.store.UpsertAttachmentMapping(ctx, *mapping); err != nil {
				return "", nil, err
			}
		}
		replacements[attachment.Token] = memos.AttachmentFilePath(mapping.UID, mapping.Filename)
		memoAttachments = append(memoAttachments, memos.Attachment{
			Name:     mapping.MemosAttachmentName,
			UID:      mapping.UID,
			Filename: mapping.Filename,
			Type:     mapping.MimeType,
			Size:     mapping.SizeBytes,
		})
	}
	for token, replacement := range replacements {
		content = strings.ReplaceAll(content, token, replacement)
	}
	for token := range seenTokens {
		if strings.Contains(content, token) {
			return "", nil, fmt.Errorf("attachment token %s was not replaced", token)
		}
	}
	return content, memoAttachments, nil
}

func (e *Engine) uploadAttachment(ctx context.Context, attachment domain.Attachment) (*memos.Attachment, error) {
	if attachment.Open == nil {
		return nil, fmt.Errorf("attachment %s cannot be opened", attachment.ExternalID)
	}
	rc, err := attachment.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := readBounded(rc, e.options.MaxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	return e.memos.CreateAttachment(ctx, memos.CreateAttachmentRequest{
		Filename: sanitizeAttachmentFilename(attachment.Filename),
		Type:     attachment.MimeType,
		Content:  data,
	})
}

func (e *Engine) failItem(ctx context.Context, jobID string, item store.ImportItem, stage string, err error) error {
	item.Status = "failed"
	item.ErrorStage = stage
	item.Error = sanitizeError(err)
	if item.Warnings == "" {
		item.Warnings = "[]"
	}
	_ = e.store.UpdateItem(ctx, item)
	e.publish(Event{JobID: jobID, Type: "item_failed", ExternalID: item.ExternalID, Payload: item})
	return err
}

func (e *Engine) cancelItem(ctx context.Context, jobID string, item store.ImportItem, err error) error {
	item.Status = "pending"
	item.ErrorStage = ""
	item.Error = ""
	if item.Warnings == "" {
		item.Warnings = "[]"
	}
	_ = e.store.UpdateItem(ctx, item)
	e.publish(Event{JobID: jobID, Type: "item_canceled", ExternalID: item.ExternalID, Payload: item})
	return err
}

func (e *Engine) publish(event Event) {
	e.broker.Publish(event)
}

// ComposeContent renders the memo body exactly as the importer sends it before attachment token replacement.
func ComposeContent(doc *domain.Document) string {
	var b strings.Builder
	title := strings.TrimSpace(doc.Ref.Title)
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(doc.Content))
	if len(doc.Tags) > 0 {
		b.WriteString("\n\n")
		for i, tag := range doc.Tags {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteByte('#')
			b.WriteString(sanitizeTag(tag))
		}
	}
	return strings.TrimSpace(b.String())
}

func contentHash(content string, attachments []memos.Attachment) string {
	h := sha256.New()
	h.Write([]byte(content))
	for _, attachment := range attachments {
		h.Write([]byte("\x00"))
		h.Write([]byte(attachment.Name))
		h.Write([]byte(attachment.UID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func documentUnchanged(mapping *store.DocumentMapping, doc *domain.Document, hash string) bool {
	if mapping == nil || mapping.ContentHash != hash {
		return false
	}
	if doc.UpdatedAt.IsZero() || mapping.SourceUpdatedAt.IsZero() {
		return true
	}
	return !doc.UpdatedAt.After(mapping.SourceUpdatedAt)
}

func marshalWarnings(warnings []domain.Warning) string {
	if len(warnings) == 0 {
		return "[]"
	}
	data, err := json.Marshal(warnings)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 32 << 20
	}
	lr := &io.LimitedReader{R: r, N: limit + 1}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("attachment exceeds size limit %d bytes", limit)
	}
	return data, nil
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return redact.Short(err.Error(), 512)
}

func sanitizeTag(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
	tag = strings.NewReplacer(" ", "_", "\t", "_", "\n", "_").Replace(tag)
	if tag == "" {
		return "untagged"
	}
	return tag
}

func sanitizeAttachmentFilename(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	filename = path.Base(filename)
	filename = strings.ReplaceAll(filename, "\x00", "")
	if filename == "" || filename == "." || filename == "/" || filename == ".." {
		return "attachment"
	}
	return filename
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func attachmentUIDFromName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
