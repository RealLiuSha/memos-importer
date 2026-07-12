package store

import (
	"context"
	"database/sql"
	"time"
)

type JobStatus string

const (
	JobStatusPending  JobStatus = "pending"
	JobStatusRunning  JobStatus = "running"
	JobStatusDone     JobStatus = "done"
	JobStatusFailed   JobStatus = "failed"
	JobStatusCanceled JobStatus = "canceled"
)

type Job struct {
	ID         string     `json:"id"`
	Source     string     `json:"source"`
	Status     JobStatus  `json:"status"`
	Options    string     `json:"options"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type JobSummary struct {
	Total           int `json:"total"`
	Pending         int `json:"pending"`
	Running         int `json:"running"`
	Imported        int `json:"imported"`
	Overwritten     int `json:"overwritten"`
	Skipped         int `json:"skipped"`
	Failed          int `json:"failed"`
	Completed       int `json:"completed"`
	ProgressPercent int `json:"progress_percent"`
}

type JobWithSummary struct {
	Job
	Summary JobSummary `json:"summary"`
}

func (s *Store) CreateJob(ctx context.Context, job Job, items []ImportItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO import_job(id, source, status, options, created_at, started_at, finished_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Source, job.Status, job.Options, formatTime(job.CreatedAt), nullableTime(job.StartedAt), nullableTime(job.FinishedAt), job.Error); err != nil {
		return err
	}
	for _, item := range items {
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = job.CreatedAt
		}
		if item.Warnings == "" {
			item.Warnings = "[]"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO import_item(job_id, external_id, title, status, warnings, error_stage, error, memo_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID, item.ExternalID, item.Title, item.Status, item.Warnings, item.ErrorStage, item.Error, item.MemoID, formatTime(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateJobStatus(ctx context.Context, id string, status JobStatus, errText string) error {
	now := nowUTC()
	switch status {
	case JobStatusRunning:
		_, err := s.db.ExecContext(ctx, `UPDATE import_job
			SET status = ?, started_at = ?, finished_at = NULL, error = ?
			WHERE id = ?`, status, now, errText, id)
		return err
	case JobStatusDone, JobStatusFailed, JobStatusCanceled:
		_, err := s.db.ExecContext(ctx, `UPDATE import_job
			SET status = ?, finished_at = ?, error = ?
			WHERE id = ?`, status, now, errText, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE import_job
		SET status = ?, error = ?
		WHERE id = ?`, status, errText, id)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	var job Job
	var createdAt string
	var startedAt, finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, source, status, options, created_at, started_at, finished_at, COALESCE(error, '')
		FROM import_job WHERE id = ?`, id).
		Scan(&job.ID, &job.Source, &job.Status, &job.Options, &createdAt, &startedAt, &finishedAt, &job.Error)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := parseJobTimes(&job, createdAt, startedAt, finishedAt); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) ListJobsWithSummary(ctx context.Context, limit int) ([]JobWithSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT j.id, j.source, j.status, j.options, j.created_at, j.started_at, j.finished_at, COALESCE(j.error, ''),
		COUNT(i.external_id),
		COALESCE(SUM(CASE WHEN i.status = 'pending' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.status = 'running' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.status = 'imported' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.status = 'overwritten' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.status = 'skipped' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT id, source, status, options, created_at, started_at, finished_at, error
			FROM import_job
			ORDER BY created_at DESC
			LIMIT ?
		) AS j
		LEFT JOIN import_item AS i ON i.job_id = j.id
		GROUP BY j.id, j.source, j.status, j.options, j.created_at, j.started_at, j.finished_at, j.error
		ORDER BY j.created_at DESC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []JobWithSummary
	for rows.Next() {
		var entry JobWithSummary
		var createdAt string
		var startedAt, finishedAt sql.NullString
		var summary JobSummary
		if err := rows.Scan(&entry.ID, &entry.Source, &entry.Status, &entry.Options, &createdAt, &startedAt, &finishedAt, &entry.Error,
			&summary.Total, &summary.Pending, &summary.Running, &summary.Imported, &summary.Overwritten, &summary.Skipped, &summary.Failed); err != nil {
			return nil, err
		}
		if err := parseJobTimes(&entry.Job, createdAt, startedAt, finishedAt); err != nil {
			return nil, err
		}
		entry.Summary = finalizeJobSummary(summary)
		jobs = append(jobs, entry)
	}
	return jobs, rows.Err()
}

func SummarizeItems(items []ImportItem) JobSummary {
	var summary JobSummary
	for _, item := range items {
		summary.Total++
		switch item.Status {
		case ItemStatusPending:
			summary.Pending++
		case ItemStatusRunning:
			summary.Running++
		case ItemStatusImported:
			summary.Imported++
		case ItemStatusOverwritten:
			summary.Overwritten++
		case ItemStatusSkipped:
			summary.Skipped++
		case ItemStatusFailed:
			summary.Failed++
		}
	}
	return finalizeJobSummary(summary)
}

func parseJobTimes(job *Job, createdAt string, startedAt, finishedAt sql.NullString) error {
	t, err := scanTime(createdAt)
	if err != nil {
		return err
	}
	job.CreatedAt = t
	if startedAt.Valid && startedAt.String != "" {
		t, err := scanTime(startedAt.String)
		if err != nil {
			return err
		}
		job.StartedAt = &t
	}
	if finishedAt.Valid && finishedAt.String != "" {
		t, err := scanTime(finishedAt.String)
		if err != nil {
			return err
		}
		job.FinishedAt = &t
	}
	return nil
}

func finalizeJobSummary(summary JobSummary) JobSummary {
	summary.Completed = summary.Imported + summary.Overwritten + summary.Skipped + summary.Failed
	if summary.Total > 0 {
		summary.ProgressPercent = summary.Completed * 100 / summary.Total
	}
	return summary
}

func nullableTime(t *time.Time) interface{} {
	if t == nil || t.IsZero() {
		return nil
	}
	return formatTime(*t)
}
