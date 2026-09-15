package exec

import "net/http"

// CommitWriter makes the commit rule observable rather than reported.
//
// The rule is that once the first byte reaches the client there is no
// re-route. A surface's returned outcome cannot carry that: a stream that
// fails after commit is both "committed" and "failed", and a binary surface
// that wrote half a body is indistinguishable from one that finished.
//
// So the loop wraps the response writer and, after a surface returns, asks the
// wrapper rather than the surface. Detecting what counts as content-bearing
// stays with the surface — only it knows its wire format — but the record of
// whether anything actually went out belongs here.
//
// It is not safe for concurrent use. One request writes to one of these from
// one goroutine, which is what the handler contract already requires.
type CommitWriter struct {
	w         http.ResponseWriter
	committed bool
	bytes     int64
	err       error
	// hold is told when a write to the client starts and ends. A write that
	// blocks is waiting on the client, and whatever bounds the provider must
	// not count that wait against it.
	hold writeHold
}

type writeHold interface {
	beginWrite()
	endWrite()
}

func NewCommitWriter(w http.ResponseWriter) *CommitWriter {
	return &CommitWriter{w: w}
}

// Committed reports whether anything has reached the client. Once true it
// never returns false again.
func (c *CommitWriter) Committed() bool { return c.committed }

// Bytes is how many body bytes went out. Spec §7 requires this on the record:
// a truncated binary response cannot be signalled in-band, so the trace is the
// only place the truncation can appear.
func (c *CommitWriter) Bytes() int64 { return c.bytes }

// Err is the first write or flush to the client that failed. Once set, the
// client is not receiving the response, whatever the provider does next.
func (c *CommitWriter) Err() error { return c.err }

func (c *CommitWriter) commit() { c.committed = true }

func (c *CommitWriter) fail(err error) {
	if err != nil && c.err == nil {
		c.err = err
	}
}

func (c *CommitWriter) begin() {
	if c.hold != nil {
		c.hold.beginWrite()
	}
}

func (c *CommitWriter) end() {
	if c.hold != nil {
		c.hold.endWrite()
	}
}

func (c *CommitWriter) Header() http.Header { return c.w.Header() }

func (c *CommitWriter) WriteHeader(status int) {
	// A status line is as irrevocable as a body byte: the client has been told
	// this attempt is the answer.
	c.commit()
	c.begin()
	defer c.end()
	c.w.WriteHeader(status)
}

func (c *CommitWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		// A zero-length write reaches no client, so it must not end the chain.
		// net/http would send headers here, but a surface probing with an empty
		// write has not answered anything and keeps its failover.
		return 0, nil
	}
	c.commit()
	c.begin()
	defer c.end()
	n, err := c.w.Write(b)
	c.bytes += int64(n)
	c.fail(err)
	return n, err
}

// Flush forwards to the underlying writer. SSE surfaces flush per event, and a
// wrapper that swallowed it would buffer every stream to completion. A small
// event sits in the writer's buffer until the flush, so the flush is usually
// the write that blocks on a client that stopped reading, and its error is
// kept rather than dropped.
func (c *CommitWriter) Flush() {
	c.commit()
	c.begin()
	defer c.end()
	switch f := c.w.(type) {
	case interface{ FlushError() error }:
		c.fail(f.FlushError())
	case http.Flusher:
		f.Flush()
	}
}

var (
	_ http.ResponseWriter = (*CommitWriter)(nil)
	_ http.Flusher        = (*CommitWriter)(nil)
)
