package memos

import (
	"encoding/json"
	"strconv"
	"strings"
)

func compareVersion(got, min string) int {
	g := parseVersion(got)
	m := parseVersion(min)
	for i := 0; i < 3; i++ {
		if g[i] > m[i] {
			return 1
		}
		if g[i] < m[i] {
			return -1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		part := parts[i]
		for j, r := range part {
			if r < '0' || r > '9' {
				part = part[:j]
				break
			}
		}
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}

func findContentLengthLimit(raw map[string]interface{}) (int64, bool) {
	if v, ok := raw["content_length_limit"]; ok {
		return numberToInt64(v)
	}
	if v, ok := raw["contentLengthLimit"]; ok {
		return numberToInt64(v)
	}
	for _, key := range []string{"memo_related_setting", "memoRelatedSetting", "setting", "value"} {
		if child, ok := raw[key].(map[string]interface{}); ok {
			if v, ok := findContentLengthLimit(child); ok {
				return v, true
			}
		}
	}
	if settings, ok := raw["settings"].([]interface{}); ok {
		for _, item := range settings {
			if m, ok := item.(map[string]interface{}); ok {
				if v, ok := findContentLengthLimit(m); ok {
					return v, true
				}
			}
		}
	}
	return 0, false
}

func numberToInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
