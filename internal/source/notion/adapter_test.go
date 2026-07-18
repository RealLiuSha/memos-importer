package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memos-importer/internal/source"
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
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["filter"]; ok {
			t.Fatalf("document search should combine pages and databases: %#v", body)
		}
		sortBody, _ := body["sort"].(map[string]interface{})
		if sortBody["direction"] != "descending" || sortBody["timestamp"] != "last_edited_time" {
			t.Fatalf("missing last-edited descending sort: %#v", body)
		}
		call := atomic.AddInt32(&searchCalls, 1)
		switch call {
		case 1:
			if body["page_size"] != float64(3) {
				t.Fatalf("unexpected first page size: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{
					map[string]interface{}{
						"id": "page-1", "object": "page", "last_edited_time": "2024-01-02T00:00:00Z",
						"properties": map[string]interface{}{
							"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "Page 1"}}},
						},
					},
					map[string]interface{}{
						"id": "db-1", "object": "database", "last_edited_time": "2024-01-03T00:00:00Z",
						"title": []interface{}{map[string]interface{}{"plain_text": "Database"}},
					},
				},
				"has_more":    true,
				"next_cursor": "cursor-2",
			})
		case 2:
			if body["page_size"] != float64(1) || body["start_cursor"] != "cursor-2" {
				t.Fatalf("unexpected second page request: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []interface{}{map[string]interface{}{
					"id": "page-2", "object": "page", "last_edited_time": "2024-01-04T00:00:00Z",
					"properties": map[string]interface{}{
						"Name": map[string]interface{}{"type": "title", "title": []interface{}{map[string]interface{}{"plain_text": "Page 2"}}},
					},
				}},
				"has_more":    true,
				"next_cursor": "cursor-3",
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
	list, err := adapter.ListDocuments(context.Background(), source.ListOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 3 || !list.HasMore || searchCalls != 2 {
		t.Fatalf("expected a limited result with more pages, got %#v calls=%d", list, searchCalls)
	}
	refs := list.Documents
	if refs[0].ID != "page-2" || refs[1].ID != "db-1" || refs[2].ID != "page-1" || refs[1].Kind != "database" {
		t.Fatalf("documents were not sorted by updated_at descending: %#v", refs)
	}
}

func TestAdapterListDocumentsCapsEachSearchPageAtOneHundred(t *testing.T) {
	var pageSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		pageSize := int(body["page_size"].(float64))
		pageSizes = append(pageSizes, pageSize)
		results := make([]interface{}, 0, pageSize)
		for i := 0; i < pageSize; i++ {
			id := fmt.Sprintf("page-%03d-%03d", len(pageSizes), i)
			results = append(results, map[string]interface{}{
				"id": id, "object": "page", "last_edited_time": "2024-01-02T00:00:00Z",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": results, "has_more": true, "next_cursor": fmt.Sprintf("cursor-%d", len(pageSizes)+1),
		})
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
	list, err := adapter.ListDocuments(context.Background(), source.ListOptions{Limit: 250})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(pageSizes); got != "[100 100 50]" {
		t.Fatalf("unexpected search page sizes: %s", got)
	}
	if len(list.Documents) != 250 || !list.HasMore {
		t.Fatalf("unexpected limited result: len=%d has_more=%v", len(list.Documents), list.HasMore)
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
