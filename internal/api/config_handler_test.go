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

	"memos-importer/internal/config"
	"memos-importer/internal/importer"
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
