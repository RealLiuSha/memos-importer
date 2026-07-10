package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
	"memos-importer/internal/memos"
	"memos-importer/internal/source"
	"memos-importer/internal/source/notion"
	"memos-importer/internal/store"
)

type Server struct {
	store  *store.Store
	cfg    config.Config
	broker *importer.Broker

	mu           sync.Mutex
	runners      map[string]context.CancelFunc
	sourceFunc   func(context.Context, config.Config, importer.Options) (source.Source, error)
	memosFunc    func(context.Context, config.Config) (memosRuntime, error)
	runAsyncFunc func(jobID string, run func(context.Context) error) bool
}

func NewServer(st *store.Store, cfg config.Config) *Server {
	s := &Server{
		store:   st,
		cfg:     cfg,
		broker:  importer.NewBroker(),
		runners: make(map[string]context.CancelFunc),
	}
	s.sourceFunc = s.defaultSource
	s.memosFunc = s.defaultMemosClient
	s.runAsyncFunc = s.startRunner
	return s
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.passwordMiddleware)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Route("/api", func(r chi.Router) {
		r.Post("/session", s.createSession)
		r.Get("/config", s.getConfig)
		r.Post("/config", s.saveConfig)
		r.Post("/config/verify", s.verifyConfig)
		r.Get("/sources/notion/tree", s.notionTree)
		r.Post("/sources/notion/tree", s.notionTree)
		r.Get("/sources/notion/documents/{id}/preview", s.previewNotionDocument)
		r.Post("/sources/notion/documents/{id}/preview", s.previewNotionDocument)
		r.Post("/jobs", s.createJob)
		r.Get("/jobs", s.listJobs)
		r.Get("/jobs/{id}", s.getJob)
		r.Get("/jobs/{id}/events", s.jobEvents)
		r.Post("/jobs/{id}/cancel", s.cancelJob)
		r.Post("/jobs/{id}/resume", s.resumeJob)
		r.Post("/jobs/{id}/retry", s.retryJob)
	})
	r.NotFound(StaticHandler())
	return r
}

func (s *Server) passwordMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AccessPassword == "" || r.URL.Path == "/health" || !strings.HasPrefix(r.URL.Path, "/api/") || isSessionCreate(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.hasValidCredential(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, errors.New("invalid access password"))
	})
}

type memosRuntime interface {
	importer.MemosClient
	VerifyMinVersion(ctx context.Context, min string) (*memos.InstanceProfile, error)
	ContentLengthLimit(ctx context.Context) (int64, error)
}

func (s *Server) newSource(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
	return s.sourceFunc(ctx, cfg, options)
}

func (s *Server) defaultSource(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
	timeSource := options.TimeSource
	if timeSource == "" {
		timeSource = cfg.NotionTimeSource
	}
	return notion.NewAdapter(cfg.NotionToken, timeSource, notion.WithAdapterRequestTimeout(cfg.RequestTimeout))
}

func (s *Server) newMemosClient(ctx context.Context, cfg config.Config) (memosRuntime, error) {
	return s.memosFunc(ctx, cfg)
}

func (s *Server) defaultMemosClient(ctx context.Context, cfg config.Config) (memosRuntime, error) {
	endpoint, err := cfg.NormalizedMemosEndpoint()
	if err != nil {
		return nil, err
	}
	return memos.New(endpoint, cfg.MemosToken, memos.WithRequestTimeout(cfg.RequestTimeout))
}

func (s *Server) startRunner(jobID string, run func(context.Context) error) bool {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.runners[jobID] != nil {
		s.mu.Unlock()
		cancel()
		return false
	}
	s.runners[jobID] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.runners, jobID)
			s.mu.Unlock()
		}()
		_ = run(ctx)
	}()
	return true
}

func (s *Server) cancelRunner(jobID string) bool {
	s.mu.Lock()
	cancel := s.runners[jobID]
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	return ""
}

func requestTimeoutContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(r.Context())
	}
	return context.WithTimeout(r.Context(), timeout)
}
