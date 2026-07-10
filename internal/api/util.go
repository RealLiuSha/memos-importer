package api

type errString string

func (e errString) Error() string { return string(e) }

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeError(err.Error())
}
