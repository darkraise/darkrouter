// Package health holds the circuit breaker. State is authoritative in memory
// because the router consults it on every request; SQLite is a durable copy.
package health

import (
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// Key identifies a breaker entry. An empty Model is the credential-level entry,
// used for cooldowns that apply across every model a credential serves.
type Key struct {
	ProviderID string
	KeyID      string
	Model      string
}

// Signal is one observed outcome. StatusCode is carried separately from Outcome
// because 429 and 503 both classify as RetryableProvider but cool differently:
// a rate limit is precise and immediate, a 5xx needs trip_after failures first.
type Signal struct {
	Outcome       adapter.Outcome
	StatusCode    int
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// Entry is a persistable view of one breaker entry.
type Entry struct {
	Key                 Key
	CoolingUntil        time.Time
	BackoffLevel        int
	ConsecutiveFailures int
}

type state struct {
	coolingUntil        time.Time
	backoffLevel        int
	consecutiveFailures int

	// probing is set when a half-open probe has been admitted, so concurrent
	// callers at expiry see the candidate as unavailable instead of all
	// becoming probes.
	probing bool

	// retryAfterOnly marks a cooldown that came from a Retry-After header
	// rather than the ladder. Such a cooldown closes on expiry with no probe,
	// because nothing was ever tripped.
	retryAfterOnly bool
}

// ladder is the escalation sequence from master design §9. It continues
// doubling past the last entry until the configured maximum clamps it.
var ladder = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
	15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second,
}

func cooldownFor(level int, max time.Duration) time.Duration {
	if level < 0 {
		level = 0
	}
	if level < len(ladder) {
		if d := ladder[level]; d < max {
			return d
		}
		return max
	}
	d := ladder[len(ladder)-1]
	for i := len(ladder); i <= level; i++ {
		// Checked before doubling so a large level cannot overflow int64.
		if d >= max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		return max
	}
	return d
}

type Breaker struct {
	mu        sync.Mutex
	m         map[Key]*state
	lastUsed  map[CredKey]time.Time
	tripAfter int
	max       time.Duration

	// policy, when set, supplies trip_after and the maximum cooldown at the
	// moment they are applied. policy.cooldown is hot-editable, so a value
	// frozen at construction would hold until the next restart.
	policy func() (tripAfter int, max time.Duration)

	// now is swappable so tests can advance time without sleeping.
	now func() time.Time

	dirty bool
}

func New(tripAfter int, max time.Duration) *Breaker {
	tripAfter, max = clampPolicy(tripAfter, max)
	return &Breaker{
		m:         make(map[Key]*state),
		lastUsed:  make(map[CredKey]time.Time),
		tripAfter: tripAfter, max: max, now: time.Now,
	}
}

// Configure makes the breaker read its thresholds from source on every
// decision instead of the values New was given. The source is called under
// the breaker's lock and must be cheap and non-blocking: an atomic read of
// the current config snapshot, not a query.
func (b *Breaker) Configure(source func() (tripAfter int, max time.Duration)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.policy = source
}

func clampPolicy(tripAfter int, max time.Duration) (int, time.Duration) {
	if tripAfter < 1 {
		tripAfter = 1
	}
	if max <= 0 {
		max = 15 * time.Minute
	}
	return tripAfter, max
}

// policyLocked returns the thresholds in force right now.
func (b *Breaker) policyLocked() (int, time.Duration) {
	if b.policy == nil {
		return b.tripAfter, b.max
	}
	return clampPolicy(b.policy())
}

// Available reports whether the triple may be attempted now, and performs the
// half-open claim itself: at expiry of a ladder cooldown exactly one caller is
// admitted as the probe.
func (b *Breaker) Available(k Key) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// The credential-level entry gates every model the credential serves, so it
	// is checked first and independently.
	if k.Model != "" {
		if !b.availableLocked(Key{ProviderID: k.ProviderID, KeyID: k.KeyID}) {
			return false
		}
	}
	return b.availableLocked(k)
}

func (b *Breaker) availableLocked(k Key) bool {
	st, ok := b.m[k]
	if !ok || st.coolingUntil.IsZero() {
		return true
	}
	now := b.now()
	if now.Before(st.coolingUntil) {
		return false
	}

	// Expired.
	if st.retryAfterOnly {
		// No ladder was tripped, so there is nothing to probe: reopen fully.
		st.coolingUntil = time.Time{}
		st.retryAfterOnly = false
		b.dirty = true
		return true
	}
	if st.probing {
		return false
	}
	st.probing = true
	return true
}

// Record applies one outcome. It is the only place breaker state changes, so
// the rules from spec §7.1 live here and nowhere else.
//
// Every outcome releases the half-open probe on both entries Available can
// claim it on. An outcome that leaves the ladder alone still has to do this:
// the probe is the one admitted caller, and if its attempt ends without
// touching the entry nothing else ever will, and the entry stays shut.
func (b *Breaker) Record(k Key, s Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	ck := Key{ProviderID: k.ProviderID, KeyID: k.KeyID}

	switch s.Outcome {
	case adapter.OutcomeClientCancelled:
		// Never counts against any provider. Marking a provider unhealthy
		// because someone pressed Ctrl-C is a self-inflicted outage.
		b.releaseProbeLocked(k)
		b.releaseProbeLocked(ck)
		return

	case adapter.OutcomeRetryableModel:
		// The provider is reachable and the credential is fine; only this model
		// is wrong. Phase 6 counts these per target to surface a permanently
		// misconfigured base URL.
		b.releaseProbeLocked(k)
		b.releaseProbeLocked(ck)
		return

	case adapter.OutcomeSuccess:
		// Proves the provider is reachable and the credential works, so both
		// entries close fully.
		b.resetLocked(k)
		b.resetLocked(ck)
		return

	case adapter.OutcomeFatal:
		// Proves the provider is reachable; a 400 says something about the
		// request, not about the provider. It says nothing about the credential
		// either way, so a cooling credential keeps its ladder and only gives
		// back the probe.
		b.resetLocked(k)
		b.releaseProbeLocked(ck)
		return

	case adapter.OutcomeRetryableCredential:
		// Cools the credential across every model it serves. This never resets
		// any ladder: a billing-exhausted key must not be resurrected by a
		// client's malformed request.
		b.releaseProbeLocked(k)
		st := b.getLocked(ck)
		b.coolLocked(st, now, st.backoffLevel)
		return

	case adapter.OutcomeRetryableProvider:
		b.releaseProbeLocked(ck)
		st := b.getLocked(k)
		tripAfter, max := b.policyLocked()
		if s.HasRetryAfter {
			// The provider said how long it needs, on a 429 or a 503 alike.
			// That is more precise than the ladder and does not trip it.
			d := s.RetryAfter
			if d > max {
				d = max
			}
			st.coolingUntil = now.Add(d)
			st.retryAfterOnly = true
			st.probing = false
			b.dirty = true
			return
		}
		if s.StatusCode == 429 {
			b.coolLocked(st, now, st.backoffLevel)
			return
		}
		// Everything else retryable: a single failure must not cool.
		st.consecutiveFailures++
		st.probing = false
		b.dirty = true
		if st.consecutiveFailures >= tripAfter {
			b.coolLocked(st, now, st.backoffLevel)
		}
		return
	}
}

// releaseProbeLocked gives back a half-open claim without changing anything
// else, so the next caller after expiry becomes the probe instead.
func (b *Breaker) releaseProbeLocked(k Key) {
	if st, ok := b.m[k]; ok && st.probing {
		st.probing = false
		b.dirty = true
	}
}

// resetLocked forgets the entry entirely: ladder, counters and probe.
func (b *Breaker) resetLocked(k Key) {
	if _, ok := b.m[k]; ok {
		delete(b.m, k)
		b.dirty = true
	}
}

func (b *Breaker) getLocked(k Key) *state {
	st, ok := b.m[k]
	if !ok {
		st = &state{}
		b.m[k] = st
	}
	return st
}

// coolLocked cools at the given level and advances the ladder, so the next trip
// escalates. consecutiveFailures is deliberately not reset: it is what makes a
// probe failure re-trip immediately at the next level.
func (b *Breaker) coolLocked(st *state, now time.Time, level int) {
	_, max := b.policyLocked()
	st.coolingUntil = now.Add(cooldownFor(level, max))
	st.backoffLevel = level + 1
	st.retryAfterOnly = false
	st.probing = false
	b.dirty = true
}

// Snapshot returns every entry for the persister. It is a copy: the caller
// writes it to SQLite without holding the lock.
func (b *Breaker) Snapshot() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, 0, len(b.m))
	for k, st := range b.m {
		out = append(out, Entry{
			Key: k, CoolingUntil: st.coolingUntil,
			BackoffLevel: st.backoffLevel, ConsecutiveFailures: st.consecutiveFailures,
		})
	}
	return out
}

// Rehydrate restores state at startup. Entries whose cooldown has passed are
// reopened, but their failure counters are retained: a provider that was
// flapping before a restart must not get a clean slate.
func (b *Breaker) Rehydrate(entries []Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for _, e := range entries {
		st := &state{
			backoffLevel:        e.BackoffLevel,
			consecutiveFailures: e.ConsecutiveFailures,
		}
		if !e.CoolingUntil.IsZero() && now.Before(e.CoolingUntil) {
			st.coolingUntil = e.CoolingUntil
		}
		b.m[e.Key] = st
	}
}

// TakeDirty reports whether state changed since the last call and clears the
// flag, so the persister writes only when there is something to write.
func (b *Breaker) TakeDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.dirty
	b.dirty = false
	return d
}
