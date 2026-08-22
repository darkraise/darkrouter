package exec

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

func timeouts() config.TimeoutConfig {
	return config.TimeoutConfig{
		Connect: 10 * time.Second, FirstByte: 60 * time.Second,
		Total: 10 * time.Minute, Idle: 120 * time.Second,
	}
}

func TestBudgetRemainingCountsDownFromTotal(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)
	if got := b.remaining(start); got != 10*time.Minute {
		t.Errorf("remaining at start = %s, want 10m", got)
	}
	if got := b.remaining(start.Add(4 * time.Minute)); got != 6*time.Minute {
		t.Errorf("remaining = %s, want 6m", got)
	}
	if got := b.remaining(start.Add(11 * time.Minute)); got != 0 {
		t.Errorf("remaining past total = %s, want 0", got)
	}
}

// An attempt that cannot possibly complete must not be started.
func TestBudgetGateRefusesAnAttemptThatCannotFinish(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)

	// 10m total, 70s needed per attempt.
	if !b.canStartAttempt(start.Add(8 * time.Minute)) {
		t.Error("2m remaining is more than connect+first_byte; the attempt should start")
	}
	if b.canStartAttempt(start.Add(9*time.Minute + 1*time.Second)) {
		t.Error("59s remaining is less than connect+first_byte; the attempt must be refused")
	}
	if b.canStartAttempt(start.Add(10 * time.Minute)) {
		t.Error("no budget left; the attempt must be refused")
	}
}

// The per-attempt deadline is the smaller of connect+first_byte and whatever
// remains, so a long-running request cannot overrun its total.
func TestAttemptDeadlineIsBoundedByBothLimits(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)

	if got := b.attemptDeadline(start); !got.Equal(start.Add(70 * time.Second)) {
		t.Errorf("deadline = %s, want start+70s", got)
	}
	at := start.Add(8*time.Minute + 30*time.Second)
	if got := b.attemptDeadline(at); !got.Equal(at.Add(70 * time.Second)) {
		t.Errorf("deadline = %s, want +70s", got)
	}
	// Only once less than connect+first_byte remains does the total deadline
	// win. At 9m55s elapsed there are 5s left, so the attempt would be clamped
	// rather than allowed to run 70s past the budget. canStartAttempt refuses
	// this case anyway; the clamp is the second line of defence.
	at = start.Add(9*time.Minute + 55*time.Second)
	if got := b.attemptDeadline(at); !got.Equal(start.Add(10 * time.Minute)) {
		t.Errorf("deadline = %s, want the total deadline", got)
	}
}

func TestBudgetHandlesAZeroTotal(t *testing.T) {
	tc := timeouts()
	tc.Total = 0 // config defaults prevent this, but the type must not divide by it
	b := newBudget(tc, time.Now())
	if b.canStartAttempt(time.Now()) {
		t.Error("a zero total must refuse every attempt rather than allowing all")
	}
}
