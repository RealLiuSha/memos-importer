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
