package notion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"memos-importer/internal/domain"
	"memos-importer/internal/redact"
	"memos-importer/internal/source/notion/converter"
)

type Adapter struct {
	client             *Client
	httpClient         *http.Client
	requestTimeout     time.Duration
	downloadTimeout    time.Duration
	downloadMaxRetries int
	timeSource         string
	conv               *converter.Converter
}

type AdapterOption func(*Adapter)

func WithClient(client *Client) AdapterOption {
	return func(a *Adapter) {
		if client != nil {
			a.client = client
		}
	}
}

func WithDownloadClient(client *http.Client) AdapterOption {
	return func(a *Adapter) {
		if client != nil {
			a.httpClient = client
		}
	}
}

func WithAdapterRequestTimeout(timeout time.Duration) AdapterOption {
	return func(a *Adapter) {
		if timeout > 0 {
			a.requestTimeout = timeout
			a.downloadTimeout = timeout
		}
	}
}

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

func NewAdapter(token, timeSource string, opts ...AdapterOption) (*Adapter, error) {
	a := &Adapter{
		httpClient:         http.DefaultClient,
		requestTimeout:     30 * time.Second,
		downloadTimeout:    30 * time.Second,
		downloadMaxRetries: 2,
		conv:               converter.New(),
	}
	normalizedTimeSource, err := NormalizeTimeSource(timeSource)
	if err != nil {
		return nil, err
	}
	a.timeSource = normalizedTimeSource
	for _, opt := range opts {
		opt(a)
	}
	if a.client == nil {
		client, err := NewClient(token, WithRequestTimeout(a.requestTimeout))
		if err != nil {
			return nil, err
		}
		a.client = client
	}
	return a, nil
}

func (a *Adapter) Name() string {
	return "notion"
}

func (a *Adapter) Verify(ctx context.Context) error {
	_, err := a.client.Search(ctx, map[string]interface{}{"page_size": 1})
	return err
}

func (a *Adapter) ListDocuments(ctx context.Context) ([]domain.DocumentRef, error) {
	var refs []domain.DocumentRef
	seen := make(map[string]bool)
	for _, filter := range []string{"page", "database"} {
		cursor := ""
		for {
			body := map[string]interface{}{
				"page_size": 100,
				"filter": map[string]interface{}{
					"property": "object",
					"value":    filter,
				},
			}
			if cursor != "" {
				body["start_cursor"] = cursor
			}
			resp, err := a.client.Search(ctx, body)
			if err != nil {
				return nil, err
			}
			for _, item := range arr(resp["results"]) {
				m := obj(item)
				id := str(m["id"])
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				updatedAt, _ := parseTime(str(m["last_edited_time"]))
				refs = append(refs, domain.DocumentRef{
					Source:    "notion",
					ID:        id,
					Title:     titleFromObject(m),
					ParentID:  parentID(m),
					Kind:      str(m["object"]),
					UpdatedAt: updatedAt,
				})
			}
			if hasMore(resp) {
				cursor = str(resp["next_cursor"])
				continue
			}
			break
		}
	}
	return refs, nil
}

func (a *Adapter) FetchDocument(ctx context.Context, id string) (*domain.Document, error) {
	page, err := a.client.RetrievePage(ctx, id)
	if err != nil {
		return nil, err
	}
	blocks, err := a.fetchChildrenRecursive(ctx, id)
	if err != nil {
		return nil, err
	}
	converted := a.conv.Convert(blocks)
	createdAt, _ := parseTime(str(page["created_time"]))
	updatedAt, _ := parseTime(str(page["last_edited_time"]))
	chosenTime := a.resolveTime(page, createdAt, updatedAt)
	attachments := make([]domain.Attachment, 0, len(converted.Attachments))
	for _, ref := range converted.Attachments {
		ref := ref
		attachments = append(attachments, domain.Attachment{
			Source:     "notion",
			ExternalID: ref.BlockID,
			Filename:   ref.Filename,
			MimeType:   ref.MimeType,
			SizeBytes:  ref.SizeBytes,
			Token:      ref.Token,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				return a.openURL(ctx, ref.URL)
			},
		})
	}
	return &domain.Document{
		Ref: domain.DocumentRef{
			Source:    "notion",
			ID:        id,
			Title:     titleFromObject(page),
			ParentID:  parentID(page),
			Kind:      str(page["object"]),
			UpdatedAt: updatedAt,
		},
		Content:     converted.Markdown,
		CreatedAt:   chosenTime,
		UpdatedAt:   updatedAt,
		Tags:        tagsFromPage(page),
		Warnings:    converted.Warnings,
		Attachments: attachments,
	}, nil
}

func (a *Adapter) ExpandDocumentIDs(ctx context.Context, ids []string) ([]string, error) {
	var expanded []string
	seen := make(map[string]bool)
	for _, id := range ids {
		pageIDs, ok, err := a.databasePageIDs(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			if !seen[id] {
				expanded = append(expanded, id)
				seen[id] = true
			}
			continue
		}
		for _, pageID := range pageIDs {
			if !seen[pageID] {
				expanded = append(expanded, pageID)
				seen[pageID] = true
			}
		}
	}
	return expanded, nil
}

func (a *Adapter) databasePageIDs(ctx context.Context, id string) ([]string, bool, error) {
	if _, err := a.client.RetrieveDatabase(ctx, id); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && isNotDatabaseResponse(httpErr) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var ids []string
	cursor := ""
	for {
		resp, err := a.client.QueryDatabase(ctx, id, cursor)
		if err != nil {
			return nil, true, err
		}
		for _, item := range arr(resp["results"]) {
			m := obj(item)
			if str(m["object"]) == "page" {
				if pageID := str(m["id"]); pageID != "" {
					ids = append(ids, pageID)
				}
			}
		}
		if hasMore(resp) {
			cursor = str(resp["next_cursor"])
			continue
		}
		break
	}
	return ids, true, nil
}

func isNotDatabaseResponse(err *HTTPError) bool {
	if err == nil {
		return false
	}
	if err.StatusCode == http.StatusNotFound {
		return true
	}
	if err.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(err.Body)
	return strings.Contains(body, "not a database") || strings.Contains(body, "is a page")
}

func (a *Adapter) fetchChildrenRecursive(ctx context.Context, blockID string) ([]map[string]interface{}, error) {
	var all []map[string]interface{}
	cursor := ""
	for {
		resp, err := a.client.ListBlockChildren(ctx, blockID, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range arr(resp["results"]) {
			block := obj(item)
			if len(block) == 0 {
				continue
			}
			if boolValue(block["has_children"]) {
				children, err := a.fetchChildrenRecursive(ctx, str(block["id"]))
				if err != nil {
					return nil, err
				}
				block["children"] = mapsToInterfaces(children)
			}
			all = append(all, block)
		}
		if hasMore(resp) {
			cursor = str(resp["next_cursor"])
			continue
		}
		break
	}
	return all, nil
}

func (a *Adapter) resolveTime(page map[string]interface{}, createdAt, updatedAt time.Time) time.Time {
	switch {
	case a.timeSource == TimeSourceLastEdited:
		if !updatedAt.IsZero() {
			return updatedAt
		}
	case strings.HasPrefix(a.timeSource, "property:"):
		name := strings.TrimPrefix(a.timeSource, "property:")
		if t, ok := dateProperty(page, name); ok {
			return t
		}
	}
	return createdAt
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
