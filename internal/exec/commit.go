package exec

import (
	"errors"

	"github.com/darkraise/darkrouter/internal/ir"
)

// ErrPreCommitBufferFull means an attempt emitted more pre-commit payload than
// the cap allows. It is an attempt failure, not a client error: the provider is
// misbehaving and another one may not.
var ErrPreCommitBufferFull = errors.New("exec: pre-commit buffer exceeded")

// IsContentBearing reports whether an event commits the response.
//
// message_start, pings, keepalives, and role-only deltas do not: committing on
// a keepalive forfeits failover for nothing. Thinking does, because a reasoning
// model can legitimately think for a minute before its first text token, and
// holding the response open that long would blow the pre-commit budget.
func IsContentBearing(ev ir.StreamEvent) bool {
	if ev.Type != ir.EventContentDelta || ev.Delta == nil {
		return false
	}
	switch ev.Delta.Type {
	case ir.BlockText:
		return ev.Delta.Text != ""
	case ir.BlockThinking, ir.BlockRedactedThinking:
		return ev.Delta.Thinking != ""
	case ir.BlockToolUse:
		return ev.Delta.ToolInput != ""
	default:
		return false
	}
}

// preCommitBuffer holds the in-flight attempt's events until it commits.
//
// It is bounded in bytes as well as by first_byte in time: a provider can emit
// megabytes of payload inside sixty seconds, and the time bound alone would let
// it.
type preCommitBuffer struct {
	evs      []ir.StreamEvent
	bytes    int
	maxBytes int
}

// newPreCommitBuffer returns a buffer capped at maxBytes of payload. A
// non-positive cap means unbounded.
func newPreCommitBuffer(maxBytes int) *preCommitBuffer {
	return &preCommitBuffer{maxBytes: maxBytes}
}

func (b *preCommitBuffer) add(ev ir.StreamEvent) error {
	n := eventBytes(ev)
	if b.maxBytes > 0 && b.bytes+n > b.maxBytes {
		return ErrPreCommitBufferFull
	}
	b.bytes += n
	b.evs = append(b.evs, ev)
	return nil
}

// events returns the buffered sequence for replay. It is repeatable, but the
// caller must replay exactly once — twice would duplicate the client's stream.
func (b *preCommitBuffer) events() []ir.StreamEvent { return b.evs }

// eventOverhead charges every buffered event, not only its payload.
//
// Without it the cap is unreachable: the only fields carrying bytes are the
// ones that commit, so a flood of pings, message_starts, or usage deltas could
// buffer without bound and the byte cap would protect against nothing.
const eventOverhead = 64

func eventBytes(ev ir.StreamEvent) int {
	n := eventOverhead + len(ev.ID) + len(ev.Model)
	if ev.Delta != nil {
		n += len(ev.Delta.Text) + len(ev.Delta.Thinking) + len(ev.Delta.ToolInput) +
			len(ev.Delta.ToolID) + len(ev.Delta.ToolName)
	}
	return n
}
