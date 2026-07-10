package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"memos-importer/internal/importer"
)

func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errString("streaming unsupported"))
		return
	}
	events, cancel := s.broker.Subscribe(jobID)
	defer cancel()
	fmt.Fprintf(w, "event: ready\ndata: {\"job_id\":%q}\n\n", jobID)
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, importer.MarshalEvent(event))
			flusher.Flush()
		}
	}
}
