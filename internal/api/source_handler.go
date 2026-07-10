package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
)

func (s *Server) notionTree(w http.ResponseWriter, r *http.Request) {
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
	refs, err := src.ListDocuments(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"documents": refs})
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
