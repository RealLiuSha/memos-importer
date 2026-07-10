package memos

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type InstanceProfile struct {
	Version string `json:"version"`
	Mode    string `json:"mode,omitempty"`
}

type Attachment struct {
	Name       string    `json:"name,omitempty"`
	UID        string    `json:"uid,omitempty"`
	Filename   string    `json:"filename,omitempty"`
	Type       string    `json:"type,omitempty"`
	Size       int64     `json:"size,omitempty"`
	CreateTime time.Time `json:"create_time,omitempty"`
}

func (a *Attachment) UnmarshalJSON(data []byte) error {
	type attachmentAlias Attachment
	var raw struct {
		attachmentAlias
		Size interface{} `json:"size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = Attachment(raw.attachmentAlias)
	switch size := raw.Size.(type) {
	case nil:
		return nil
	case float64:
		a.Size = int64(size)
		return nil
	case string:
		if size == "" {
			return nil
		}
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			return fmt.Errorf("decode attachment size %q: %w", size, err)
		}
		a.Size = n
		return nil
	default:
		return fmt.Errorf("decode attachment size: unsupported JSON type %T", raw.Size)
	}
}

type CreateAttachmentRequest struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Content  []byte `json:"content"`
}

type Memo struct {
	Name        string       `json:"name,omitempty"`
	Content     string       `json:"content,omitempty"`
	Visibility  string       `json:"visibility,omitempty"`
	State       string       `json:"state,omitempty"`
	Pinned      bool         `json:"pinned,omitempty"`
	CreateTime  time.Time    `json:"create_time,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type CreateMemoRequest struct {
	Memo Memo `json:"memo"`
}

func (r CreateMemoRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Memo)
}

type UpdateMemoRequest struct {
	Memo Memo `json:"memo"`
}

func (r UpdateMemoRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Memo)
}

type FileCheck struct {
	Path        string `json:"path"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	OK          bool   `json:"ok"`
}
