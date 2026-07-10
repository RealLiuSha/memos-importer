package main

import (
	"net/http"
	"testing"

	"memos-importer/internal/config"
)

func TestNewHTTPServerDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	srv := newHTTPServer(cfg, http.NewServeMux())
	if srv.Addr != "127.0.0.1:0" {
		t.Fatalf("unexpected addr: %s", srv.Addr)
	}
	if srv.ReadHeaderTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("timeouts should be configured: %#v", srv)
	}
}

func TestValidateServerSecurityRequiresPasswordForNonLoopback(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "0.0.0.0:8080"
	cfg.AccessPassword = ""
	if err := cfg.ValidateServerSecurity(); err == nil {
		t.Fatal("expected non-loopback listener without password to be rejected")
	}
	cfg.AccessPassword = "pw"
	if err := cfg.ValidateServerSecurity(); err != nil {
		t.Fatalf("expected password-protected non-loopback listener to be allowed: %v", err)
	}
}
