package redact

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	urlPattern           = regexp.MustCompile(`https?://[^\s"'<>]+`)
	authorizationPattern = regexp.MustCompile(`(?i)(["']?authorization["']?\s*[:=]\s*)bearer\s+[A-Za-z0-9._~+/=-]+`)
	bearerPattern        = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	sensitiveKVPattern   = regexp.MustCompile(`(?i)(["']?(?:token|access_token|refresh_token|secret|password|x-amz-signature|x-amz-credential|x-amz-security-token|signature)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^,\s}\]]+)`)
)

func Text(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = redactURLs(text)
	text = authorizationPattern.ReplaceAllString(text, "${1}Bearer <redacted>")
	text = bearerPattern.ReplaceAllString(text, "Bearer <redacted>")
	text = sensitiveKVPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := sensitiveKVPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return "<redacted>"
		}
		return parts[1] + "<redacted>"
	})
	return text
}

func Short(text string, limit int) string {
	text = Text(text)
	if limit > 0 && len(text) > limit {
		return text[:limit]
	}
	return text
}

func redactURLs(text string) string {
	return urlPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		trimmed := strings.TrimRight(candidate, `.,);]}`)
		suffix := strings.TrimPrefix(candidate, trimmed)
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return candidate
		}
		if u.User != nil {
			u.User = url.User("<redacted>")
		}
		if u.RawQuery != "" {
			u.RawQuery = "redacted"
		}
		return u.String() + suffix
	})
}
