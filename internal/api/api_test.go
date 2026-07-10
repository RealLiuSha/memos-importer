package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memos-importer/internal/config"
	"memos-importer/internal/domain"
	"memos-importer/internal/importer"
	"memos-importer/internal/memos"
	"memos-importer/internal/source"
	"memos-importer/internal/store"
)

func TestSaveConfigMasksButDoesNotPersistSecrets(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := NewServer(st, config.Default())
	body := bytes.NewBufferString(`{"memos_endpoint":"http://memos.local","memos_token":"abcdefghijklmnopqrstuvwxyz","notion_token":"secretsecret"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("token leaked: %s", rec.Body.String())
	}
	if resp["memos_token"] == "" {
		t.Fatalf("masked token missing: %#v", resp)
	}
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if strings.Contains(rec.Body.String(), "abcdefghijklmnopqrstuvwxyz") || strings.Contains(rec.Body.String(), "secretsecret") {
		t.Fatalf("GET config leaked browser-provided token: %s", rec.Body.String())
	}
}

func TestSaveConfigRejectsInvalidTimeSource(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"notion_time_source":"updated"}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid notion time_source") {
		t.Fatalf("expected time source validation error, got %s", rec.Body.String())
	}
}

func TestErrorResponseRedactsSecretsAndTemporaryURLs(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadGateway, errString(`Authorization: Bearer raw-token token=secret temporary https://user:pass@notion.example/file.png?X-Amz-Signature=secret`))
	body := rec.Body.String()
	for _, forbidden := range []string{"raw-token", "token=secret", "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("error response leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "redacted") {
		t.Fatalf("expected redaction marker: %s", body)
	}
}

func TestVerifyConfigRejectsOldMemosVersion(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.0", limit: 1024}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return fakeAPISource{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config/verify", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "lower than required") {
		t.Fatalf("old version was not rejected: %s", rec.Body.String())
	}
}

func TestVerifyConfigRejectsMalformedJSONBeforeExternalChecks(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	called := false
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		called = true
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config/verify", bytes.NewBufferString(`{"memos_endpoint":`))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("malformed JSON should not call external verification")
	}
}

func TestRequestConfigIsPassedToExternalClients(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	var gotMemosEndpoint string
	var gotMemosToken string
	var gotNotionToken string
	var gotTimeSource string
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		gotMemosEndpoint = cfg.MemosEndpoint
		gotMemosToken = cfg.MemosToken
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		gotNotionToken = cfg.NotionToken
		gotTimeSource = options.TimeSource
		return fakeAPISource{}, nil
	}
	body := bytes.NewBufferString(`{"memos_endpoint":"http://memos.local","memos_token":"memos-token","notion_token":"notion-token","notion_time_source":"last_edited_time","worker_count":3}`)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/verify", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if gotMemosEndpoint != "http://memos.local" || gotMemosToken != "memos-token" || gotNotionToken != "notion-token" || gotTimeSource != "last_edited_time" {
		t.Fatalf("request config was not passed through: endpoint=%q memos=%q notion=%q time=%q", gotMemosEndpoint, gotMemosToken, gotNotionToken, gotTimeSource)
	}
}

func TestCreateJobRejectsOldMemosVersionBeforeSource(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.0", limit: 4096}, nil
	}
	sourceCalled := false
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		sourceCalled = true
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		t.Fatal("runner should not start for unsupported memos version")
		return false
	}
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"worker_count":1}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected create status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "lower than required") {
		t.Fatalf("expected version gate error, got %s", rec.Body.String())
	}
	if sourceCalled {
		t.Fatal("source should not be created after unsupported memos version")
	}
	jobs, err := st.ListJobs(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("unsupported memos version should not create jobs: %#v", jobs)
	}
}

func TestCreateJobRejectsContentLengthLimitErrorBeforeSource(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limitErr: errString("settings unavailable")}, nil
	}
	sourceCalled := false
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		sourceCalled = true
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		t.Fatal("runner should not start without content_length_limit")
		return false
	}
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"worker_count":1}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected create status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "settings unavailable") {
		t.Fatalf("expected settings error, got %s", rec.Body.String())
	}
	if sourceCalled {
		t.Fatal("source should not be created after content_length_limit failure")
	}
}

func TestJobCreateCancelRetryAndSSE(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	runtime := &fakeRuntime{version: "0.29.1", limit: 4096}
	src := fakeAPISource{docs: map[string]*domain.Document{
		"page-1": apiDoc("page-1", "hello"),
	}}
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) { return runtime, nil }
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return src, nil
	}

	runStarted := make(chan string, 1)
	releaseRun := make(chan struct{})
	runDone := make(chan struct{})
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.runners[jobID] = cancel
		s.mu.Unlock()
		go func() {
			defer close(runDone)
			runStarted <- jobID
			<-releaseRun
			_ = run(ctx)
			s.mu.Lock()
			delete(s.runners, jobID)
			s.mu.Unlock()
		}()
		return true
	}

	router := s.Router()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"worker_count":1}}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected create status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	jobID := created["job_id"]
	if jobID == "" {
		t.Fatalf("job id missing: %s", rec.Body.String())
	}
	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	events, cancel := s.broker.Subscribe(jobID)
	defer cancel()
	s.broker.Publish(importer.Event{JobID: jobID, Type: "probe"})
	select {
	case event := <-events:
		if event.Type != "probe" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected SSE broker event")
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/"+jobID+"/cancel", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected cancel status %d: %s", rec.Code, rec.Body.String())
	}
	close(releaseRun)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	if err := st.UpdateItem(context.Background(), store.ImportItem{JobID: jobID, ExternalID: "page-1", Title: "page-1", Status: "failed", Warnings: "[]", ErrorStage: "fetch", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		_ = run(context.Background())
		return true
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/"+jobID+"/retry", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected retry status %d: %s", rec.Code, rec.Body.String())
	}
	items, err := st.ListItems(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "imported" {
		t.Fatalf("retry did not import failed item: %#v", items[0])
	}
}

func TestCreateJobExpandsDatabaseSelection(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	src := fakeExpandingSource{
		fakeAPISource: fakeAPISource{docs: map[string]*domain.Document{
			"page-1":    apiDoc("page-1", "selected page"),
			"db-page-1": apiDoc("db-page-1", "from database 1"),
			"db-page-2": apiDoc("db-page-2", "from database 2"),
		}},
		expanded: map[string][]string{"db-1": []string{"page-1", "db-page-1", "db-page-2"}},
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return src, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool { return true }

	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1","db-1"],"options":{"worker_count":1}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListItems(context.Background(), created["job_id"])
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 deduplicated expanded page items, got %#v", items)
	}
	got := []string{items[0].ExternalID, items[1].ExternalID, items[2].ExternalID}
	if strings.Join(got, ",") != "db-page-1,db-page-2,page-1" {
		t.Fatalf("database id was not expanded into pages: %#v", items)
	}
}

func TestCreateJobRejectsMissingExternalIDsBeforeExternalConfig(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":[" ",""],"options":{}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "external_ids is required") {
		t.Fatalf("expected external id validation error, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "memos endpoint") {
		t.Fatalf("missing selection should not be reported as config failure: %s", rec.Body.String())
	}
}

func TestCreateJobRejectsInvalidOptionsBeforeExternalConfig(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"visibility":"TEAM"}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid visibility") {
		t.Fatalf("expected options validation error, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "memos endpoint") {
		t.Fatalf("invalid options should not be reported as config failure: %s", rec.Body.String())
	}
}

func TestCreateJobRejectsInvalidTimeSourceBeforeExternalConfig(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"time_source":"updated"}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid notion time_source") {
		t.Fatalf("expected time source validation error, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "memos endpoint") {
		t.Fatalf("invalid time source should not be reported as config failure: %s", rec.Body.String())
	}
}

func TestCreateJobUsesConfiguredWorkerDefault(t *testing.T) {
	st := openAPIStore(t)
	cfg := config.Default()
	cfg.WorkerCount = 7
	s := NewServer(st, cfg)
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool { return true }
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	job, err := st.GetJob(context.Background(), created["job_id"])
	if err != nil {
		t.Fatal(err)
	}
	var opts importer.Options
	if err := json.Unmarshal([]byte(job.Options), &opts); err != nil {
		t.Fatal(err)
	}
	if opts.WorkerCount != 7 {
		t.Fatalf("expected configured worker default, got options %#v", opts)
	}
}

func TestCreateJobNormalizesPropertyTimeSource(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		if options.TimeSource != "property:Published At" {
			t.Fatalf("time source was not normalized before source creation: %#v", options)
		}
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool { return true }
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"notion","external_ids":["page-1"],"options":{"time_source":" property:Published At "}}`)
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStartRunnerRejectsDuplicateJob(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	started := make(chan struct{})
	done := make(chan struct{})
	if !s.startRunner("job-1", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(done)
		return ctx.Err()
	}) {
		t.Fatal("first runner should start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first runner did not start")
	}
	duplicateRan := make(chan struct{}, 1)
	if s.startRunner("job-1", func(ctx context.Context) error {
		duplicateRan <- struct{}{}
		return nil
	}) {
		t.Fatal("duplicate runner should be rejected")
	}
	select {
	case <-duplicateRan:
		t.Fatal("duplicate runner executed")
	default:
	}
	if !s.cancelRunner("job-1") {
		t.Fatal("running job should be cancelable")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after cancel")
	}
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		running := s.runners["job-1"] != nil
		s.mu.Unlock()
		if !running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("runner was not removed")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestJobEventsUnsubscribesOnDisconnect(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	server := httptest.NewServer(s.Router())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/jobs/job-sse/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	waitBrokerSubscribers(t, s, "job-sse", 1)
	cancel()
	_ = resp.Body.Close()
	waitBrokerSubscribers(t, s, "job-sse", 0)
}

func TestResumeJobRunsPendingItems(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		_ = run(context.Background())
		return true
	}
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-resume", Source: "notion", Status: "canceled", Options: `{"worker_count":1}`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "pending", Warnings: "[]"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-resume/resume", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected resume status %d: %s", rec.Code, rec.Body.String())
	}
	items, err := st.ListItems(context.Background(), "job-resume")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "imported" {
		t.Fatalf("resume did not import pending item: %#v", items[0])
	}
	job, err := st.GetJob(context.Background(), "job-resume")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "done" {
		t.Fatalf("resume did not complete job: %#v", job)
	}
}

func TestResumeJobAppliesContentLengthLimitWhenStoredOptionsMissingIt(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4}, nil
	}
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		if options.ContentLengthLimit != 4 {
			t.Fatalf("stored job did not refresh content length limit: %#v", options)
		}
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "content over limit")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		_ = run(context.Background())
		return true
	}
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-resume-limit", Source: "notion", Status: "canceled", Options: `{"worker_count":1}`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "pending", Warnings: "[]"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-resume-limit/resume", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected resume status %d: %s", rec.Code, rec.Body.String())
	}
	items, err := st.ListItems(context.Background(), "job-resume-limit")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" || items[0].ErrorStage != "content_length" {
		t.Fatalf("resume should enforce refreshed content length limit: %#v", items)
	}
}

func TestResumeJobClearsPreviousFinishedAtWhileRunning(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.1", limit: 4096}, nil
	}
	fetchStarted := make(chan struct{})
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		return fakeAPISource{
			docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")},
			beforeFetch: func(ctx context.Context, id string) {
				close(fetchStarted)
				<-ctx.Done()
			},
		}, nil
	}
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-resume-running", Source: "notion", Status: "pending", Options: `{"worker_count":1}`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "pending", Warnings: "[]"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobStatus(context.Background(), "job-resume-running", "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJobStatus(context.Background(), "job-resume-running", "failed", "previous failure"); err != nil {
		t.Fatal(err)
	}
	failedJob, err := st.GetJob(context.Background(), "job-resume-running")
	if err != nil {
		t.Fatal(err)
	}
	if failedJob.FinishedAt == nil || failedJob.Error == "" {
		t.Fatalf("test setup should create failed finish metadata: %#v", failedJob)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-resume-running/resume", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unexpected resume status %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("resume did not start fetching")
	}
	runningJob, err := st.GetJob(context.Background(), "job-resume-running")
	if err != nil {
		t.Fatal(err)
	}
	if runningJob.Status != "running" || runningJob.FinishedAt != nil || runningJob.Error != "" {
		t.Fatalf("running resumed job should clear previous finish metadata: %#v", runningJob)
	}
	if !s.cancelRunner("job-resume-running") {
		t.Fatal("expected running resume to be cancelable")
	}
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		running := s.runners["job-resume-running"] != nil
		s.mu.Unlock()
		if !running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("runner was not removed")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestResumeJobRejectsCorruptStoredOptionsBeforeExternalConfig(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-corrupt-options", Source: "notion", Status: "canceled", Options: `{"strategy":`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "pending", Warnings: "[]"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-corrupt-options/resume", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected resume status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unexpected end of JSON input") {
		t.Fatalf("expected stored options JSON error, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "memos endpoint") {
		t.Fatalf("stored job validation should happen before external config checks: %s", rec.Body.String())
	}
}

func TestResumeJobRejectsOldMemosVersionBeforeSource(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	s.memosFunc = func(ctx context.Context, cfg config.Config) (memosRuntime, error) {
		return &fakeRuntime{version: "0.29.0", limit: 4096}, nil
	}
	sourceCalled := false
	s.sourceFunc = func(ctx context.Context, cfg config.Config, options importer.Options) (source.Source, error) {
		sourceCalled = true
		return fakeAPISource{docs: map[string]*domain.Document{"page-1": apiDoc("page-1", "hello")}}, nil
	}
	s.runAsyncFunc = func(jobID string, run func(context.Context) error) bool {
		t.Fatal("runner should not start for unsupported memos version")
		return false
	}
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-old-version", Source: "notion", Status: "canceled", Options: `{"worker_count":1}`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "pending", Warnings: "[]"}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-old-version/resume", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected resume status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "lower than required") {
		t.Fatalf("expected version gate error, got %s", rec.Body.String())
	}
	if sourceCalled {
		t.Fatal("source should not be created after unsupported memos version")
	}
	items, err := st.ListItems(context.Background(), "job-old-version")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "pending" {
		t.Fatalf("resume should leave stored items untouched after version rejection: %#v", items)
	}
}

func TestJobDetailReturnsItemWarnings(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	warnings := `[{"code":"unsupported_block","message":"unsupported Notion block type: synced_block"}]`
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-warnings", Source: "notion", Status: "done", Options: `{}`,
	}, []store.ImportItem{{ExternalID: "page-1", Title: "page-1", Status: "imported", Warnings: warnings}}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs/job-warnings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported_block") {
		t.Fatalf("job detail did not include warnings: %s", rec.Body.String())
	}
}

func TestJobListAndDetailExposeSummary(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-summary", Source: "notion", Status: "failed", Options: `{}`,
	}, []store.ImportItem{
		{ExternalID: "page-1", Title: "page-1", Status: "imported", Warnings: "[]"},
		{ExternalID: "page-2", Title: "page-2", Status: "failed", Warnings: "[]"},
		{ExternalID: "page-3", Title: "page-3", Status: "pending", Warnings: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status %d: %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Jobs []struct {
			ID      string           `json:"id"`
			Summary store.JobSummary `json:"summary"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Jobs) != 1 || listResp.Jobs[0].ID != "job-summary" {
		t.Fatalf("unexpected jobs response: %#v", listResp)
	}
	if listResp.Jobs[0].Summary.Total != 3 || listResp.Jobs[0].Summary.Completed != 2 || listResp.Jobs[0].Summary.ProgressPercent != 66 {
		t.Fatalf("unexpected list summary: %#v", listResp.Jobs[0].Summary)
	}

	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs/job-summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected detail status %d: %s", rec.Code, rec.Body.String())
	}
	var detailResp struct {
		Summary store.JobSummary `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detailResp); err != nil {
		t.Fatal(err)
	}
	if detailResp.Summary.Total != 3 || detailResp.Summary.Failed != 1 || detailResp.Summary.Pending != 1 {
		t.Fatalf("unexpected detail summary: %#v", detailResp.Summary)
	}
}

func TestNotionTreeAndPreviewExposeWarnings(t *testing.T) {
	st := openAPIStore(t)
	s := NewServer(st, config.Default())
	src := fakeAPISource{docs: map[string]*domain.Document{
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
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sources/notion/documents/page-1/preview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected preview status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported_block") || !strings.Contains(rec.Body.String(), "Unsupported Notion block") {
		t.Fatalf("preview did not expose warning/placeholder: %s", rec.Body.String())
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

func TestPasswordRequiredForProtectedAPI(t *testing.T) {
	st := openAPIStore(t)
	cfg := config.Default()
	cfg.AccessPassword = "pw"
	s := NewServer(st, cfg)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("X-Memos-Importer-Password", "pw")
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected authorized request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAccessPasswordSessionCookieAuthorizesAPI(t *testing.T) {
	st := openAPIStore(t)
	cfg := config.Default()
	cfg.AccessPassword = "super-secret"
	s := NewServer(st, cfg)
	router := s.Router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static console should load before API auth, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized protected API, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewBufferString(`{"password":"wrong"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected bad password rejection, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewBufferString(`{"password":"super-secret"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected session creation, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), "super-secret") {
		t.Fatalf("session cookie leaked raw password: %s", rec.Header().Get("Set-Cookie"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie missing")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cookie-authenticated request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStaticConsoleFallbackDoesNotBypassAPINotFound(t *testing.T) {
	st := openAPIStore(t)
	cfg := config.Default()
	cfg.AccessPassword = "pw"
	s := NewServer(st, cfg)
	router := s.Router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/deep/link", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static SPA route should load before API auth, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("static fallback did not return console shell: %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	req.Header.Set("X-Memos-Importer-Password", "pw")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown API route should not fall back to console, got %d: %s", rec.Code, rec.Body.String())
	}
}

func openAPIStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func waitBrokerSubscribers(t *testing.T, s *Server, jobID string, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		got := s.broker.SubscriberCount(jobID)
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected %d broker subscribers for %s", want, jobID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type fakeRuntime struct {
	version  string
	limit    int64
	limitErr error
	created  int
}

func (f *fakeRuntime) VerifyMinVersion(ctx context.Context, min string) (*memos.InstanceProfile, error) {
	if f.version < min {
		return &memos.InstanceProfile{Version: f.version}, errString("memos version " + f.version + " is lower than required " + min)
	}
	return &memos.InstanceProfile{Version: f.version}, nil
}

func (f *fakeRuntime) ContentLengthLimit(ctx context.Context) (int64, error) {
	if f.limitErr != nil {
		return 0, f.limitErr
	}
	return f.limit, nil
}

func (f *fakeRuntime) CreateAttachment(ctx context.Context, req memos.CreateAttachmentRequest) (*memos.Attachment, error) {
	return &memos.Attachment{Name: "attachments/1", UID: "uid1", Filename: req.Filename, Type: req.Type, Size: int64(len(req.Content))}, nil
}

func (f *fakeRuntime) CreateMemo(ctx context.Context, req memos.CreateMemoRequest) (*memos.Memo, error) {
	f.created++
	return &memos.Memo{Name: "memos/1", Content: req.Memo.Content}, nil
}

func (f *fakeRuntime) UpdateMemo(ctx context.Context, name string, req memos.UpdateMemoRequest) (*memos.Memo, error) {
	return &memos.Memo{Name: name, Content: req.Memo.Content}, nil
}

type fakeAPISource struct {
	docs        map[string]*domain.Document
	beforeFetch func(ctx context.Context, id string)
}

func (f fakeAPISource) Name() string                     { return "notion" }
func (f fakeAPISource) Verify(ctx context.Context) error { return nil }
func (f fakeAPISource) ListDocuments(ctx context.Context) ([]domain.DocumentRef, error) {
	refs := make([]domain.DocumentRef, 0, len(f.docs))
	for _, doc := range f.docs {
		refs = append(refs, doc.Ref)
	}
	return refs, nil
}
func (f fakeAPISource) FetchDocument(ctx context.Context, id string) (*domain.Document, error) {
	if f.beforeFetch != nil {
		f.beforeFetch(ctx, id)
	}
	return f.docs[id], nil
}

type fakeExpandingSource struct {
	fakeAPISource
	expanded map[string][]string
}

func (f fakeExpandingSource) ExpandDocumentIDs(ctx context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if expanded := f.expanded[id]; len(expanded) > 0 {
			out = append(out, expanded...)
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func apiDoc(id, content string) *domain.Document {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return &domain.Document{Ref: domain.DocumentRef{Source: "notion", ID: id, Title: id, UpdatedAt: now}, Content: content, CreatedAt: now, UpdatedAt: now}
}
