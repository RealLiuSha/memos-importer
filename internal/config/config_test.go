package config

import "testing"

func TestValidateServerSecurityRequiresPasswordForNonLoopback(t *testing.T) {
	cfg := Default()
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

func TestAllowNoPasswordPermitsOpenNonLoopback(t *testing.T) {
	cfg := Default()
	cfg.ListenAddr = "0.0.0.0:8080"
	cfg.AccessPassword = ""
	cfg.AllowNoPassword = true
	if err := cfg.ValidateServerSecurity(); err != nil {
		t.Fatalf("expected opt-in no-password listener to be allowed: %v", err)
	}
	if !cfg.RunsOpen() {
		t.Fatal("expected RunsOpen to report an open non-loopback listener")
	}
}

func TestListenAddressSecurityClassification(t *testing.T) {
	tests := []struct {
		name         string
		listenAddr   string
		password     string
		allowOpen    bool
		wantError    bool
		wantRunsOpen bool
	}{
		{name: "IPv4 loopback", listenAddr: "127.0.0.1:8080"},
		{name: "localhost", listenAddr: "localhost:8080"},
		{name: "IPv6 loopback", listenAddr: "[::1]:8080"},
		{name: "non-loopback blocked", listenAddr: "0.0.0.0:8080", wantError: true, wantRunsOpen: true},
		{name: "non-loopback protected", listenAddr: "0.0.0.0:8080", password: "pw"},
		{name: "non-loopback explicitly open", listenAddr: "0.0.0.0:8080", allowOpen: true, wantRunsOpen: true},
		{name: "invalid address", listenAddr: "not-an-address", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.ListenAddr = tt.listenAddr
			cfg.AccessPassword = tt.password
			cfg.AllowNoPassword = tt.allowOpen

			err := cfg.ValidateServerSecurity()
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateServerSecurity() error = %v, wantError %v", err, tt.wantError)
			}
			if got := cfg.RunsOpen(); got != tt.wantRunsOpen {
				t.Fatalf("RunsOpen() = %v, want %v", got, tt.wantRunsOpen)
			}
		})
	}
}
