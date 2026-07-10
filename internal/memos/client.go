package memos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
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
