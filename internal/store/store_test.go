package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConfigAndMappings(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}

	updated := time.Date(2026, 7, 7, 1, 2, 3, 0, time.UTC)
	if err := s.UpsertDocumentMapping(ctx, DocumentMapping{
		Source: "notion", ExternalID: "page-1", MemoID: "memos/1", SourceUpdatedAt: updated, ContentHash: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetDocumentMapping(ctx, "notion", "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if m.MemoID != "memos/1" || m.ContentHash != "h1" || !m.SourceUpdatedAt.Equal(updated) {
		t.Fatalf("unexpected mapping: %#v", m)
	}

	if _, err := s.GetDocumentMapping(ctx, "notion", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := s.UpsertAttachmentMapping(ctx, AttachmentMapping{
		Source: "notion", ExternalID: "block-1", MemosAttachmentName: "attachments/1", UID: "u1", Filename: "a.png", MimeType: "image/png", SizeBytes: 12,
	}); err != nil {
		t.Fatal(err)
	}
	am, err := s.GetAttachmentMapping(ctx, "notion", "block-1")
	if err != nil {
		t.Fatal(err)
	}
	if am.MemosAttachmentName != "attachments/1" || am.UID != "u1" || am.Filename != "a.png" ||
		am.MimeType != "image/png" || am.SizeBytes != 12 || am.ImportedAt.IsZero() {
		t.Fatalf("unexpected attachment mapping: %#v", am)
	}
}

func TestJobLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	job := Job{ID: "job-1", Source: "notion", Status: "pending", Options: "{}"}
	items := []ImportItem{{ExternalID: "page-1", Title: "Page", Status: "pending"}}
	if err := s.CreateJob(ctx, job, items); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateJobStatus(ctx, "job-1", "running", ""); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingItems(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(pending))
	}
	pending[0].Status = "failed"
	pending[0].ErrorStage = "fetch"
	pending[0].Error = "boom"
	if err := s.UpdateItem(ctx, pending[0]); err != nil {
		t.Fatal(err)
	}
	failed, err := s.FailedItems(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].ErrorStage != "fetch" {
		t.Fatalf("unexpected failed items: %#v", failed)
	}
	if err := s.UpdateJobStatus(ctx, "job-1", "failed", "1 item(s) failed"); err != nil {
		t.Fatal(err)
	}
	failedJob, err := s.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if failedJob.FinishedAt == nil || failedJob.Error != "1 item(s) failed" {
		t.Fatalf("failed job should have finish metadata: %#v", failedJob)
	}
	if err := s.UpdateJobStatus(ctx, "job-1", "running", ""); err != nil {
		t.Fatal(err)
	}
	runningJob, err := s.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if runningJob.Status != "running" || runningJob.FinishedAt != nil || runningJob.Error != "" {
		t.Fatalf("running job should clear previous finish metadata: %#v", runningJob)
	}
}

func TestListJobsWithSummary(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	job := Job{ID: "job-summary", Source: "notion", Status: "running", Options: "{}"}
	items := []ImportItem{
		{ExternalID: "page-1", Title: "Page 1", Status: "imported"},
		{ExternalID: "page-2", Title: "Page 2", Status: "overwritten"},
		{ExternalID: "page-3", Title: "Page 3", Status: "skipped"},
		{ExternalID: "page-4", Title: "Page 4", Status: "failed"},
		{ExternalID: "page-5", Title: "Page 5", Status: "pending"},
		{ExternalID: "page-6", Title: "Page 6", Status: "running"},
	}
	if err := s.CreateJob(ctx, job, items); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobsWithSummary(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %#v", jobs)
	}
	summary := jobs[0].Summary
	if summary.Total != 6 || summary.Imported != 1 || summary.Overwritten != 1 || summary.Skipped != 1 || summary.Failed != 1 || summary.Pending != 1 || summary.Running != 1 {
		t.Fatalf("unexpected summary counts: %#v", summary)
	}
	if summary.Completed != 4 || summary.ProgressPercent != 66 {
		t.Fatalf("unexpected summary progress: %#v", summary)
	}
}
