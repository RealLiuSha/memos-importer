package importer

import "fmt"

const (
	StrategySkip      = "skip"
	StrategyOverwrite = "overwrite"

	VisibilityPrivate   = "PRIVATE"
	VisibilityProtected = "PROTECTED"
	VisibilityPublic    = "PUBLIC"
)

type Options struct {
	Strategy           string `json:"strategy"`
	TimeSource         string `json:"time_source"`
	Visibility         string `json:"visibility"`
	WorkerCount        int    `json:"worker_count"`
	ContentLengthLimit int64  `json:"content_length_limit"`
	MaxAttachmentBytes int64  `json:"max_attachment_bytes"`
}

func (o Options) Normalized() Options {
	if o.Strategy == "" {
		o.Strategy = StrategySkip
	}
	if o.Visibility == "" {
		o.Visibility = VisibilityPrivate
	}
	if o.WorkerCount <= 0 {
		o.WorkerCount = 4
	}
	if o.WorkerCount > 16 {
		o.WorkerCount = 16
	}
	if o.MaxAttachmentBytes <= 0 {
		o.MaxAttachmentBytes = 32 << 20
	}
	return o
}

func (o Options) Validate() error {
	o = o.Normalized()
	switch o.Strategy {
	case StrategySkip, StrategyOverwrite:
	default:
		return fmt.Errorf("invalid strategy %q: must be %q or %q", o.Strategy, StrategySkip, StrategyOverwrite)
	}
	switch o.Visibility {
	case VisibilityPrivate, VisibilityProtected, VisibilityPublic:
	default:
		return fmt.Errorf("invalid visibility %q: must be %q, %q, or %q", o.Visibility, VisibilityPrivate, VisibilityProtected, VisibilityPublic)
	}
	return nil
}
