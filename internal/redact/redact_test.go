package redact

import (
	"strings"
	"testing"
)

func TestTextRedactsURLsAndSecrets(t *testing.T) {
	input := `Authorization: Bearer abc.def token=secret password:"pw" url=https://user:pass@notion.example/file.png?X-Amz-Signature=secret.`
	got := Text(input)
	for _, forbidden := range []string{"abc.def", "token=secret", `password:"pw"`, "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redaction leaked %q in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "Bearer <redacted>") || !strings.Contains(got, "redacted") {
		t.Fatalf("expected redaction markers, got %q", got)
	}
}
