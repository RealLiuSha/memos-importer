package domain

import (
	"context"
	"io"
	"time"
)

type DocumentRef struct {
	Source    string    `json:"source"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ParentID  string    `json:"parent_id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Document struct {
	Ref         DocumentRef `json:"ref"`
	Content     string      `json:"content"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Tags        []string    `json:"tags,omitempty"`
	Pinned      bool        `json:"pinned"`
	Warnings    []Warning   `json:"warnings,omitempty"`
	Attachments []Attachment
}

type Attachment struct {
	Source     string
	ExternalID string
	Filename   string
	MimeType   string
	SizeBytes  int64
	Token      string
	Open       func(ctx context.Context) (io.ReadCloser, error)
}
