package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"memos-importer/internal/domain"
	"memos-importer/internal/memos"
	"memos-importer/internal/store"
)

// ComposeContent renders the memo body exactly as the importer sends it before attachment token replacement.
func ComposeContent(doc *domain.Document) string {
	var b strings.Builder
	title := strings.TrimSpace(doc.Ref.Title)
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(doc.Content))
	if len(doc.Tags) > 0 {
		b.WriteString("\n\n")
		for i, tag := range doc.Tags {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteByte('#')
			b.WriteString(sanitizeTag(tag))
		}
	}
	return strings.TrimSpace(b.String())
}

func contentHash(content string, attachments []memos.Attachment) string {
	h := sha256.New()
	h.Write([]byte(content))
	for _, attachment := range attachments {
		h.Write([]byte("\x00"))
		h.Write([]byte(attachment.Name))
		h.Write([]byte(attachment.UID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func documentUnchanged(mapping *store.DocumentMapping, doc *domain.Document, hash string) bool {
	if mapping == nil || mapping.ContentHash != hash {
		return false
	}
	if doc.UpdatedAt.IsZero() || mapping.SourceUpdatedAt.IsZero() {
		return true
	}
	return !doc.UpdatedAt.After(mapping.SourceUpdatedAt)
}

func sanitizeTag(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
	tag = strings.NewReplacer(" ", "_", "\t", "_", "\n", "_").Replace(tag)
	if tag == "" {
		return "untagged"
	}
	return tag
}
