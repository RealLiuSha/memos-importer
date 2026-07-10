package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"memos-importer/internal/config"
)

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
