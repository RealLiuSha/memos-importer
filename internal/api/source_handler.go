package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
	"memos-importer/internal/source"
)

const (
	defaultNotionDocumentLimit = 100
	maxNotionDocumentLimit     = 1000
)

func (s *Server) notionTree(w http.ResponseWriter, r *http.Request) {
	limit, err := notionDocumentLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.configFromEnvelopeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	src, err := s.sourceFunc(r.Context(), cfg, importer.Options{})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	list, err := src.ListDocuments(r.Context(), source.ListOptions{Limit: limit})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"documents": list.Documents,
		"has_more":  list.HasMore,
	})
}

func notionDocumentLimit(r *http.Request) (int, error) {
	values := r.URL.Query()
	if !values.Has("limit") {
		return defaultNotionDocumentLimit, nil
	}
	value := strings.TrimSpace(values.Get("limit"))
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > maxNotionDocumentLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxNotionDocumentLimit)
	}
	return limit, nil
}

func (s *Server) previewNotionDocument(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.configFromEnvelopeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	src, err := s.sourceFunc(r.Context(), cfg, importer.Options{})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	doc, err := src.FetchDocument(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if doc == nil {
		writeError(w, http.StatusBadGateway, errString("source returned nil document"))
		return
	}
	content := importer.ComposeContent(doc)
	limit := int64(0)
	if client, err := s.memosFunc(r.Context(), cfg); err == nil {
		limit, _ = client.ContentLengthLimit(r.Context())
	}
	contentLength := int64(len([]byte(content)))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"document":             doc.Ref,
		"markdown":             content,
		"warnings":             doc.Warnings,
		"attachment_count":     len(doc.Attachments),
		"content_length":       contentLength,
		"content_length_limit": limit,
		"over_limit":           limit > 0 && contentLength > limit,
	})
}

func (s *Server) configFromEnvelopeRequest(r *http.Request) (config.Config, error) {
	if r.Method == http.MethodGet || r.Body == nil {
		return s.cfg, nil
	}
	var req configEnvelope
	if err := decodeOptionalJSON(r, &req); err != nil {
		return s.cfg, err
	}
	return s.configFromPayload(req.Config)
}
