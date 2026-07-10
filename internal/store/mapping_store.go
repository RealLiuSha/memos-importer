package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type DocumentMapping struct {
	Source          string
	ExternalID      string
	MemoID          string
	SourceUpdatedAt time.Time
	ContentHash     string
	ImportedAt      time.Time
}

type AttachmentMapping struct {
	Source              string
	ExternalID          string
	MemosAttachmentName string
	UID                 string
	Filename            string
	MimeType            string
	SizeBytes           int64
	ImportedAt          time.Time
}

func (s *Store) GetDocumentMapping(ctx context.Context, source, externalID string) (*DocumentMapping, error) {
	var m DocumentMapping
	var sourceUpdatedAt, importedAt string
	err := s.db.QueryRowContext(ctx, `SELECT source, external_id, memo_id, source_updated_at, content_hash, imported_at
		FROM document_mapping WHERE source = ? AND external_id = ?`, source, externalID).
		Scan(&m.Source, &m.ExternalID, &m.MemoID, &sourceUpdatedAt, &m.ContentHash, &importedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var parseErr error
	m.SourceUpdatedAt, parseErr = scanTime(sourceUpdatedAt)
	if parseErr != nil {
		return nil, parseErr
	}
	m.ImportedAt, parseErr = scanTime(importedAt)
	if parseErr != nil {
		return nil, parseErr
	}
	return &m, nil
}

func (s *Store) UpsertDocumentMapping(ctx context.Context, m DocumentMapping) error {
	if m.ImportedAt.IsZero() {
		m.ImportedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_mapping(source, external_id, memo_id, source_updated_at, content_hash, imported_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source, external_id) DO UPDATE SET
			memo_id = excluded.memo_id,
			source_updated_at = excluded.source_updated_at,
			content_hash = excluded.content_hash,
			imported_at = excluded.imported_at`,
		m.Source, m.ExternalID, m.MemoID, formatTime(m.SourceUpdatedAt), m.ContentHash, formatTime(m.ImportedAt))
	return err
}

func (s *Store) GetAttachmentMapping(ctx context.Context, source, externalID string) (*AttachmentMapping, error) {
	var m AttachmentMapping
	var importedAt string
	err := s.db.QueryRowContext(ctx, `SELECT source, external_id, memos_attachment_name, uid, filename, mime_type, size_bytes, imported_at
		FROM attachment_mapping WHERE source = ? AND external_id = ?`, source, externalID).
		Scan(&m.Source, &m.ExternalID, &m.MemosAttachmentName, &m.UID, &m.Filename, &m.MimeType, &m.SizeBytes, &importedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t, err := scanTime(importedAt)
	if err != nil {
		return nil, err
	}
	m.ImportedAt = t
	return &m, nil
}

func (s *Store) UpsertAttachmentMapping(ctx context.Context, m AttachmentMapping) error {
	if m.ImportedAt.IsZero() {
		m.ImportedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO attachment_mapping(source, external_id, memos_attachment_name, uid, filename, mime_type, size_bytes, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source, external_id) DO UPDATE SET
			memos_attachment_name = excluded.memos_attachment_name,
			uid = excluded.uid,
			filename = excluded.filename,
			mime_type = excluded.mime_type,
			size_bytes = excluded.size_bytes,
			imported_at = excluded.imported_at`,
		m.Source, m.ExternalID, m.MemosAttachmentName, m.UID, m.Filename, m.MimeType, m.SizeBytes, formatTime(m.ImportedAt))
	return err
}

func (s *Store) ListAttachmentMappings(ctx context.Context) ([]AttachmentMapping, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, external_id, memos_attachment_name, uid, filename, mime_type, size_bytes, imported_at
		FROM attachment_mapping ORDER BY source, external_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []AttachmentMapping
	for rows.Next() {
		var m AttachmentMapping
		var importedAt string
		if err := rows.Scan(&m.Source, &m.ExternalID, &m.MemosAttachmentName, &m.UID, &m.Filename, &m.MimeType, &m.SizeBytes, &importedAt); err != nil {
			return nil, err
		}
		t, err := scanTime(importedAt)
		if err != nil {
			return nil, err
		}
		m.ImportedAt = t
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}
