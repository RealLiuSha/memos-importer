package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"memos-importer/internal/redact"
)

const defaultBaseURL = "https://api.notion.com/v1"
const notionVersion = "2022-06-28"

type Client struct {
	baseURL        string
	token          string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
}

type ClientOption func(*Client)

func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) {
		if baseURL != "" {
			c.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRequestTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		if timeout > 0 {
			c.requestTimeout = timeout
		}
	}
}

func WithMaxRetries(n int) ClientOption {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

func NewClient(token string, opts ...ClientOption) (*Client, error) {
	if token == "" {
		return nil, errors.New("notion token is required")
	}
	c := &Client{
		baseURL:        defaultBaseURL,
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

func (c *Client) Search(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(ctx, http.MethodPost, "/search", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) RetrievePage(ctx context.Context, id string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/pages/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) RetrieveDatabase(ctx context.Context, id string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/databases/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ListBlockChildren(ctx context.Context, id, cursor string) (map[string]interface{}, error) {
	apiPath := "/blocks/" + url.PathEscape(id) + "/children?page_size=100"
	if cursor != "" {
		apiPath += "&start_cursor=" + url.QueryEscape(cursor)
	}
	var out map[string]interface{}
	if err := c.do(ctx, http.MethodGet, apiPath, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) QueryDatabase(ctx context.Context, id, cursor string) (map[string]interface{}, error) {
	body := map[string]interface{}{"page_size": 100}
	if cursor != "" {
		body["start_cursor"] = cursor
	}
	var out map[string]interface{}
	if err := c.do(ctx, http.MethodPost, "/databases/"+url.PathEscape(id)+"/query", body, &out); err != nil {
		return nil, err
	}
	return out, nil
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
			delay := retryDelay(attempt, lastErr)
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
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Body: sanitizeBody(respBody), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode notion response: %w", err)
	}
	return nil
}

type HTTPError struct {
	Operation  string
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	operation := e.Operation
	if operation == "" {
		operation = "notion API"
	}
	if e.Body == "" {
		return fmt.Sprintf("%s returned status %d", operation, e.StatusCode)
	}
	return fmt.Sprintf("%s returned status %d: %s", operation, e.StatusCode, e.Body)
}

func sanitizeBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	return redact.Short(text, 512)
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(attempt int, err error) time.Duration {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		return httpErr.RetryAfter
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(200*(1<<uint(attempt-1))) * time.Millisecond
	if delay > 3*time.Second {
		return 3 * time.Second
	}
	return delay
}

func retryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
