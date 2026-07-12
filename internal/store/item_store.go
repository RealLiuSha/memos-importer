package store

import (
	"context"
	"database/sql"
	"time"
)

type ItemStatus string

const (
	ItemStatusPending     ItemStatus = "pending"
	ItemStatusRunning     ItemStatus = "running"
	ItemStatusImported    ItemStatus = "imported"
	ItemStatusOverwritten ItemStatus = "overwritten"
	ItemStatusSkipped     ItemStatus = "skipped"
	ItemStatusFailed      ItemStatus = "failed"
)

type ImportItem struct {
	JobID      string     `json:"job_id"`
	ExternalID string     `json:"external_id"`
	Title      string     `json:"title"`
	Status     ItemStatus `json:"status"`
	Warnings   string     `json:"warnings"`
	ErrorStage string     `json:"error_stage,omitempty"`
	Error      string     `json:"error,omitempty"`
	MemoID     string     `json:"memo_id,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (s *Store) ListItems(ctx context.Context, jobID string) ([]ImportItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, external_id, title, status, warnings,
		COALESCE(error_stage, ''), COALESCE(error, ''), COALESCE(memo_id, ''), updated_at
		FROM import_item WHERE job_id = ? ORDER BY title, external_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ImportItem
	for rows.Next() {
		var item ImportItem
		var updatedAt string
		if err := rows.Scan(&item.JobID, &item.ExternalID, &item.Title, &item.Status, &item.Warnings, &item.ErrorStage, &item.Error, &item.MemoID, &updatedAt); err != nil {
			return nil, err
		}
		t, err := scanTime(updatedAt)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt = t
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateItem(ctx context.Context, item ImportItem) error {
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	if item.Warnings == "" {
		item.Warnings = "[]"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE import_item
		SET title = ?, status = ?, warnings = ?, error_stage = ?, error = ?, memo_id = ?, updated_at = ?
		WHERE job_id = ? AND external_id = ?`,
		item.Title, item.Status, item.Warnings, item.ErrorStage, item.Error, item.MemoID, formatTime(item.UpdatedAt), item.JobID, item.ExternalID)
	return err
}

func (s *Store) PendingItems(ctx context.Context, jobID string) ([]ImportItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, external_id, title, status, warnings,
		COALESCE(error_stage, ''), COALESCE(error, ''), COALESCE(memo_id, ''), updated_at
		FROM import_item WHERE job_id = ? AND status IN ('pending', 'running') ORDER BY title, external_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) FailedItems(ctx context.Context, jobID string) ([]ImportItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, external_id, title, status, warnings,
		COALESCE(error_stage, ''), COALESCE(error, ''), COALESCE(memo_id, ''), updated_at
		FROM import_item WHERE job_id = ? AND status = 'failed' ORDER BY title, external_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) ResetItems(ctx context.Context, jobID string, externalIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range externalIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE import_item
			SET status = 'pending', error_stage = NULL, error = NULL, updated_at = ?
			WHERE job_id = ? AND external_id = ?`, nowUTC(), jobID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanItems(rows *sql.Rows) ([]ImportItem, error) {
	var items []ImportItem
	for rows.Next() {
		var item ImportItem
		var updatedAt string
		if err := rows.Scan(&item.JobID, &item.ExternalID, &item.Title, &item.Status, &item.Warnings, &item.ErrorStage, &item.Error, &item.MemoID, &updatedAt); err != nil {
			return nil, err
		}
		t, err := scanTime(updatedAt)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt = t
		items = append(items, item)
	}
	return items, rows.Err()
}
