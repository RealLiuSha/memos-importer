package converter

import (
	"strings"
	"testing"
)

func TestConvertRichTextAttachmentAndWarning(t *testing.T) {
	blocks := []map[string]interface{}{
		{
			"id":   "p1",
			"type": "paragraph",
			"paragraph": map[string]interface{}{"rich_text": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": map[string]interface{}{"content": "Hello "},
				},
				map[string]interface{}{
					"type":        "text",
					"text":        map[string]interface{}{"content": "world"},
					"annotations": map[string]interface{}{"bold": true},
					"href":        "https://example.com",
				},
			}},
		},
		{
			"id":   "img1",
			"type": "image",
			"image": map[string]interface{}{
				"type":    "file",
				"file":    map[string]interface{}{"url": "https://notion.example/a.png"},
				"caption": []interface{}{map[string]interface{}{"text": map[string]interface{}{"content": "A"}}},
			},
		},
		{"id": "x", "type": "synced_block", "synced_block": map[string]interface{}{}},
	}
	result := New().Convert(blocks)
	if !strings.Contains(result.Markdown, "Hello **[world](https://example.com)**") {
		t.Fatalf("rich text not rendered: %s", result.Markdown)
	}
	if len(result.Attachments) != 1 || !strings.Contains(result.Markdown, result.Attachments[0].Token) {
		t.Fatalf("attachment token missing: %#v markdown=%s", result.Attachments, result.Markdown)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "unsupported_block" {
		t.Fatalf("warning missing: %#v", result.Warnings)
	}
}

func TestConvertTable(t *testing.T) {
	blocks := []map[string]interface{}{
		{
			"id": "tbl", "type": "table",
			"children": []interface{}{
				map[string]interface{}{"type": "table_row", "table_row": map[string]interface{}{"cells": []interface{}{
					[]interface{}{map[string]interface{}{"text": map[string]interface{}{"content": "A"}}},
					[]interface{}{map[string]interface{}{"text": map[string]interface{}{"content": "B"}}},
				}}},
				map[string]interface{}{"type": "table_row", "table_row": map[string]interface{}{"cells": []interface{}{
					[]interface{}{map[string]interface{}{"text": map[string]interface{}{"content": "1"}}},
					[]interface{}{map[string]interface{}{"text": map[string]interface{}{"content": "2"}}},
				}}},
			},
		},
	}
	got := New().Convert(blocks).Markdown
	if !strings.Contains(got, "| A | B |") || !strings.Contains(got, "| 1 | 2 |") {
		t.Fatalf("table not rendered:\n%s", got)
	}
}

func TestConvertCommonBlocksNestedAndDeterministic(t *testing.T) {
	blocks := []map[string]interface{}{
		{"id": "h1", "type": "heading_1", "heading_1": map[string]interface{}{"rich_text": rt("Heading")}},
		{
			"id": "todo", "type": "to_do",
			"to_do": map[string]interface{}{"checked": true, "rich_text": rt("Done")},
			"children": []interface{}{
				map[string]interface{}{"id": "child", "type": "bulleted_list_item", "bulleted_list_item": map[string]interface{}{"rich_text": rt("Nested")}},
			},
		},
		{"id": "num", "type": "numbered_list_item", "numbered_list_item": map[string]interface{}{"rich_text": rt("One")}},
		{"id": "code", "type": "code", "code": map[string]interface{}{"language": "go", "rich_text": rt("fmt.Println(\"hi\")")}},
		{"id": "quote", "type": "quote", "quote": map[string]interface{}{"rich_text": rt("Quoted")}},
		{"id": "callout", "type": "callout", "callout": map[string]interface{}{"rich_text": rt("Callout")}},
		{"id": "divider", "type": "divider", "divider": map[string]interface{}{}},
		{"id": "bookmark", "type": "bookmark", "bookmark": map[string]interface{}{"url": "https://example.com", "caption": rt("Example")}},
		{
			"id": "toggle", "type": "toggle",
			"toggle": map[string]interface{}{"rich_text": rt("More")},
			"children": []interface{}{
				map[string]interface{}{"id": "p", "type": "paragraph", "paragraph": map[string]interface{}{"rich_text": rt("Inside")}},
			},
		},
	}
	first := New().Convert(blocks)
	second := New().Convert(blocks)
	if first.Markdown != second.Markdown {
		t.Fatalf("conversion should be deterministic:\nfirst=%s\nsecond=%s", first.Markdown, second.Markdown)
	}
	for _, want := range []string{
		"## Heading",
		"- [x] Done",
		"  - Nested",
		"1. One",
		"```go\nfmt.Println(\"hi\")\n```",
		"> Quoted",
		"> Callout",
		"---",
		"[Example](https://example.com)",
		"<details>",
		"<summary>More</summary>",
		"Inside",
	} {
		if !strings.Contains(first.Markdown, want) {
			t.Fatalf("expected %q in markdown:\n%s", want, first.Markdown)
		}
	}
	if len(first.Warnings) != 1 || first.Warnings[0].Code != "toggle_flattened" {
		t.Fatalf("expected toggle warning only, got %#v", first.Warnings)
	}
}

func TestConvertExternalAttachmentWarning(t *testing.T) {
	result := New().Convert([]map[string]interface{}{
		{
			"id":   "img-ext",
			"type": "image",
			"image": map[string]interface{}{
				"type":     "external",
				"external": map[string]interface{}{"url": "https://cdn.example.com/a.png"},
				"caption":  rt("External image"),
			},
		},
	})
	if len(result.Attachments) != 0 {
		t.Fatalf("external attachment should not be uploaded by importer: %#v", result.Attachments)
	}
	if !strings.Contains(result.Markdown, "![External image](https://cdn.example.com/a.png)") {
		t.Fatalf("external image link not preserved: %s", result.Markdown)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "external_attachment" {
		t.Fatalf("expected external attachment warning, got %#v", result.Warnings)
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename(`..\evil` + "\x00" + `.png`); got != "evil.png" {
		t.Fatalf("unexpected sanitized filename: %q", got)
	}
	if got := sanitizeFilename(".."); got != "attachment" {
		t.Fatalf("unexpected fallback filename: %q", got)
	}
}

func rt(text string) []interface{} {
	return []interface{}{map[string]interface{}{"text": map[string]interface{}{"content": text}}}
}
