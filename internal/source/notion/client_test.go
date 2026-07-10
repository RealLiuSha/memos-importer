package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientEscapesPathIDs(t *testing.T) {
	const id = "id/with space"
	seen := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.EscapedPath()] = true
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
	}))
	defer server.Close()
	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RetrievePage(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RetrieveDatabase(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListBlockChildren(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.QueryDatabase(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"GET /pages/id%2Fwith%20space",
		"GET /databases/id%2Fwith%20space",
		"GET /blocks/id%2Fwith%20space/children",
		"POST /databases/id%2Fwith%20space/query",
	} {
		if !seen[want] {
			t.Fatalf("missing escaped request %s, seen=%#v", want, seen)
		}
	}
}

func TestClientListBlockChildrenEscapesCursor(t *testing.T) {
	const cursor = "opaque+cursor=1&next=true"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/blocks/block-1/children" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("start_cursor"); got != cursor {
			t.Fatalf("cursor was not preserved through query encoding: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}, "has_more": false})
	}))
	defer server.Close()

	client, err := NewClient("token", WithBaseURL(server.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListBlockChildren(context.Background(), "block-1", cursor); err != nil {
		t.Fatal(err)
	}
}
