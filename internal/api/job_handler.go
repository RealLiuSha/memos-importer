package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
	"memos-importer/internal/source/notion"
	"memos-importer/internal/store"
)

type createJobRequest struct {
	Source      string           `json:"source"`
	ExternalIDs []string         `json:"external_ids"`
	Options     importer.Options `json:"options"`
	Config      configPayload    `json:"config"`
}

type jobActionRequest struct {
	Config configPayload `json:"config"`
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Source == "" {
		req.Source = "notion"
	}
	if req.Source != "notion" {
		writeError(w, http.StatusBadRequest, errString("unsupported source"))
		return
	}
	if !hasAnyExternalID(req.ExternalIDs) {
		writeError(w, http.StatusBadRequest, errString("external_ids is required"))
		return
	}
	if err := validateNotionOptions(&req.Options); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.configFromPayload(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Options.WorkerCount <= 0 {
		req.Options.WorkerCount = cfg.WorkerCount
	}
	if req.Options.TimeSource == "" {
		if _, err := notion.NormalizeTimeSource(cfg.NotionTimeSource); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	client, err := s.newMemosClient(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := client.VerifyMinVersion(r.Context(), "0.29.1"); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := ensureContentLengthLimit(r.Context(), client, &req.Options); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	src, err := s.newSource(r.Context(), cfg, req.Options)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	externalIDs := req.ExternalIDs
	if expander, ok := src.(interface {
		ExpandDocumentIDs(context.Context, []string) ([]string, error)
	}); ok {
		expanded, err := expander.ExpandDocumentIDs(r.Context(), req.ExternalIDs)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		externalIDs = expanded
	}
	engine := importer.NewEngine(src, client, s.store, s.broker, req.Options)
	jobID, err := engine.CreateJob(r.Context(), externalIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.runAsyncFunc(jobID, func(ctx context.Context) error {
		return engine.RunJob(ctx, jobID)
	}) {
		writeError(w, http.StatusConflict, errString("job is already running"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobsWithSummary(r.Context(), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": jobs})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	job, err := s.store.GetJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	items, err := s.store.ListItems(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"job":     job,
		"items":   items,
		"summary": store.SummarizeItems(items),
	})
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	ok := s.cancelRunner(jobID)
	if ok {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "canceling"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "not_running"})
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	cfg, err := s.configFromJobActionRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	engine, err := s.engineForStoredJob(r.Context(), jobID, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.runAsyncFunc(jobID, func(ctx context.Context) error {
		return engine.RetryFailed(ctx, jobID)
	}) {
		writeError(w, http.StatusConflict, errString("job is already running"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

func (s *Server) resumeJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	cfg, err := s.configFromJobActionRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	engine, err := s.engineForStoredJob(r.Context(), jobID, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.runAsyncFunc(jobID, func(ctx context.Context) error {
		return engine.RunJob(ctx, jobID)
	}) {
		writeError(w, http.StatusConflict, errString("job is already running"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

func (s *Server) engineForStoredJob(ctx context.Context, jobID string, cfg config.Config) (*importer.Engine, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	var options importer.Options
	if err := json.Unmarshal([]byte(job.Options), &options); err != nil {
		return nil, err
	}
	if err := validateNotionOptions(&options); err != nil {
		return nil, err
	}
	client, err := s.newMemosClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := client.VerifyMinVersion(ctx, "0.29.1"); err != nil {
		return nil, err
	}
	if err := ensureContentLengthLimit(ctx, client, &options); err != nil {
		return nil, err
	}
	src, err := s.newSource(ctx, cfg, options)
	if err != nil {
		return nil, err
	}
	return importer.NewEngine(src, client, s.store, s.broker, options), nil
}

func (s *Server) configFromJobActionRequest(r *http.Request) (config.Config, error) {
	var req jobActionRequest
	if r.Body == nil {
		return s.cfg, nil
	}
	if err := decodeOptionalJSON(r, &req); err != nil {
		return s.cfg, err
	}
	return s.configFromPayload(req.Config)
}

func ensureContentLengthLimit(ctx context.Context, client memosRuntime, options *importer.Options) error {
	if options.ContentLengthLimit > 0 {
		return nil
	}
	limit, err := client.ContentLengthLimit(ctx)
	if err != nil {
		return err
	}
	options.ContentLengthLimit = limit
	return nil
}

func hasAnyExternalID(externalIDs []string) bool {
	for _, id := range externalIDs {
		if strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

func validateNotionOptions(options *importer.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if options.TimeSource == "" {
		return nil
	}
	timeSource, err := notion.NormalizeTimeSource(options.TimeSource)
	if err != nil {
		return err
	}
	options.TimeSource = timeSource
	return nil
}
