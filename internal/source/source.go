package source

import (
	"context"

	"memos-importer/internal/domain"
)

type Source interface {
	Name() string
	Verify(ctx context.Context) error
	ListDocuments(ctx context.Context, options ListOptions) (DocumentList, error)
	FetchDocument(ctx context.Context, id string) (*domain.Document, error)
}

type ListOptions struct {
	Limit int
}

type DocumentList struct {
	Documents []domain.DocumentRef
	HasMore   bool
}
