package memos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"memos-importer/internal/redact"
)

type Client struct {
	endpoint       string
	token          string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
	trace          TraceFunc
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.requestTimeout = timeout
		}
	}
}

func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

func New(endpoint, token string, opts ...Option) (*Client, error) {
	normalizedEndpoint, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	c := &Client{
		endpoint:       normalizedEndpoint,
		token:          token,
		httpClient:     http.DefaultClient,
		requestTimeout: 30 * time.Second,
		maxRetries:     2,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func normalizeEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		return "", errors.New("memos endpoint is required")
	}
	endpoint = strings.TrimRight(endpoint, "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid memos endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "/api/v1" || strings.HasSuffix(u.Path, "/api/v1") {
		u.Path = strings.TrimSuffix(u.Path, "/api/v1")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (c *Client) InstanceProfile(ctx context.Context) (*InstanceProfile, error) {
	var profile InstanceProfile
	if err := c.do(ctx, http.MethodGet, "/api/v1/instance/profile", nil, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (c *Client) VerifyMinVersion(ctx context.Context, min string) (*InstanceProfile, error) {
	profile, err := c.InstanceProfile(ctx)
	if err != nil {
		return nil, err
	}
	if compareVersion(profile.Version, min) < 0 {
		return nil, fmt.Errorf("memos version %s is lower than required %s", profile.Version, min)
	}
	return profile, nil
}

func (c *Client) ContentLengthLimit(ctx context.Context) (int64, error) {
	var raw map[string]interface{}
	if err := c.do(ctx, http.MethodPost, "/api/v1/instance/settings:batchGet", map[string][]string{
		"names": []string{"instance/settings/MEMO_RELATED"},
	}, &raw); err == nil {
		if limit, ok := findContentLengthLimit(raw); ok {
			return limit, nil
		}
	} else {
		lastErr := err
		if limit, err := c.legacyContentLengthLimit(ctx); err == nil {
			return limit, nil
		}
		return 0, lastErr
	}
	return c.legacyContentLengthLimit(ctx)
}

func (c *Client) legacyContentLengthLimit(ctx context.Context) (int64, error) {
	paths := []string{
		"/api/v1/instance/settings/memo_related",
		"/api/v1/instance/settings",
	}
	var lastErr error
	for _, p := range paths {
		var raw map[string]interface{}
		if err := c.do(ctx, http.MethodGet, p, nil, &raw); err != nil {
			lastErr = err
			continue
		}
		if limit, ok := findContentLengthLimit(raw); ok {
			return limit, nil
		}
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, errors.New("content_length_limit not found in instance settings")
}

func (c *Client) CreateAttachment(ctx context.Context, req CreateAttachmentRequest) (*Attachment, error) {
	var attachment Attachment
	if err := c.do(ctx, http.MethodPost, "/api/v1/attachments", req, &attachment); err != nil {
		return nil, err
	}
	return &attachment, nil
}

func (c *Client) CreateMemo(ctx context.Context, req CreateMemoRequest) (*Memo, error) {
	var memo Memo
	if err := c.do(ctx, http.MethodPost, "/api/v1/memos", req, &memo); err != nil {
		return nil, err
	}
	return &memo, nil
}

func (c *Client) UpdateMemo(ctx context.Context, name string, req UpdateMemoRequest) (*Memo, error) {
	var memo Memo
	if err := c.do(ctx, http.MethodPatch, memoAPIPath(name), req, &memo); err != nil {
		return nil, err
	}
	return &memo, nil
}

func (c *Client) CheckAttachmentFile(ctx context.Context, uid, filename string) (*FileCheck, error) {
	filePath := AttachmentFilePath(uid, filename)
	check, err := c.checkFile(ctx, http.MethodHead, filePath)
	if err == nil && check.StatusCode != http.StatusMethodNotAllowed {
		return check, nil
	}
	if err != nil {
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusMethodNotAllowed {
			return check, err
		}
	}
	return c.checkFile(ctx, http.MethodGet, filePath)
}

func (c *Client) do(ctx context.Context, method, apiPath string, in interface{}, out interface{}) error {
	var body []byte
	var err error
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	attempts := c.maxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		reqCtx := ctx
		cancel := func() {}
		if c.requestTimeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		}
		err = c.doOnce(reqCtx, method, apiPath, body, out)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || !isRetryableStatus(httpErr.StatusCode) {
			return err
		}
	}
	return lastErr
}

func (c *Client) doOnce(ctx context.Context, method, apiPath string, body []byte, out interface{}) error {
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+apiPath, bytes.NewReader(body))
	if err != nil {
		c.emitTrace(method, apiPath, 0, started, body, nil, err)
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = redactError(err)
		c.emitTrace(method, apiPath, 0, started, body, nil, err)
		return err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		c.emitTrace(method, apiPath, resp.StatusCode, started, body, respBody, readErr)
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := &HTTPError{StatusCode: resp.StatusCode, Body: sanitizeBody(respBody)}
		c.emitTrace(method, apiPath, resp.StatusCode, started, body, respBody, err)
		return err
	}
	if out == nil || len(respBody) == 0 {
		c.emitTrace(method, apiPath, resp.StatusCode, started, body, respBody, nil)
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		err = fmt.Errorf("decode memos response: %w", err)
		c.emitTrace(method, apiPath, resp.StatusCode, started, body, respBody, err)
		return err
	}
	c.emitTrace(method, apiPath, resp.StatusCode, started, body, respBody, nil)
	return nil
}

func (c *Client) checkFile(ctx context.Context, method, filePath string) (*FileCheck, error) {
	started := time.Now()
	reqCtx := ctx
	cancel := func() {}
	if c.requestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, c.endpoint+filePath, nil)
	if err != nil {
		c.emitTrace(method, filePath, 0, started, nil, nil, err)
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = redactError(err)
		c.emitTrace(method, filePath, 0, started, nil, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	if method == http.MethodGet {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	}
	check := &FileCheck{
		Path:        filePath,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		OK:          resp.StatusCode >= 200 && resp.StatusCode < 300,
	}
	var traceErr error
	if !check.OK {
		traceErr = &HTTPError{StatusCode: resp.StatusCode}
	}
	c.emitTrace(method, filePath, resp.StatusCode, started, nil, nil, traceErr)
	if traceErr != nil {
		return check, traceErr
	}
	return check, nil
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("memos API returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("memos API returned status %d: %s", e.StatusCode, e.Body)
}

func memoAPIPath(name string) string {
	name = strings.TrimPrefix(name, "/")
	if strings.HasPrefix(name, "memos/") {
		return "/api/v1/" + name
	}
	return "/api/v1/memos/" + url.PathEscape(name)
}

func AttachmentFilePath(uid, filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	cleanName := path.Base(filename)
	cleanName = strings.ReplaceAll(cleanName, "\x00", "")
	if cleanName == "" || cleanName == "." || cleanName == "/" || cleanName == ".." {
		cleanName = "attachment"
	}
	return "/file/attachments/" + url.PathEscape(uid) + "/" + url.PathEscape(cleanName)
}

func compareVersion(got, min string) int {
	g := parseVersion(got)
	m := parseVersion(min)
	for i := 0; i < 3; i++ {
		if g[i] > m[i] {
			return 1
		}
		if g[i] < m[i] {
			return -1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(trimNonDigit(parts[i]))
		out[i] = n
	}
	return out
}

func trimNonDigit(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func findContentLengthLimit(raw map[string]interface{}) (int64, bool) {
	if v, ok := raw["content_length_limit"]; ok {
		return numberToInt64(v)
	}
	if v, ok := raw["contentLengthLimit"]; ok {
		return numberToInt64(v)
	}
	for _, key := range []string{"memo_related_setting", "memoRelatedSetting", "setting", "value"} {
		if child, ok := raw[key].(map[string]interface{}); ok {
			if v, ok := findContentLengthLimit(child); ok {
				return v, true
			}
		}
	}
	if settings, ok := raw["settings"].([]interface{}); ok {
		for _, item := range settings {
			if m, ok := item.(map[string]interface{}); ok {
				if v, ok := findContentLengthLimit(m); ok {
					return v, true
				}
			}
		}
	}
	return 0, false
}

func numberToInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(100*(1<<uint(attempt-1))) * time.Millisecond
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

func sanitizeBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	return redact.Short(text, 512)
}

func redactError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redact.Short(err.Error(), 512))
}
