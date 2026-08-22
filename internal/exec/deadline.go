package exec

import (
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// budget tracks what remains of policy.timeout.total and decides whether
// another attempt is worth starting.
//
// It takes instants rather than reading a clock so the gate is testable without
// sleeping, the same reason the router takes its evaluation instant as input.
type budget struct {
	deadline time.Time
	perTry   time.Duration
}

func newBudget(t config.TimeoutConfig, start time.Time) budget {
	return budget{
		deadline: start.Add(t.Total),
		// Pre-commit, an attempt needs to connect and then receive headers.
		perTry: t.Connect + t.FirstByte,
	}
}

func (b budget) remaining(now time.Time) time.Duration {
	d := b.deadline.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// canStartAttempt reports whether enough budget remains for an attempt to
// possibly complete. Starting one that cannot wastes the budget and the
// provider's quota, and replaces a clear attempts-exhausted error with a bare
// timeout.
func (b budget) canStartAttempt(now time.Time) bool {
	return b.remaining(now) >= b.perTry
}

// attemptDeadline is the earlier of connect+first_byte and the remaining total.
func (b budget) attemptDeadline(now time.Time) time.Time {
	byTry := now.Add(b.perTry)
	if byTry.After(b.deadline) {
		return b.deadline
	}
	return byTry
}
