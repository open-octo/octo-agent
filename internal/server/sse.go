package server

import (
	"fmt"
	"net/http"
)

// SSEWriter wraps an http.ResponseWriter to emit Server-Sent Events.
// It sets the required headers and provides a helper to send events
// with automatic client-disconnect detection via the request context.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	done    <-chan struct{}
	ctxErr  func() error
}

// NewSSEWriter creates an SSEWriter for the given response and request.
// It writes the SSE headers immediately and returns an error if the
// underlying ResponseWriter does not support flushing.
func NewSSEWriter(w http.ResponseWriter, r *http.Request) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &SSEWriter{
		w:       w,
		flusher: flusher,
		done:    r.Context().Done(),
		ctxErr:  r.Context().Err,
	}, nil
}

// WriteEvent sends a single SSE event. If the client has disconnected,
// it returns the context error.
func (s *SSEWriter) WriteEvent(eventType string, data string) error {
	select {
	case <-s.done:
		return s.ctxErr()
	default:
	}

	_, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, data)
	if err != nil {
		return fmt.Errorf("write sse event: %w", err)
	}
	s.flusher.Flush()
	return nil
}
