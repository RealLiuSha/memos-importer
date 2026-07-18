package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memos-importer/internal/config"
	"memos-importer/internal/domain"
	"memos-importer/internal/importer"
	"memos-importer/internal/source"
)

func TestNotionTreeAndPreviewExposeWarnings(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	var listOptions source.ListOptions
	src := fakeAPISource{listOptions: &listOptions, listHasMore: true, docs: map[string]*domain.Document{
		"page-1": {
			Ref:       domain.DocumentRef{Source: "notion", ID: "page-1", Title: "Page", UpdatedAt: time.Now()},
			Content:   "<!-- Unsupported Notion block: synced_block -->",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Warnings:  []domain.Warning{domain.NewWarning("unsupported_block", "unsupported Notion block type: synced_block", "block-1")},
		},
	}}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return src, nil
	}
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	router := s.Router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/tree", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "page-1") {
		t.Fatalf("unexpected tree response %d: %s", rec.Code, rec.Body.String())
	}
	if listOptions.Limit != defaultNotionDocumentLimit || !strings.Contains(rec.Body.String(), `"has_more":true`) {
		t.Fatalf("tree did not use the default limit or expose has_more: options=%#v body=%s", listOptions, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/documents/page-1/preview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected preview status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported_block") || !strings.Contains(rec.Body.String(), "Unsupported Notion block") {
		t.Fatalf("preview did not expose warning/placeholder: %s", rec.Body.String())
	}
}

func TestNotionTreeValidatesDocumentLimit(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantLimit  int
	}{
		{name: "custom", query: "?limit=250", wantStatus: http.StatusOK, wantLimit: 250},
		{name: "zero", query: "?limit=0", wantStatus: http.StatusBadRequest},
		{name: "negative", query: "?limit=-1", wantStatus: http.StatusBadRequest},
		{name: "non integer", query: "?limit=many", wantStatus: http.StatusBadRequest},
		{name: "over maximum", query: "?limit=1001", wantStatus: http.StatusBadRequest},
		{name: "empty", query: "?limit=", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openAPIStore(t)
			s := NewServer(st, config.Default())
			var options source.ListOptions
			s.sourceFunc = func(ctx context.Context, cfg config.Config, importerOptions importer.Options) (source.Source, error) {
				return fakeAPISource{docs: map[string]*domain.Document{}, listOptions: &options}, nil
			}
			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/tree"+tt.query, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
			}
			if options.Limit != tt.wantLimit {
				t.Fatalf("unexpected list options: %#v", options)
			}
		})
	}
}

func TestPreviewNilDocumentReturnsBadGateway(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return fakeAPISource{docs: map[string]*domain.Document{}}, nil
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/documents/page-1/preview", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected preview status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "source returned nil document") {
		t.Fatalf("expected nil document error, got %s", rec.Body.String())
	}
}

func TestPreviewContentLengthIncludesImportedTitleAndTags(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	src := fakeAPISource{docs: map[string]*domain.Document{
		"page-1": {
			Ref:       domain.DocumentRef{Source: "notion", ID: "page-1", Title: "Long imported title", UpdatedAt: time.Now()},
			Content:   "x",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Tags:      []string{"tag one"},
		},
	}}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return src, nil
	}
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4}, nil
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/documents/page-1/preview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected preview status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Markdown           string `json:"markdown"`
		ContentLength      int64  `json:"content_length"`
		ContentLengthLimit int64  `json:"content_length_limit"`
		OverLimit          bool   `json:"over_limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Markdown, "# Long imported title") || !strings.Contains(body.Markdown, "#tag_one") {
		t.Fatalf("preview markdown should match imported content, got %q", body.Markdown)
	}
	if body.ContentLength <= int64(len("x")) || body.ContentLengthLimit != 4 || !body.OverLimit {
		t.Fatalf("preview did not check final imported content length: %#v", body)
	}
}
