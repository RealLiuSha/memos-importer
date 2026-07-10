package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"memos-importer/internal/redact"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = errors.New(http.StatusText(status))
	}
	writeJSON(w, status, errorResponse{Error: sanitizeError(err.Error())})
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func decodeOptionalJSON(r *http.Request, v interface{}) error {
	if err := decodeJSON(r, v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func sanitizeError(text string) string {
	return redact.Short(text, 512)
}
