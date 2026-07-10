package domain

type WarningSeverity string

const (
	WarningInfo  WarningSeverity = "info"
	WarningWarn  WarningSeverity = "warning"
	WarningError WarningSeverity = "error"
)

type Warning struct {
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	BlockID  string          `json:"block_id,omitempty"`
	Severity WarningSeverity `json:"severity"`
}

func NewWarning(code, message, blockID string) Warning {
	return Warning{
		Code:     code,
		Message:  message,
		BlockID:  blockID,
		Severity: WarningWarn,
	}
}
