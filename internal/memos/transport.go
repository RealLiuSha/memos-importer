package memos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"memos-importer/internal/redact"
)

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
