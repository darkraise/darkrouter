package exec

import (
	"bytes"
	"errors"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// ErrEventTooLong means a provider opened an event and never terminated it
// within the configured line cap. It is an attempt failure, not a client error.
var ErrEventTooLong = errors.New("exec: forwarded event exceeds the line cap")

// eventSplitter cuts a forwarded byte stream into whole SSE events, handing
// back the exact bytes that arrived for each one.
//
// The fast path forwards at event granularity rather than chunk granularity
// because spec §5.2's usage-chunk strip is impossible otherwise. Nothing
// observable is lost: providers flush per event, and an SSE client cannot see
// how a stream was chunked. What is preserved is each event's bytes, exactly.
type eventSplitter struct {
	buf []byte
	max int
}

// push appends a chunk and returns every event it completed.
func (s *eventSplitter) push(chunk []byte) ([][]byte, error) {
	s.buf = append(s.buf, chunk...)
	var out [][]byte
	for {
		end := eventEnd(s.buf)
		if end < 0 {
			break
		}
		ev := make([]byte, end)
		copy(ev, s.buf[:end])
		out = append(out, ev)
		s.buf = s.buf[end:]
	}
	if s.max > 0 && len(s.buf) > s.max {
		return out, ErrEventTooLong
	}
	return out, nil
}

// flush returns the unterminated tail and empties the carry. A provider that
// ends without a trailing blank line still owes the client those bytes.
func (s *eventSplitter) flush() []byte {
	out := s.buf
	s.buf = nil
	return out
}

// eventEnd returns the index just past the first event boundary in b, or -1.
//
// The SSE grammar permits LF, CRLF and a lone CR as a line terminator, and a
// boundary is two in a row. Providers differ, so all three combinations have to
// be recognized rather than the one a given upstream happens to send.
func eventEnd(b []byte) int {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			if i+1 < len(b) {
				if b[i+1] == '\n' {
					return i + 2
				}
				if b[i+1] == '\r' {
					if i+2 < len(b) && b[i+2] == '\n' {
						return i + 3
					}
					return i + 2
				}
			}
		case '\r':
			if i+1 < len(b) && b[i+1] == '\n' {
				// CRLF: the boundary is this pair followed by another.
				if i+2 < len(b) {
					if b[i+2] == '\n' {
						return i + 3
					}
					if b[i+2] == '\r' {
						if i+3 < len(b) && b[i+3] == '\n' {
							return i + 4
						}
						return i + 3
					}
				}
				i++ // the LF is consumed by this CRLF
				continue
			}
			if i+1 < len(b) && b[i+1] == '\r' {
				return i + 2
			}
		}
	}
	return -1
}

// parseEvent reads one whole event's bytes back into its fields, reusing the
// same reader the IR path uses so the two cannot disagree about the grammar.
// It reports false for a comment or an empty block, which dispatch nothing.
func parseEvent(raw []byte, maxLine int) (sse.Event, bool) {
	ev, err := sse.NewReader(bytes.NewReader(raw), maxLine).Next()
	if err != nil {
		// Both outcomes are the same answer here. io.EOF means the block
		// dispatched nothing — a comment or blank lines — and any other error
		// means a malformed event, which spec §6 says to stop recognizing and
		// simply forward. Neither is a reason to end the stream.
		return sse.Event{}, false
	}
	return ev, true
}

// mergeUsage folds one event's usage into the running total.
//
// Merged rather than assigned because Anthropic splits usage across
// message_start and message_delta: assigning the second would erase the input
// and cache counts and compute a wrong cost on every cached or long-prompt
// request. A later zero never erases a known count, which is also what makes
// Gemini's cumulative usageMetadata safe to fold repeatedly.
func mergeUsage(dst *ir.Usage, u *ir.Usage) {
	if u == nil {
		return
	}
	if u.InputTokens > 0 {
		dst.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		dst.OutputTokens = u.OutputTokens
	}
	if u.CacheReadTokens > 0 {
		dst.CacheReadTokens = u.CacheReadTokens
	}
	if u.CacheWriteTokens > 0 {
		dst.CacheWriteTokens = u.CacheWriteTokens
	}
	if u.ReasoningTokens > 0 {
		dst.ReasoningTokens = u.ReasoningTokens
	}
}
