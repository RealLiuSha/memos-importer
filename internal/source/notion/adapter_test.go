package notion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdapterFetchDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing auth")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/pages/page-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "page-1", "object": "page", "created_time": "2024-01-01T00:00:00Z", "last_edited_time": "2024-01-02T00:00:00Z",
				"properties": map[string]interface{}{
					"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "My Page"}}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/page-1/children":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{"id": "p", "type": "paragraph", "paragraph": map[string]interface{}{"rich_text": []interface{}{map[string]interface{}{"plain_text": "hello"}}}},
					map[string]interface{}{"id": "x", "type": "synced_block", "synced_block": map[string]interface{}{}},
				},
				"has_more": false,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "created_time", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := adapter.FetchDocument(context.Background(), "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Ref.Title != "My Page" || !strings.Contains(doc.Content, "hello") || len(doc.Warnings) != 1 {
		t.Fatalf("unexpected doc: %#v", doc)
	}
}

func TestAdapterRequestTimeoutOptionAppliesToClientAndDownload(t *testing.T) {
	adapter, err := NewAdapter("token", "created_time", WithAdapterRequestTimeout(250*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if adapter.client.requestTimeout != 250*time.Millisecond {
		t.Fatalf("client timeout was not configured: %s", adapter.client.requestTimeout)
	}
	if adapter.downloadTimeout != 250*time.Millisecond {
		t.Fatalf("download timeout was not configured: %s", adapter.downloadTimeout)
	}
}

func TestAdapterListDocumentsPaginationAndDatabase(t *testing.T) {
	var searchCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		call := atomic.AddInt32(&searchCalls, 1)
		switch call {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{map[string]interface{}{
					"id": "page-1", "object": "page", "last_edited_time": "2024-01-02T00:00:00Z",
					"properties": map[string]interface{}{
						"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "Page 1"}}},
					},
				}},
				"has_more":    true,
				"next_cursor": "cursor-2",
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{map[string]interface{}{
					"id": "db-1", "object": "database", "last_edited_time": "2024-01-03T00:00:00Z",
					"title": []interface{}{map[string]interface{}{"plain_text": "Database"}},
				}},
				"has_more": false,
			})
		default:
			t.Fatalf("unexpected search call %d", call)
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "created_time", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := adapter.ListDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %#v", refs)
	}
	if refs[0].Title != "Page 1" || refs[1].Title != "Database" || refs[1].Kind != "database" {
		t.Fatalf("unexpected refs: %#v", refs)
	}
}

func TestAdapterFetchDocumentUsesDateProperty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/pages/page-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "page-1", "object": "page", "created_time": "2024-01-01T00:00:00Z", "last_edited_time": "2024-01-02T00:00:00Z",
				"properties": map[string]interface{}{
					"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "My Page"}}},
					"When": map[string]interface{}{"type": "date", "date": map[string]interface{}{"start": "2024-02-03T04:05:06Z"}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/page-1/children":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "property:When", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := adapter.FetchDocument(context.Background(), "page-1")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, "2024-02-03T04:05:06Z")
	if !doc.CreatedAt.Equal(want) {
		t.Fatalf("expected date property time %s, got %s", want, doc.CreatedAt)
	}
}

func TestAdapterFetchDocumentUsesLastEditedTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/pages/page-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "page-1", "object": "page", "created_time": "2024-01-01T00:00:00Z", "last_edited_time": "2024-01-02T00:00:00Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/page-1/children":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "last_edited_time", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := adapter.FetchDocument(context.Background(), "page-1")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, "2024-01-02T00:00:00Z")
	if !doc.CreatedAt.Equal(want) {
		t.Fatalf("expected last edited time %s, got %s", want, doc.CreatedAt)
	}
}

func TestNewAdapterRejectsInvalidTimeSource(t *testing.T) {
	if _, err := NewAdapter("token", "last_update"); err == nil || !strings.Contains(err.Error(), "invalid notion time_source") {
		t.Fatalf("expected invalid time source error, got %v", err)
	}
	if normalized, err := NormalizeTimeSource(" property:Published At "); err != nil || normalized != "property:Published At" {
		t.Fatalf("unexpected normalized property time source: %q err=%v", normalized, err)
	}
}

func TestAdapterExpandDatabaseIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/databases/db-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "db-1", "object": "database"})
		case r.Method == http.MethodPost && r.URL.Path == "/databases/db-1/query":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{"id": "page-1", "object": "page"},
					map[string]interface{}{"id": "page-2", "object": "page"},
				},
				"has_more": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/databases/page-3":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object":  "error",
				"status":  400,
				"code":    "validation_error",
				"message": "Provided ID page-3 is a page, not a database. Use the retrieve page API instead",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/databases/page-4":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "created_time", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := adapter.ExpandDocumentIDs(context.Background(), []string{"db-1", "page-3", "page-4"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids, ",")
	if got != "page-1,page-2,page-3,page-4" {
		t.Fatalf("unexpected ids: %s", got)
	}
}

func TestAdapterAttachmentDownloadRetriesAndRedacts(t *testing.T) {
	var downloadCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/pages/page-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "page-1", "object": "page", "created_time": "2024-01-01T00:00:00Z", "last_edited_time": "2024-01-02T00:00:00Z",
				"properties": map[string]interface{}{
					"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "My Page"}}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/page-1/children":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{
						"id": "img1", "type": "image",
						"image": map[string]interface{}{
							"type": "file",
							"file": map[string]interface{}{"url": serverURL(r) + "/download/file.png?X-Amz-Signature=secret"},
						},
					},
				},
				"has_more": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/download/file.png":
			call := atomic.AddInt32(&downloadCalls, 1)
			if call == 1 {
				w.Header().Set("Retry-After", "0")
				http.Error(w, "temporary", http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte("image-bytes"))
		case r.Method == http.MethodGet && r.URL.Path == "/download/fail.png":
			http.Error(w, `Authorization: Bearer raw-token token=secret temporary https://user:pass@s3.example/file.png?X-Amz-Signature=secret`, http.StatusBadGateway)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter("token", "created_time", WithClient(client), WithDownloadMaxRetries(1), WithDownloadTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := adapter.FetchDocument(context.Background(), "page-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", doc.Attachments)
	}
	rc, err := doc.Attachments[0].Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" || downloadCalls != 2 {
		t.Fatalf("download retry did not work, data=%q calls=%d", string(data), downloadCalls)
	}

	_, err = adapter.openURL(context.Background(), server.URL+"/download/fail.png?X-Amz-Signature=secret")
	if err == nil {
		t.Fatal("expected failed download")
	}
	for _, forbidden := range []string{"raw-token", "token=secret", "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("download error leaked %q: %s", forbidden, err)
		}
	}
}

func TestClientRetries429AndRedactsURLQuery(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"message":"Authorization: Bearer raw-token token=secret temporary https://user:pass@s3.example/file.png?X-Amz-Signature=secret"}`, http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), map[string]interface{}{"page_size": 1}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected retry, got %d calls", calls)
	}

	client, err = NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	atomic.StoreInt32(&calls, 0)
	_, err = client.Search(context.Background(), map[string]interface{}{"page_size": 1})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, forbidden := range []string{"raw-token", "token=secret", "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("notion error leaked %q: %s", forbidden, err)
		}
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
