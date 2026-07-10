package converter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"

	"memos-importer/internal/domain"
)

type AttachmentRef struct {
	BlockID   string
	URL       string
	Token     string
	Filename  string
	MimeType  string
	SizeBytes int64
	External  bool
}

type Result struct {
	Markdown    string
	Attachments []AttachmentRef
	Warnings    []domain.Warning
}

type Converter struct{}

func New() *Converter {
	return &Converter{}
}

func (c *Converter) Convert(blocks []map[string]interface{}) Result {
	r := &renderer{}
	r.renderBlocks(blocks, 0)
	return Result{
		Markdown:    strings.TrimSpace(r.String()),
		Attachments: r.attachments,
		Warnings:    r.warnings,
	}
}

type renderer struct {
	b           strings.Builder
	warnings    []domain.Warning
	attachments []AttachmentRef
}

func (r *renderer) String() string {
	return r.b.String()
}

func (r *renderer) renderBlocks(blocks []map[string]interface{}, depth int) {
	for i, block := range blocks {
		if i > 0 && !strings.HasSuffix(r.b.String(), "\n\n") {
			r.b.WriteString("\n")
		}
		r.renderBlock(block, depth)
	}
}

func (r *renderer) renderBlock(block map[string]interface{}, depth int) {
	blockType := str(block["type"])
	blockID := str(block["id"])
	data := obj(block[blockType])
	switch blockType {
	case "paragraph":
		r.writeLine(renderRichText(arr(data["rich_text"])))
	case "heading_1":
		r.writeLine("## " + renderRichText(arr(data["rich_text"])))
	case "heading_2":
		r.writeLine("### " + renderRichText(arr(data["rich_text"])))
	case "heading_3":
		r.writeLine("#### " + renderRichText(arr(data["rich_text"])))
	case "bulleted_list_item":
		r.writeLine(strings.Repeat("  ", depth) + "- " + renderRichText(arr(data["rich_text"])))
		r.renderChildren(block, depth+1)
	case "numbered_list_item":
		r.writeLine(strings.Repeat("  ", depth) + "1. " + renderRichText(arr(data["rich_text"])))
		r.renderChildren(block, depth+1)
	case "to_do":
		mark := " "
		if checked, _ := data["checked"].(bool); checked {
			mark = "x"
		}
		r.writeLine(fmt.Sprintf("%s- [%s] %s", strings.Repeat("  ", depth), mark, renderRichText(arr(data["rich_text"]))))
		r.renderChildren(block, depth+1)
	case "code":
		lang := str(data["language"])
		r.writeLine("```" + lang)
		r.writeLine(richPlainText(arr(data["rich_text"])))
		r.writeLine("```")
	case "quote":
		lines := strings.Split(renderRichText(arr(data["rich_text"])), "\n")
		for _, line := range lines {
			r.writeLine("> " + line)
		}
		r.renderChildren(block, depth+1)
	case "callout":
		icon := iconText(obj(data["icon"]))
		text := strings.TrimSpace(icon + " " + renderRichText(arr(data["rich_text"])))
		for _, line := range strings.Split(text, "\n") {
			r.writeLine("> " + line)
		}
		r.renderChildren(block, depth+1)
	case "divider":
		r.writeLine("---")
	case "image", "file", "pdf", "video":
		r.renderAttachment(blockType, blockID, data)
	case "bookmark", "link_preview", "embed":
		u := str(data["url"])
		if u == "" {
			r.unsupported(blockType, blockID)
			return
		}
		caption := renderRichText(arr(data["caption"]))
		if caption == "" {
			caption = u
		}
		r.writeLine("[" + escapeMarkdown(caption) + "](" + u + ")")
		if blockType == "embed" {
			r.warn("embed_external", "embed block kept as external link", blockID)
		}
	case "table":
		r.renderTable(block)
	case "toggle":
		r.writeLine("<details>")
		summary := renderRichText(arr(data["rich_text"]))
		if summary == "" {
			summary = "Toggle"
		}
		r.writeLine("<summary>" + summary + "</summary>")
		r.renderChildren(block, depth+1)
		r.writeLine("</details>")
		r.warn("toggle_flattened", "toggle block rendered as HTML details", blockID)
	default:
		r.unsupported(blockType, blockID)
	}
}

func (r *renderer) renderChildren(block map[string]interface{}, depth int) {
	children := blocksFromAny(block["children"])
	if len(children) == 0 {
		return
	}
	r.renderBlocks(children, depth)
}

func (r *renderer) renderAttachment(blockType, blockID string, data map[string]interface{}) {
	fileType := str(data["type"])
	fileData := obj(data[fileType])
	rawURL := str(fileData["url"])
	caption := renderRichText(arr(data["caption"]))
	if caption == "" {
		caption = filenameFromURL(rawURL, blockType)
	}
	if fileType == "external" {
		if blockType == "image" {
			r.writeLine("![" + escapeMarkdown(caption) + "](" + rawURL + ")")
		} else {
			r.writeLine("[" + escapeMarkdown(caption) + "](" + rawURL + ")")
		}
		r.warn("external_attachment", "external attachment link was preserved without upload", blockID)
		return
	}
	if rawURL == "" {
		r.unsupported(blockType, blockID)
		return
	}
	filename := filenameFromURL(rawURL, blockType)
	token := attachmentToken(blockID, filename)
	ref := AttachmentRef{
		BlockID:   blockID,
		URL:       rawURL,
		Token:     token,
		Filename:  filename,
		MimeType:  mimeFromBlockType(blockType),
		SizeBytes: int64FromAny(fileData["size"]),
	}
	r.attachments = append(r.attachments, ref)
	if blockType == "image" {
		r.writeLine("![" + escapeMarkdown(caption) + "](" + token + ")")
	} else {
		r.writeLine("[" + escapeMarkdown(caption) + "](" + token + ")")
	}
}

func (r *renderer) renderTable(block map[string]interface{}) {
	children := blocksFromAny(block["children"])
	if len(children) == 0 {
		r.warn("empty_table", "table block has no rows", str(block["id"]))
		r.writeLine("<!-- Unsupported Notion table: empty -->")
		return
	}
	var rows [][]string
	for _, child := range children {
		if str(child["type"]) != "table_row" {
			continue
		}
		data := obj(child["table_row"])
		cells := arr(data["cells"])
		row := make([]string, 0, len(cells))
		for _, cell := range cells {
			row = append(row, strings.ReplaceAll(renderRichText(arr(cell)), "|", "\\|"))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		r.warn("empty_table", "table block has no table_row children", str(block["id"]))
		r.writeLine("<!-- Unsupported Notion table: no rows -->")
		return
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	header := padRow(rows[0], width)
	r.writeLine("| " + strings.Join(header, " | ") + " |")
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	r.writeLine("| " + strings.Join(sep, " | ") + " |")
	for _, row := range rows[1:] {
		r.writeLine("| " + strings.Join(padRow(row, width), " | ") + " |")
	}
}

func (r *renderer) unsupported(blockType, blockID string) {
	if blockType == "" {
		blockType = "unknown"
	}
	r.warn("unsupported_block", "unsupported Notion block type: "+blockType, blockID)
	r.writeLine("<!-- Unsupported Notion block: " + blockType + " -->")
}

func (r *renderer) warn(code, message, blockID string) {
	r.warnings = append(r.warnings, domain.NewWarning(code, message, blockID))
}

func (r *renderer) writeLine(line string) {
	r.b.WriteString(line)
	r.b.WriteString("\n")
}

func padRow(row []string, width int) []string {
	out := make([]string, width)
	copy(out, row)
	return out
}

func attachmentToken(blockID, filename string) string {
	sum := sha256.Sum256([]byte(blockID + ":" + filename))
	return "__MEMOS_IMPORTER_ATTACHMENT_" + hex.EncodeToString(sum[:12]) + "__"
}

func filenameFromURL(rawURL, fallback string) string {
	u, err := url.Parse(rawURL)
	if err == nil {
		name := path.Base(u.Path)
		if name != "." && name != "/" && name != "" {
			return sanitizeFilename(name)
		}
	}
	if fallback == "" {
		fallback = "attachment"
	}
	return sanitizeFilename(fallback)
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = path.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "." || name == "/" || name == "" || name == ".." {
		return "attachment"
	}
	return name
}

func mimeFromBlockType(blockType string) string {
	switch blockType {
	case "image":
		return "image/*"
	case "pdf":
		return "application/pdf"
	case "video":
		return "video/*"
	default:
		return "application/octet-stream"
	}
}
