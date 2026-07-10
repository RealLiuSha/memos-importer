package converter

import (
	"fmt"
	"strings"
)

func renderRichText(items []interface{}) string {
	var b strings.Builder
	for _, item := range items {
		m := obj(item)
		text := richTextContent(m)
		if text == "" {
			continue
		}
		text = escapeMarkdown(text)
		annotations := obj(m["annotations"])
		if boolValue(annotations["code"]) {
			text = "`" + strings.ReplaceAll(text, "`", "\\`") + "`"
		}
		if href := str(m["href"]); href != "" {
			text = "[" + text + "](" + href + ")"
		}
		if boolValue(annotations["bold"]) {
			text = "**" + text + "**"
		}
		if boolValue(annotations["italic"]) {
			text = "*" + text + "*"
		}
		if boolValue(annotations["strikethrough"]) {
			text = "~~" + text + "~~"
		}
		b.WriteString(text)
	}
	return b.String()
}

func richPlainText(items []interface{}) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(richTextContent(obj(item)))
	}
	return b.String()
}

func richTextContent(m map[string]interface{}) string {
	if text := obj(m["text"]); text != nil {
		return str(text["content"])
	}
	if mention := obj(m["mention"]); mention != nil {
		if name := str(mention["plain_text"]); name != "" {
			return name
		}
	}
	return str(m["plain_text"])
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
	)
	return replacer.Replace(s)
}

func iconText(icon map[string]interface{}) string {
	switch str(icon["type"]) {
	case "emoji":
		return str(icon["emoji"])
	case "external":
		if external := obj(icon["external"]); external != nil {
			return str(external["url"])
		}
	case "file":
		if file := obj(icon["file"]); file != nil {
			return str(file["url"])
		}
	}
	return ""
}

func boolValue(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func debugValue(v interface{}) string {
	return fmt.Sprintf("%#v", v)
}
