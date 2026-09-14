package server

import (
	"net/http"
	"time"
)

// writeDeadlines bounds how long any single write to a client may block.
//
// Neither listener sets WriteTimeout, because it would cut a long stream at a
// fixed age. Without some bound, though, a client that stops reading fills its
// socket and the handler's next write blocks for good: cancelling the request
// context does not unblock a write, so the handler, its upstream connection
// and its buffers stay pinned until shutdown. Each write therefore renews a
// deadline of idle from now, so a stream lasts as long as the client keeps
// taking bytes, and a write stuck for idle fails. A failed write also cancels
// the request context, which ends the upstream read behind it.
//
// idle is policy.timeout.idle, read per request: the bound the gateway already
// puts on a silent provider is the bound it puts on a silent client.
func writeDeadlines(next http.Handler, idle func() time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := idle()
		if d <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		dw := &deadlineWriter{ResponseWriter: w, rc: http.NewResponseController(w), idle: d}
		next.ServeHTTP(dw, r)
		// net/http flushes whatever the handler left buffered after it
		// returns, and clears the deadline once that is done.
		dw.extend()
	})
}

type deadlineWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	idle time.Duration
}

func (d *deadlineWriter) extend() {
	// ErrNotSupported from a writer with no connection behind it, such as a
	// test recorder, leaves nothing to bound.
	_ = d.rc.SetWriteDeadline(time.Now().Add(d.idle))
}

func (d *deadlineWriter) WriteHeader(code int) {
	d.extend()
	d.ResponseWriter.WriteHeader(code)
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	d.extend()
	return d.ResponseWriter.Write(p)
}

// Flush is implemented directly because streaming callers assert
// http.Flusher on the writer they are handed.
func (d *deadlineWriter) Flush() {
	d.extend()
	_ = d.rc.Flush()
}

func (d *deadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
