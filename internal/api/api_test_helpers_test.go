package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"memos-importer/internal/domain"
	"memos-importer/internal/memos"
	"memos-importer/internal/store"
)

func openAPIStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func waitBrokerSubscribers(t *testing.T, s *Server, jobID string, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		got := s.broker.SubscriberCount(jobID)
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected %d broker subscribers for %s", want, jobID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type fakeRuntime struct {
	version  string
	limit    int64
	limitErr error
	created  int
}

func (f *fakeRuntime) VerifyMinVersion(ctx context.Context, min string) (*memos.InstanceProfile, error) {
	if f.version < min {
		return &memos.InstanceProfile{Version: f.version}, errString("memos version " + f.version + " is lower than required " + min)
	}
	return &memos.InstanceProfile{Version: f.version}, nil
}

func (f *fakeRuntime) ContentLengthLimit(ctx context.Context) (int64, error) {
	if f.limitErr != nil {
		return 0, f.limitErr
	}
	return f.limit, nil
}

func (f *fakeRuntime) CreateAttachment(ctx context.Context, req memos.CreateAttachmentRequest) (*memos.Attachment, error) {
	return &memos.Attachment{Name: "attachments/1", UID: "uid1", Filename: req.Filename, Type: req.Type, Size: int64(len(req.Content))}, nil
}

func (f *fakeRuntime) CreateMemo(ctx context.Context, req memos.CreateMemoRequest) (*memos.Memo, error) {
	f.created++
	return &memos.Memo{Name: "memos/1", Content: req.Memo.Content}, nil
}

func (f *fakeRuntime) UpdateMemo(ctx context.Context, name string, req memos.UpdateMemoRequest) (*memos.Memo, error) {
	return &memos.Memo{Name: name, Content: req.Memo.Content}, nil
}

type fakeAPISource struct {
	docs        map[string]*domain.Document
	beforeFetch func(ctx context.Context, id string)
}

func (f fakeAPISource) Name() string                     { return "notion" }
func (f fakeAPISource) Verify(ctx context.Context) error { return nil }
func (f fakeAPISource) ListDocuments(ctx context.Context) ([]domain.DocumentRef, error) {
	refs := make([]domain.DocumentRef, 0, len(f.docs))
	for _, doc := range f.docs {
		refs = append(refs, doc.Ref)
	}
	return refs, nil
}
func (f fakeAPISource) FetchDocument(ctx context.Context, id string) (*domain.Document, error) {
	if f.beforeFetch != nil {
		f.beforeFetch(ctx, id)
	}
	return f.docs[id], nil
}

type fakeExpandingSource struct {
	fakeAPISource
	expanded map[string][]string
}

func (f fakeExpandingSource) ExpandDocumentIDs(ctx context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if expanded := f.expanded[id]; len(expanded) > 0 {
			out = append(out, expanded...)
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func apiDoc(id, content string) *domain.Document {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return &domain.Document{Ref: domain.DocumentRef{Source: "notion", ID: id, Title: id, UpdatedAt: now}, Content: content, CreatedAt: now, UpdatedAt: now}
}
