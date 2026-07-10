package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"memos-importer/internal/domain"
	"memos-importer/internal/memos"
	"memos-importer/internal/store"
)

func (e *Engine) prepareContent(ctx context.Context, doc *domain.Document) (string, []memos.Attachment, error) {
	content := ComposeContent(doc)
	replacements := make(map[string]string)
	memoAttachments := make([]memos.Attachment, 0, len(doc.Attachments))
	seenTokens := make(map[string]bool)
	for _, attachment := range doc.Attachments {
		if attachment.Token == "" {
			return "", nil, fmt.Errorf("attachment %s has empty token", attachment.ExternalID)
		}
		if seenTokens[attachment.Token] {
			return "", nil, fmt.Errorf("duplicate attachment token %s", attachment.Token)
		}
		seenTokens[attachment.Token] = true
		if !strings.Contains(content, attachment.Token) {
			return "", nil, fmt.Errorf("attachment token %s not found in content", attachment.Token)
		}
		mapping, err := e.store.GetAttachmentMapping(ctx, attachment.Source, attachment.ExternalID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", nil, err
		}
		if mapping == nil {
			uploaded, err := e.uploadAttachment(ctx, attachment)
			if err != nil {
				return "", nil, err
			}
			if uploaded.Name == "" {
				return "", nil, fmt.Errorf("memos attachment response missing name for %s", attachment.ExternalID)
			}
			sizeBytes := uploaded.Size
			if sizeBytes <= 0 {
				sizeBytes = attachment.SizeBytes
			}
			mapping = &store.AttachmentMapping{
				Source:              attachment.Source,
				ExternalID:          attachment.ExternalID,
				MemosAttachmentName: uploaded.Name,
				UID:                 uploaded.UID,
				Filename:            sanitizeAttachmentFilename(firstNonEmpty(uploaded.Filename, attachment.Filename)),
				MimeType:            firstNonEmpty(uploaded.Type, attachment.MimeType),
				SizeBytes:           sizeBytes,
				ImportedAt:          time.Now().UTC(),
			}
			if mapping.UID == "" {
				mapping.UID = attachmentUIDFromName(mapping.MemosAttachmentName)
			}
			if mapping.UID == "" {
				return "", nil, fmt.Errorf("memos attachment response missing uid for %s", attachment.ExternalID)
			}
			if err := e.store.UpsertAttachmentMapping(ctx, *mapping); err != nil {
				return "", nil, err
			}
		}
		replacements[attachment.Token] = memos.AttachmentFilePath(mapping.UID, mapping.Filename)
		memoAttachments = append(memoAttachments, memos.Attachment{
			Name:     mapping.MemosAttachmentName,
			UID:      mapping.UID,
			Filename: mapping.Filename,
			Type:     mapping.MimeType,
			Size:     mapping.SizeBytes,
		})
	}
	for token, replacement := range replacements {
		content = strings.ReplaceAll(content, token, replacement)
	}
	for token := range seenTokens {
		if strings.Contains(content, token) {
			return "", nil, fmt.Errorf("attachment token %s was not replaced", token)
		}
	}
	return content, memoAttachments, nil
}

func (e *Engine) uploadAttachment(ctx context.Context, attachment domain.Attachment) (*memos.Attachment, error) {
	if attachment.Open == nil {
		return nil, fmt.Errorf("attachment %s cannot be opened", attachment.ExternalID)
	}
	rc, err := attachment.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := readBounded(rc, e.options.MaxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	return e.memos.CreateAttachment(ctx, memos.CreateAttachmentRequest{
		Filename: sanitizeAttachmentFilename(attachment.Filename),
		Type:     attachment.MimeType,
		Content:  data,
	})
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 32 << 20
	}
	lr := &io.LimitedReader{R: r, N: limit + 1}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("attachment exceeds size limit %d bytes", limit)
	}
	return data, nil
}

func sanitizeAttachmentFilename(filename string) string {
	filename = strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	filename = path.Base(filename)
	filename = strings.ReplaceAll(filename, "\x00", "")
	if filename == "" || filename == "." || filename == "/" || filename == ".." {
		return "attachment"
	}
	return filename
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func attachmentUIDFromName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}
