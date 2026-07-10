package source

import (
	"context"

	"memos-importer/internal/domain"
)

type Source interface {
	Name() string
	Verify(ctx context.Context) error
	ListDocuments(ctx context.Context) ([]domain.DocumentRef, error)
	FetchDocument(ctx context.Context, id string) (*domain.Document, error)
}

type Factory func(Config) (Source, error)

type Config struct {
	Name       string
	Token      string
	TimeSource string
}
