package notion

import (
	"fmt"
	"strings"
)

const (
	TimeSourceCreated    = "created_time"
	TimeSourceLastEdited = "last_edited_time"
	timeSourceProperty   = "property:"
)

func NormalizeTimeSource(timeSource string) (string, error) {
	timeSource = strings.TrimSpace(timeSource)
	if timeSource == "" {
		return TimeSourceCreated, nil
	}
	switch timeSource {
	case TimeSourceCreated, TimeSourceLastEdited:
		return timeSource, nil
	}
	if strings.HasPrefix(timeSource, timeSourceProperty) {
		name := strings.TrimSpace(strings.TrimPrefix(timeSource, timeSourceProperty))
		if name != "" {
			return timeSourceProperty + name, nil
		}
	}
	return "", fmt.Errorf("invalid notion time_source %q: must be %q, %q, or %q followed by a date property name", timeSource, TimeSourceCreated, TimeSourceLastEdited, timeSourceProperty)
}
