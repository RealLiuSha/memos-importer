package store

import "context"

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS document_mapping (
			source TEXT NOT NULL,
			external_id TEXT NOT NULL,
			memo_id TEXT NOT NULL,
			source_updated_at TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			imported_at TEXT NOT NULL,
			PRIMARY KEY (source, external_id)
		)`,
		`CREATE TABLE IF NOT EXISTS attachment_mapping (
			source TEXT NOT NULL,
			external_id TEXT NOT NULL,
			memos_attachment_name TEXT NOT NULL,
			uid TEXT NOT NULL,
			filename TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			imported_at TEXT NOT NULL,
			PRIMARY KEY (source, external_id)
		)`,
		`CREATE TABLE IF NOT EXISTS import_job (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			status TEXT NOT NULL,
			options TEXT NOT NULL,
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT,
			error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS import_item (
			job_id TEXT NOT NULL REFERENCES import_job(id) ON DELETE CASCADE,
			external_id TEXT NOT NULL,
			title TEXT NOT NULL,
			status TEXT NOT NULL,
			warnings TEXT NOT NULL DEFAULT '[]',
			error_stage TEXT,
			error TEXT,
			memo_id TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (job_id, external_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_import_job_created_at ON import_job(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_import_item_status ON import_item(job_id, status)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
