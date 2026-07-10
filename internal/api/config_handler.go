package api

import (
	"net/http"

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
	"memos-importer/internal/source/notion"
)

type configPayload struct {
	MemosEndpoint    string `json:"memos_endpoint"`
	MemosToken       string `json:"memos_token"`
	NotionToken      string `json:"notion_token"`
	NotionTimeSource string `json:"notion_time_source"`
	WorkerCount      int    `json:"worker_count"`
}

type configEnvelope struct {
	Config configPayload `json:"config"`
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, publicConfig(s.cfg))
}

func publicConfig(cfg config.Config) map[string]interface{} {
	return map[string]interface{}{
		"memos_endpoint":     cfg.MemosEndpoint,
		"memos_token":        config.MaskSecret(cfg.MemosToken),
		"notion_token":       config.MaskSecret(cfg.NotionToken),
		"notion_time_source": cfg.NotionTimeSource,
		"worker_count":       cfg.WorkerCount,
	}
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	var payload configPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.configFromPayload(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, publicConfig(cfg))
}

func (s *Server) verifyConfig(w http.ResponseWriter, r *http.Request) {
	var payload configPayload
	if err := decodeOptionalJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.configFromPayload(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	memosClient, err := s.memosFunc(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile, memosErr := memosClient.VerifyMinVersion(r.Context(), "0.29.1")
	limit, limitErr := memosClient.ContentLengthLimit(r.Context())
	notionAdapter, notionCreateErr := s.sourceFunc(r.Context(), cfg, importer.Options{TimeSource: cfg.NotionTimeSource})
	var notionErr error
	if notionCreateErr != nil {
		notionErr = notionCreateErr
	} else {
		notionErr = notionAdapter.Verify(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memos": map[string]interface{}{
			"ok":                   memosErr == nil,
			"profile":              profile,
			"content_length_limit": limit,
			"settings_ok":          limitErr == nil,
			"error":                errorString(memosErr),
			"settings_error":       errorString(limitErr),
		},
		"notion": map[string]interface{}{
			"ok":    notionErr == nil,
			"error": errorString(notionErr),
		},
	})
}

func (s *Server) configFromPayload(payload configPayload) (config.Config, error) {
	cfg := s.cfg
	if payload.MemosEndpoint != "" {
		cfg.MemosEndpoint = payload.MemosEndpoint
	}
	if payload.MemosToken != "" {
		cfg.MemosToken = payload.MemosToken
	}
	if payload.NotionToken != "" {
		cfg.NotionToken = payload.NotionToken
	}
	if payload.NotionTimeSource != "" {
		timeSource, err := notion.NormalizeTimeSource(payload.NotionTimeSource)
		if err != nil {
			return cfg, err
		}
		cfg.NotionTimeSource = timeSource
	}
	if payload.WorkerCount > 0 {
		cfg.WorkerCount = payload.WorkerCount
	}
	return cfg, nil
}
