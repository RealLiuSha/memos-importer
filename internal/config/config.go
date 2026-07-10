package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MemosEndpoint    string        `json:"memos_endpoint"`
	MemosToken       string        `json:"memos_token,omitempty"`
	NotionToken      string        `json:"notion_token,omitempty"`
	ListenAddr       string        `json:"listen_addr"`
	AccessPassword   string        `json:"access_password,omitempty"`
	AllowNoPassword  bool          `json:"-"`
	DatabasePath     string        `json:"database_path"`
	NotionTimeSource string        `json:"notion_time_source"`
	RequestTimeout   time.Duration `json:"-"`
	WorkerCount      int           `json:"worker_count"`
}

func Default() Config {
	return Config{
		ListenAddr:       getenv("MEMOS_IMPORTER_LISTEN_ADDR", "127.0.0.1:8080"),
		DatabasePath:     getenv("MEMOS_IMPORTER_DB", "memos-importer.db"),
		MemosEndpoint:    strings.TrimRight(os.Getenv("MEMOS_IMPORTER_MEMOS_ENDPOINT"), "/"),
		MemosToken:       os.Getenv("MEMOS_IMPORTER_MEMOS_TOKEN"),
		NotionToken:      os.Getenv("MEMOS_IMPORTER_NOTION_TOKEN"),
		AccessPassword:   os.Getenv("MEMOS_IMPORTER_ACCESS_PASSWORD"),
		AllowNoPassword:  boolEnv("MEMOS_IMPORTER_ALLOW_NO_PASSWORD", false),
		NotionTimeSource: getenv("MEMOS_IMPORTER_NOTION_TIME_SOURCE", "created_time"),
		RequestTimeout:   durationEnv("MEMOS_IMPORTER_REQUEST_TIMEOUT", 30*time.Second),
		WorkerCount:      intEnv("MEMOS_IMPORTER_WORKERS", 4),
	}
}

func (c Config) ValidateServerSecurity() error {
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	nonLoopback := host != "localhost" && (ip == nil || !ip.IsLoopback())
	if nonLoopback && c.AccessPassword == "" && !c.AllowNoPassword {
		return fmt.Errorf("access password is required when listening on non-loopback address %q "+
			"(set MEMOS_IMPORTER_ACCESS_PASSWORD, or MEMOS_IMPORTER_ALLOW_NO_PASSWORD=1 to run fully open)", c.ListenAddr)
	}
	return nil
}

// RunsOpen reports whether the server will listen on a non-loopback address with no access
// password, i.e. the whole API is reachable without authentication.
func (c Config) RunsOpen() bool {
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	nonLoopback := host != "localhost" && (ip == nil || !ip.IsLoopback())
	return nonLoopback && c.AccessPassword == ""
}

func (c Config) NormalizedMemosEndpoint() (string, error) {
	if c.MemosEndpoint == "" {
		return "", fmt.Errorf("memos endpoint is required")
	}
	u, err := url.Parse(c.MemosEndpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid memos endpoint")
	}
	return strings.TrimRight(c.MemosEndpoint, "/"), nil
}

func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "********"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return b
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}

func intEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	i, err := strconv.Atoi(value)
	if err != nil || i <= 0 {
		return fallback
	}
	return i
}
