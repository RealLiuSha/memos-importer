package memos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"memos-importer/internal/redact"
)

type TraceEvent struct {
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	StatusCode int         `json:"status_code,omitempty"`
	DurationMS int64       `json:"duration_ms"`
	Request    interface{} `json:"request,omitempty"`
	Response   interface{} `json:"response,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type TraceFunc func(TraceEvent)

func WithTrace(trace TraceFunc) Option {
	return func(c *Client) {
		c.trace = trace
	}
}

func (c *Client) emitTrace(method, apiPath string, statusCode int, started time.Time, requestBody, responseBody []byte, err error) {
	if c.trace == nil {
		return
	}
	event := TraceEvent{
		Method:     method,
		Path:       apiPath,
		StatusCode: statusCode,
		DurationMS: time.Since(started).Milliseconds(),
		Request:    sanitizeJSONBody(requestBody),
		Response:   sanitizeJSONBody(responseBody),
	}
	if err != nil {
		event.Error = sanitizeBody([]byte(err.Error()))
	}
	c.trace(event)
}

func sanitizeJSONBody(body []byte) interface{} {
	if len(body) == 0 {
		return nil
	}
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		return summarizeString(string(body))
	}
	return sanitizeTraceValue("", value)
}

func sanitizeTraceValue(key string, value interface{}) interface{} {
	lowerKey := strings.ToLower(key)
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for childKey, childValue := range v {
			out[childKey] = sanitizeTraceValue(childKey, childValue)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = sanitizeTraceValue(key, item)
		}
		return out
	case string:
		if isSensitiveKey(lowerKey) {
			return summarizeString(v)
		}
		return redactURLString(v)
	default:
		return value
	}
}

func isSensitiveKey(lowerKey string) bool {
	if lowerKey == "content" || lowerKey == "token" || lowerKey == "access_token" {
		return true
	}
	return strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "authorization")
}

func summarizeString(value string) map[string]interface{} {
	sum := sha256.Sum256([]byte(value))
	return map[string]interface{}{
		"redacted": true,
		"bytes":    len([]byte(value)),
		"sha256":   hex.EncodeToString(sum[:8]),
	}
}

func redactURLString(value string) string {
	return redact.Text(value)
}
