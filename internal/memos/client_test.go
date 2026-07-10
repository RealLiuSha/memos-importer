package memos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCreateMemoAndAttachment(t *testing.T) {
	var sawAuth bool
	var sawAttachment bool
	var sawCreateTime bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer token" {
			sawAuth = true
		}
		switch r.URL.Path {
		case "/api/v1/instance/profile":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"version": "0.29.1"})
		case "/api/v1/attachments":
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			content, _ := req["content"].(string)
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				t.Fatalf("content was not base64: %v", err)
			}
			if string(decoded) != "hello" {
				t.Fatalf("unexpected attachment content: %q", string(decoded))
			}
			sawAttachment = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"name": "attachments/1", "uid": "uid1", "filename": "a.txt", "type": "text/plain", "size": "5"})
		case "/api/v1/memos":
			var req Memo
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			sawCreateTime = !req.CreateTime.IsZero()
			_ = json.NewEncoder(w).Encode(Memo{Name: "memos/1", Content: req.Content})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "token", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.VerifyMinVersion(context.Background(), "0.29.1"); err != nil {
		t.Fatal(err)
	}
	attachment, err := client.CreateAttachment(context.Background(), CreateAttachmentRequest{Filename: "a.txt", Type: "text/plain", Content: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Size != 5 {
		t.Fatalf("string attachment size was not decoded: %#v", attachment)
	}
	if _, err := client.CreateMemo(context.Background(), CreateMemoRequest{Memo: Memo{Content: "memo", CreateTime: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if !sawAuth || !sawAttachment || !sawCreateTime {
		t.Fatalf("missing expected request markers: auth=%v attachment=%v createTime=%v", sawAuth, sawAttachment, sawCreateTime)
	}
}

func TestContentLengthLimitBatchGet(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/instance/settings:batchGet" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Names) != 1 || req.Names[0] != "instance/settings/MEMO_RELATED" {
			t.Fatalf("unexpected names: %#v", req.Names)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"settings": []interface{}{map[string]interface{}{
				"name":               "instance/settings/MEMO_RELATED",
				"memoRelatedSetting": map[string]interface{}{"contentLengthLimit": float64(4096)},
			}},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	limit, err := client.ContentLengthLimit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limit != 4096 || calls != 1 {
		t.Fatalf("unexpected limit/calls: %d/%d", limit, calls)
	}
}

func TestContentLengthLimitLegacyFallback(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/v1/instance/settings:batchGet":
			http.NotFound(w, r)
		case "/api/v1/instance/settings/memo_related":
			http.NotFound(w, r)
		case "/api/v1/instance/settings":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"settings": []interface{}{map[string]interface{}{
					"memo_related_setting": map[string]interface{}{"content_length_limit": float64(1234)},
				}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	limit, err := client.ContentLengthLimit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limit != 1234 || calls != 3 {
		t.Fatalf("unexpected limit/calls: %d/%d", limit, calls)
	}
}

func TestNewAcceptsAPIBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/instance/profile" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"version": "0.29.1"})
	}))
	defer server.Close()

	client, err := New(server.URL+"/api/v1", "", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InstanceProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClientUpdateMemoUsesTopLevelMemoBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/memos/1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["content"] != "updated" || body["memo"] != nil || body["update_mask"] != nil || body["updateMask"] != nil {
			t.Fatalf("unexpected update body: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(Memo{Name: "memos/1", Content: "updated"})
	}))
	defer server.Close()

	client, err := New(server.URL, "token", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	memo, err := client.UpdateMemo(context.Background(), "memos/1", UpdateMemoRequest{
		Memo: Memo{Name: "memos/1", Content: "updated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if memo.Content != "updated" {
		t.Fatalf("unexpected memo response: %#v", memo)
	}
}

func TestVersionCompare(t *testing.T) {
	client, err := New("http://example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = client
	if compareVersion("0.29.0", "0.29.1") >= 0 {
		t.Fatal("0.29.0 should be lower than 0.29.1")
	}
	if compareVersion("v0.30.0", "0.29.1") <= 0 {
		t.Fatal("v0.30.0 should be higher than 0.29.1")
	}
}

func TestTraceRedactsSensitiveFields(t *testing.T) {
	var traces []TraceEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":    "memos/1",
			"content": "body with https://notion.example/file.png?secret=value",
			"attachments": []interface{}{map[string]interface{}{
				"name": "attachments/1", "uid": "uid1", "filename": "a.png",
			}},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "token", WithMaxRetries(0), WithTrace(func(event TraceEvent) {
		traces = append(traces, event)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateMemo(context.Background(), CreateMemoRequest{Memo: Memo{
		Content: "secret body",
		Attachments: []Attachment{{
			Name: "attachments/1", UID: "uid1", Filename: "a.png",
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected one trace, got %#v", traces)
	}
	req := traces[0].Request.(map[string]interface{})
	content := req["content"].(map[string]interface{})
	if content["redacted"] != true || content["bytes"].(int) == 0 {
		t.Fatalf("request content was not summarized: %#v", req["content"])
	}
	resp := traces[0].Response.(map[string]interface{})
	if _, ok := resp["content"].(map[string]interface{}); !ok {
		t.Fatalf("response content was not summarized: %#v", resp["content"])
	}
	if strings.Contains(fmt.Sprint(traces[0]), "secret body") || strings.Contains(fmt.Sprint(traces[0]), "secret=value") {
		t.Fatalf("trace leaked sensitive content: %#v", traces[0])
	}
}

func TestHTTPErrorRedactsURLQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `Authorization: Bearer raw-token token=secret temporary https://user:pass@notion.example/file.png?X-Amz-Signature=secret`, http.StatusBadGateway)
	}))
	defer server.Close()

	client, err := New(server.URL, "", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.InstanceProfile(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, forbidden := range []string{"raw-token", "token=secret", "user:pass", "X-Amz-Signature=secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("HTTP error leaked %q: %s", forbidden, err)
		}
	}
}

func TestCheckAttachmentFile(t *testing.T) {
	var sawHead bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file/attachments/uid1/a.png" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing auth header")
		}
		sawHead = true
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := New(server.URL, "token", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	check, err := client.CheckAttachmentFile(context.Background(), "uid1", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	if !sawHead || !check.OK || check.StatusCode != http.StatusOK || check.ContentType != "image/png" {
		t.Fatalf("unexpected check: %#v", check)
	}
}

func TestCheckAttachmentFileFallsBackToGetOn405(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodHead:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("ok"))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "", WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	check, err := client.CheckAttachmentFile(context.Background(), "uid 1", "a b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "HEAD,GET" {
		t.Fatalf("unexpected methods: %#v", methods)
	}
	if !check.OK || !strings.Contains(check.Path, "uid%201") || !strings.Contains(check.Path, "a%20b.txt") {
		t.Fatalf("unexpected fallback check: %#v", check)
	}
}

func TestAttachmentFilePathSanitizesFilename(t *testing.T) {
	got := AttachmentFilePath("uid", `..\evil`+"\x00"+`.png`)
	if got != "/file/attachments/uid/evil.png" {
		t.Fatalf("unexpected sanitized file path: %s", got)
	}
	if got := AttachmentFilePath("uid", ".."); got != "/file/attachments/uid/attachment" {
		t.Fatalf("unexpected fallback file path: %s", got)
	}
}
