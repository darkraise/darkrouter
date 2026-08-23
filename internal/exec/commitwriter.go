package exec

import "net/http"

// CommitWriter makes Phase 3's commit rule observable rather than reported.
//
// The rule is that once the first byte reaches the client there is no
// re-route. Today the loop infers that from an outcome value, and the stream
// path returns OutcomeSuccess for a post-commit failure — conflating
// "committed" with "succeeded". That is survivable with one surface and not
// with seven: a binary surface reporting success after writing half a
// truncated body would be indistinguishable from one that finished.
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
	onCommit  []func()
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

// OnCommit registers a hook to run when the first byte goes out — the loop
// hangs the total-to-idle timeout switch and the diagnostics headers off it.
//
// A hook registered after the commit runs immediately, so registration order
// cannot decide whether it runs at all.
func (c *CommitWriter) OnCommit(fn func()) {
	if c.committed {
		fn()
		return
	}
	c.onCommit = append(c.onCommit, fn)
}

func (c *CommitWriter) commit() {
	if c.committed {
		return
	}
	c.committed = true
	for _, fn := range c.onCommit {
		fn()
	}
	c.onCommit = nil
}

func (c *CommitWriter) Header() http.Header { return c.w.Header() }

func (c *CommitWriter) WriteHeader(status int) {
	// A status line is as irrevocable as a body byte: the client has been told
	// this attempt is the answer.
	c.commit()
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
	n, err := c.w.Write(b)
	c.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer. SSE surfaces flush per event, and a
// wrapper that swallowed it would buffer every stream to completion.
func (c *CommitWriter) Flush() {
	c.commit()
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

var (
	_ http.ResponseWriter = (*CommitWriter)(nil)
	_ http.Flusher        = (*CommitWriter)(nil)
)
