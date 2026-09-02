package exec

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store"
)

// bodyCapture tees what a request carried in and what went back out, up to
// the configured cap, so the trace can show both. It is armed only when
// capture.bodies is on, and only for text-shaped bodies: a speech response or
// a multipart upload is never held, which is the property spec §5 asks for.
type bodyCapture struct {
	max      int64
	retain   time.Duration
	request  *cappedBuffer
	response *cappedBuffer
}

func newBodyCapture(cfg config.CaptureConfig) *bodyCapture {
	return &bodyCapture{
		max:      cfg.MaxBytes,
		retain:   cfg.Retention,
		request:  &cappedBuffer{max: cfg.MaxBytes},
		response: &cappedBuffer{max: cfg.MaxBytes},
	}
}

// arm wraps the inbound body and the response writer. The request body is
// only teed when its content type is one the capture can show; the response
// decides at WriteHeader time, once its own content type is known.
func (c *bodyCapture) arm(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if capturable(r.Header.Get("Content-Type")) && r.Body != nil {
		r.Body = &teeBody{ReadCloser: r.Body, buf: c.request}
	}
	return &captureWriter{ResponseWriter: w, buf: c.response}
}

// fill copies the captured text onto the record. Empty buffers leave the
// record untouched, so a request that captured nothing writes no bodies row.
func (c *bodyCapture) fill(rec *store.RequestRecord, start time.Time) {
	rec.RequestBody = c.request.text()
	rec.ResponseBody = c.response.text()
	if rec.RequestBody != "" || rec.ResponseBody != "" {
		rec.BodiesExpireAt = start.Add(c.retain)
	}
}

// capturable reports whether a body of this content type can be shown as text
// in the trace. JSON, form-encoded and text bodies can; audio, images and
// multipart uploads cannot, and are never captured.
func capturable(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case mt == "application/json", strings.HasSuffix(mt, "+json"):
		return true
	case mt == "text/event-stream", strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// cappedBuffer keeps the first max bytes and counts the rest, so a body larger
// than the cap is recorded as truncated rather than partially and silently.
type cappedBuffer struct {
	max       int64
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) add(p []byte) {
	room := b.max - int64(b.buf.Len())
	if room <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return
	}
	if int64(len(p)) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return
	}
	b.buf.Write(p)
}

func (b *cappedBuffer) text() string {
	if b.buf.Len() == 0 {
		return ""
	}
	s := strings.ToValidUTF8(b.buf.String(), "�")
	if b.truncated {
		s += "\n… truncated at capture.max_bytes"
	}
	return s
}

type teeBody struct {
	io.ReadCloser
	buf *cappedBuffer
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		t.buf.add(p[:n])
	}
	return n, err
}

// captureWriter tees the response body once the headers say it is text. It
// forwards Flush so streaming keeps its per-event delivery, and Unwrap so
// http.ResponseController still reaches the connection underneath.
type captureWriter struct {
	http.ResponseWriter
	buf     *cappedBuffer
	decided bool
	keep    bool
}

func (c *captureWriter) decide() {
	if c.decided {
		return
	}
	c.decided = true
	c.keep = capturable(c.Header().Get("Content-Type"))
}

func (c *captureWriter) WriteHeader(status int) {
	c.decide()
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.decide()
	if c.keep {
		c.buf.add(p)
	}
	return c.ResponseWriter.Write(p)
}

func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

var (
	_ http.Flusher = (*captureWriter)(nil)
)
