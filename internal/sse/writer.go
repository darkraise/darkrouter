package sse

import (
	"io"
	"net/http"
	"strings"
)

// Writer emits SSE events, flushing after each one. It deliberately does not
// wrap the ResponseWriter in a bufio.Writer: buffering here turns
// time-to-first-token into time-to-completion.
type Writer struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func NewWriter(w http.ResponseWriter) *Writer {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	return &Writer{w: w, rc: http.NewResponseController(w)}
}

// Send writes one event and flushes. An empty event name omits the event field.
func (s *Writer) Send(event, data string) error {
	var b strings.Builder
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if _, err := io.WriteString(s.w, b.String()); err != nil {
		return err
	}
	// A ResponseWriter that cannot flush is a programming error upstream, not a
	// runtime condition worth failing the stream over.
	_ = s.rc.Flush()
	return nil
}

func (s *Writer) SendDone() error { return s.Send("", Done) }
