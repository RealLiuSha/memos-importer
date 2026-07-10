package notion

import (
	"strings"
	"time"
)

func obj(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func arr(v interface{}) []interface{} {
	if a, ok := v.([]interface{}); ok {
		return a
	}
	return nil
}

func str(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func hasMore(m map[string]interface{}) bool {
	return boolValue(m["has_more"]) && str(m["next_cursor"]) != ""
}

func mapsToInterfaces(in []map[string]interface{}) []interface{} {
	out := make([]interface{}, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}

func parseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return t, true
	}
	t, err = time.Parse(time.RFC3339, value)
	return t, err == nil
}

func titleFromObject(m map[string]interface{}) string {
	if title := richTextPlain(arr(m["title"])); title != "" {
		return title
	}
	props := obj(m["properties"])
	for _, value := range props {
		prop := obj(value)
		if str(prop["type"]) == "title" {
			if title := richTextPlain(arr(prop["title"])); title != "" {
				return title
			}
		}
	}
	if id := str(m["id"]); id != "" {
		return id
	}
	return "Untitled"
}

func tagsFromPage(m map[string]interface{}) []string {
	props := obj(m["properties"])
	var tags []string
	for _, value := range props {
		prop := obj(value)
		switch str(prop["type"]) {
		case "multi_select":
			for _, item := range arr(prop["multi_select"]) {
				if name := str(obj(item)["name"]); name != "" {
					tags = append(tags, cleanTag(name))
				}
			}
		case "select":
			if name := str(obj(prop["select"])["name"]); name != "" {
				tags = append(tags, cleanTag(name))
			}
		}
	}
	return tags
}

func dateProperty(m map[string]interface{}, name string) (time.Time, bool) {
	props := obj(m["properties"])
	prop := obj(props[name])
	if prop == nil || str(prop["type"]) != "date" {
		return time.Time{}, false
	}
	date := obj(prop["date"])
	return parseTime(str(date["start"]))
}

func parentID(m map[string]interface{}) string {
	parent := obj(m["parent"])
	for _, key := range []string{"page_id", "database_id", "workspace"} {
		if value := str(parent[key]); value != "" && value != "true" {
			return value
		}
	}
	return ""
}

func richTextPlain(items []interface{}) string {
	var b strings.Builder
	for _, item := range items {
		m := obj(item)
		if text := str(m["plain_text"]); text != "" {
			b.WriteString(text)
			continue
		}
		if text := obj(m["text"]); text != nil {
			b.WriteString(str(text["content"]))
		}
	}
	return strings.TrimSpace(b.String())
}

func cleanTag(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "#")
	name = strings.NewReplacer(" ", "_", "\t", "_", "\n", "_").Replace(name)
	return name
}
