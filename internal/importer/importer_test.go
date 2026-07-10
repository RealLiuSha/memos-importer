package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"memos-importer/internal/domain"
	"memos-importer/internal/memos"
	"memos-importer/internal/store"
)

type fakeSource struct {
	docs        map[string]*domain.Document
	failures    map[string]error
	beforeFetch func(ctx context.Context, id string)
}

func (f fakeSource) Name() string                     { return "notion" }
func (f fakeSource) Verify(ctx context.Context) error { return nil }
func (f fakeSource) ListDocuments(ctx context.Context) ([]domain.DocumentRef, error) {
	refs := make([]domain.DocumentRef, 0, len(f.docs))
	for _, doc := range f.docs {
		refs = append(refs, doc.Ref)
	}
	return refs, nil
}
func (f fakeSource) FetchDocument(ctx context.Context, id string) (*domain.Document, error) {
	if f.beforeFetch != nil {
		f.beforeFetch(ctx, id)
	}
	if err := f.failures[id]; err != nil {
		return nil, err
	}
	return f.docs[id], nil
}

type fakeMemos struct {
	mu                sync.Mutex
	created           int
	updated           int
	uploaded          int
	createErr         error
	updateErr         error
	attachmentResp    *memos.Attachment
	lastAttachmentReq memos.CreateAttachmentRequest
	lastCreateRequest memos.CreateMemoRequest
	lastUpdateRequest memos.UpdateMemoRequest
}

func (f *fakeMemos) CreateAttachment(ctx context.Context, req memos.CreateAttachmentRequest) (*memos.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded++
	f.lastAttachmentReq = req
	if f.attachmentResp != nil {
		return f.attachmentResp, nil
	}
	return &memos.Attachment{Name: "attachments/1", UID: "uid1", Filename: req.Filename, Type: req.Type, Size: int64(len(req.Content))}, nil
}
func (f *fakeMemos) CreateMemo(ctx context.Context, req memos.CreateMemoRequest) (*memos.Memo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	f.lastCreateRequest = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	if req.Memo.CreateTime.IsZero() {
		return nil, context.Canceled
	}
	if strings.Contains(req.Memo.Content, "__MEMOS_IMPORTER_ATTACHMENT_") {
		return nil, context.DeadlineExceeded
	}
	return &memos.Memo{Name: "memos/1", Content: req.Memo.Content}, nil
}
func (f *fakeMemos) UpdateMemo(ctx context.Context, name string, req memos.UpdateMemoRequest) (*memos.Memo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated++
	f.lastUpdateRequest = req
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &memos.Memo{Name: name, Content: req.Memo.Content}, nil
}

func TestImportCreatesMappingAndSkipsDuplicate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := "__MEMOS_IMPORTER_ATTACHMENT_test__"
	doc := &domain.Document{
		Ref:       domain.DocumentRef{Source: "notion", ID: "page-1", Title: "Page", UpdatedAt: time.Now()},
		Content:   "body ![a](" + token + ")",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Tags:      []string{"tag one"},
		Attachments: []domain.Attachment{{
			Source: "notion", ExternalID: "block-1", Filename: "a.txt", MimeType: "text/plain", Token: token,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		}},
	}
	fm := &fakeMemos{}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{WorkerCount: 1, Visibility: VisibilityPrivate})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.created != 1 {
		t.Fatalf("expected one create, got %d", fm.created)
	}
	if !fm.lastCreateRequest.Memo.CreateTime.Equal(doc.CreatedAt) {
		t.Fatalf("create_time not preserved: %#v", fm.lastCreateRequest.Memo.CreateTime)
	}
	if fm.lastCreateRequest.Memo.Visibility != VisibilityPrivate {
		t.Fatalf("unexpected visibility: %s", fm.lastCreateRequest.Memo.Visibility)
	}
	if !strings.Contains(fm.lastCreateRequest.Memo.Content, "/file/attachments/uid1/a.txt") {
		t.Fatalf("attachment token was not replaced: %s", fm.lastCreateRequest.Memo.Content)
	}
	mapping, err := st.GetDocumentMapping(ctx, "notion", "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.MemoID != "memos/1" {
		t.Fatalf("unexpected mapping: %#v", mapping)
	}

	jobID, err = engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.created != 1 {
		t.Fatalf("duplicate import should skip create, got %d creates", fm.created)
	}
	if fm.uploaded != 1 {
		t.Fatalf("duplicate import should reuse attachment mapping, got %d uploads", fm.uploaded)
	}
}

func TestImportSanitizesAttachmentFilename(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := "__MEMOS_IMPORTER_ATTACHMENT_filename__"
	doc := &domain.Document{
		Ref:       domain.DocumentRef{Source: "notion", ID: "page-1", Title: "Page", UpdatedAt: time.Now()},
		Content:   "body ![a](" + token + ")",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Attachments: []domain.Attachment{{
			Source: "notion", ExternalID: "block-1", Filename: `..\evil` + "\x00" + `.png`, MimeType: "image/png", Token: token,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello")), nil
			},
		}},
	}
	fm := &fakeMemos{}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.lastAttachmentReq.Filename != "evil.png" {
		t.Fatalf("attachment upload filename was not sanitized: %#v", fm.lastAttachmentReq)
	}
	if !strings.Contains(fm.lastCreateRequest.Memo.Content, "/file/attachments/uid1/evil.png") {
		t.Fatalf("memo content did not use sanitized attachment path: %s", fm.lastCreateRequest.Memo.Content)
	}
	mapping, err := st.GetAttachmentMapping(ctx, "notion", "block-1")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Filename != "evil.png" {
		t.Fatalf("attachment mapping filename was not sanitized: %#v", mapping)
	}
}

func TestCreateJobDeduplicatesExternalIDs(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	doc := newTestDocument("page-1", "body")
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1", " ", "page-1"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ExternalID != "page-1" {
		t.Fatalf("expected one deduplicated item, got %#v", items)
	}
}

func TestCreateJobRejectsInvalidOptions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	engine := NewEngine(fakeSource{}, &fakeMemos{}, st, NewBroker(), Options{Strategy: "merge", WorkerCount: 1})
	if _, err := engine.CreateJob(ctx, []string{"page-1"}); err == nil || !strings.Contains(err.Error(), "invalid strategy") {
		t.Fatalf("expected invalid strategy error, got %v", err)
	}
}

func TestImportOverwriteChangedMapping(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	doc := newTestDocument("page-1", "new body")
	if err := st.UpsertDocumentMapping(ctx, store.DocumentMapping{
		Source: "notion", ExternalID: "page-1", MemoID: "memos/old", SourceUpdatedAt: time.Now().Add(-time.Hour), ContentHash: "old",
	}); err != nil {
		t.Fatal(err)
	}
	fm := &fakeMemos{}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{Strategy: StrategyOverwrite, WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.updated != 1 {
		t.Fatalf("expected overwrite patch, updated=%d req=%#v", fm.updated, fm.lastUpdateRequest)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "overwritten" {
		t.Fatalf("unexpected status: %#v", items[0])
	}
}

func TestImportOverwriteWhenSourceUpdatedAtAdvancesWithSameHash(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	doc := newTestDocument("page-1", "same body")
	fm := &fakeMemos{}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{Strategy: StrategyOverwrite, WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.created != 1 {
		t.Fatalf("expected initial create, got %d", fm.created)
	}
	nextUpdatedAt := doc.UpdatedAt.Add(time.Hour)
	doc.UpdatedAt = nextUpdatedAt
	jobID, err = engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.updated != 1 {
		t.Fatalf("source_updated_at advance should trigger overwrite even when hash matches, got %d updates", fm.updated)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "overwritten" {
		t.Fatalf("expected overwritten item: %#v", items[0])
	}
	mapping, err := st.GetDocumentMapping(ctx, "notion", "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if !mapping.SourceUpdatedAt.Equal(nextUpdatedAt) {
		t.Fatalf("mapping source_updated_at was not refreshed: %#v", mapping)
	}
}

func TestImportSkipChangedMappingDoesNotUpdateMemo(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldUpdated := time.Now().Add(-time.Hour)
	if err := st.UpsertDocumentMapping(ctx, store.DocumentMapping{
		Source: "notion", ExternalID: "page-1", MemoID: "memos/old", SourceUpdatedAt: oldUpdated, ContentHash: "old-hash",
	}); err != nil {
		t.Fatal(err)
	}
	doc := newTestDocument("page-1", "changed body")
	fm := &fakeMemos{}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{Strategy: StrategySkip, WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if fm.created != 0 || fm.updated != 0 {
		t.Fatalf("skip strategy should not write memos, created=%d updated=%d", fm.created, fm.updated)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "skipped" || items[0].MemoID != "memos/old" {
		t.Fatalf("changed mapped item should be skipped against existing memo: %#v", items[0])
	}
	mapping, err := st.GetDocumentMapping(ctx, "notion", "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.ContentHash != "old-hash" || !mapping.SourceUpdatedAt.Equal(oldUpdated) {
		t.Fatalf("skip strategy should preserve old mapping until overwrite: %#v", mapping)
	}
}

func TestImportFailureIsolationAndRetry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docs := map[string]*domain.Document{
		"page-ok":   newTestDocument("page-ok", "ok"),
		"page-fail": newTestDocument("page-fail", "fail"),
	}
	src := fakeSource{docs: docs, failures: map[string]error{"page-fail": errors.New("fetch failed")}}
	fm := &fakeMemos{}
	engine := NewEngine(src, fm, st, NewBroker(), Options{WorkerCount: 2})
	jobID, err := engine.CreateJob(ctx, []string{"page-ok", "page-fail"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range items {
		statuses[item.ExternalID] = item.Status
	}
	if statuses["page-ok"] != "imported" || statuses["page-fail"] != "failed" {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" {
		t.Fatalf("job should be failed after item failure: %#v", job)
	}

	src.failures = nil
	engine = NewEngine(src, fm, st, NewBroker(), Options{WorkerCount: 1})
	if err := engine.RetryFailed(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err = st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status != "imported" && item.Status != "skipped" {
			t.Fatalf("retry should resolve failed item, got %#v", item)
		}
	}
}

func TestImportNilDocumentFailsItem(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{}}, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" || items[0].ErrorStage != "fetch" {
		t.Fatalf("nil document should fail item without panic: %#v", items)
	}
	if !strings.Contains(items[0].Error, "source returned nil document") {
		t.Fatalf("unexpected error for nil document: %#v", items[0])
	}
}

func TestImportWorkerCountDoesNotExceedLimit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docs := make(map[string]*domain.Document)
	ids := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("page-%d", i)
		ids = append(ids, id)
		docs[id] = newTestDocument(id, "body")
	}
	var inFlight int32
	var maxInFlight int32
	src := fakeSource{
		docs: docs,
		beforeFetch: func(ctx context.Context, id string) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				max := atomic.LoadInt32(&maxInFlight)
				if current <= max || atomic.CompareAndSwapInt32(&maxInFlight, max, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		},
	}
	engine := NewEngine(src, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 2})
	jobID, err := engine.CreateJob(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&maxInFlight); got > 2 {
		t.Fatalf("worker concurrency exceeded limit: %d", got)
	}
}

func TestImportContentLengthAndAttachmentSizeFailures(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	longDoc := newTestDocument("long", strings.Repeat("x", 32))
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"long": longDoc}}, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 1, ContentLengthLimit: 10})
	jobID, err := engine.CreateJob(ctx, []string{"long"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "failed" || items[0].ErrorStage != "content_length" {
		t.Fatalf("expected content_length failure: %#v", items[0])
	}

	token := "__MEMOS_IMPORTER_ATTACHMENT_big__"
	bigDoc := newTestDocument("big", "file "+token)
	bigDoc.Attachments = []domain.Attachment{{
		Source: "notion", ExternalID: "big-file", Filename: "big.bin", MimeType: "application/octet-stream", Token: token,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("0123456789abcdef")), nil
		},
	}}
	engine = NewEngine(fakeSource{docs: map[string]*domain.Document{"big": bigDoc}}, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 1, MaxAttachmentBytes: 4})
	jobID, err = engine.CreateJob(ctx, []string{"big"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err = st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "failed" || items[0].ErrorStage != "attachment" {
		t.Fatalf("expected attachment failure: %#v", items[0])
	}
}

func TestImportFailsWhenUploadedAttachmentResponseMissingName(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := "__MEMOS_IMPORTER_ATTACHMENT_missing_name__"
	doc := newTestDocument("page-1", "file "+token)
	doc.Attachments = []domain.Attachment{{
		Source: "notion", ExternalID: "block-1", Filename: "a.txt", MimeType: "text/plain", Token: token,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("hello")), nil
		},
	}}
	fm := &fakeMemos{attachmentResp: &memos.Attachment{UID: "uid1", Filename: "a.txt", Type: "text/plain", Size: 5}}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": doc}}, fm, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" || items[0].ErrorStage != "attachment" {
		t.Fatalf("expected attachment response failure: %#v", items)
	}
	if !strings.Contains(items[0].Error, "missing name") {
		t.Fatalf("expected missing name error, got %#v", items[0])
	}
	if fm.created != 0 {
		t.Fatalf("memo should not be created after invalid attachment response, got %d creates", fm.created)
	}
	if _, err := st.GetAttachmentMapping(ctx, "notion", "block-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid attachment response should not create mapping, err=%v", err)
	}
}

func TestImportFailureRedactsSensitiveError(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fm := &fakeMemos{createErr: errors.New(`Authorization: Bearer raw-token token=secret temporary https://user:pass@notion.example/file.png?X-Amz-Signature=secret`)}
	engine := NewEngine(fakeSource{docs: map[string]*domain.Document{"page-1": newTestDocument("page-1", "body")}}, fm, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(ctx, []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("expected failed item, got %#v", items)
	}
	for _, forbidden := range []string{"raw-token", "token=secret", "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(items[0].Error, forbidden) {
			t.Fatalf("stored item error leaked %q: %s", forbidden, items[0].Error)
		}
	}
	if !strings.Contains(items[0].Error, "redacted") {
		t.Fatalf("expected redacted item error, got %q", items[0].Error)
	}
}

func TestImportCancelKeepsUnfinishedItemPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := fakeSource{
		docs: map[string]*domain.Document{"page-1": newTestDocument("page-1", "body")},
		beforeFetch: func(ctx context.Context, id string) {
			cancel()
			<-ctx.Done()
		},
	}
	engine := NewEngine(src, &fakeMemos{}, st, NewBroker(), Options{WorkerCount: 1})
	jobID, err := engine.CreateJob(context.Background(), []string{"page-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RunJob(ctx, jobID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	job, err := st.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "canceled" {
		t.Fatalf("expected canceled job, got %#v", job)
	}
	items, err := st.ListItems(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "pending" {
		t.Fatalf("unfinished item should stay pending: %#v", items[0])
	}
}

func newTestDocument(id, content string) *domain.Document {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return &domain.Document{
		Ref:       domain.DocumentRef{Source: "notion", ID: id, Title: id, UpdatedAt: now},
		Content:   content,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
}
