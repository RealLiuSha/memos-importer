package notion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"memos-importer/internal/redact"
)

func WithDownloadTimeout(timeout time.Duration) AdapterOption {
	return func(a *Adapter) {
		if timeout > 0 {
			a.downloadTimeout = timeout
		}
	}
}

func WithDownloadMaxRetries(n int) AdapterOption {
	return func(a *Adapter) {
		if n >= 0 {
			a.downloadMaxRetries = n
		}
	}
}

func (a *Adapter) openURL(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	attempts := a.downloadMaxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt, lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		reqCtx := ctx
		cancel := func() {}
		if a.downloadTimeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, a.downloadTimeout)
		}
		body, err := a.openURLOnce(reqCtx, rawURL)
		if err == nil {
			return &cancelReadCloser{ReadCloser: body, cancel: cancel}, nil
		}
		cancel()
		lastErr = err
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || !isRetryableStatus(httpErr.StatusCode) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (a *Adapter) openURLOnce(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New(redact.Short(err.Error(), 512))
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, errors.New(redact.Short(err.Error(), 512))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		retryAfter := retryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		return nil, &HTTPError{Operation: "notion attachment download", StatusCode: resp.StatusCode, Body: sanitizeBody(body), RetryAfter: retryAfter}
	}
	return resp.Body, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
