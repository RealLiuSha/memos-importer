package importer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
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

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return redact.Short(err.Error(), 512)
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
