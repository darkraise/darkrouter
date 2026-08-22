package health

import "time"

// CredKey identifies a credential, without a model. Least-recently-used is a
// property of the credential rather than of a triple: rotating keys is about
// spreading load across quotas, and a quota is per key.
type CredKey struct {
	ProviderID string
	KeyID      string
}

// MarkUsed records a dispatch. It is called at attempt start, not on success:
// a credential that always fails would otherwise keep a stale timestamp and
// sort first forever.
//
// It shares the breaker's mutex because it is part of the same per-credential
// decision. A second lock would have to be ordered against the first by every
// caller, and one caller getting that order wrong is a deadlock.
func (b *Breaker) MarkUsed(ck CredKey, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastUsed == nil {
		b.lastUsed = make(map[CredKey]time.Time)
	}
	b.lastUsed[ck] = at
	b.dirty = true
}

// LastUsedSnapshot returns a copy for the router and the persister. A copy
// rather than the map itself: the router sorts over it without the lock, and a
// concurrent MarkUsed would otherwise be a data race.
func (b *Breaker) LastUsedSnapshot() map[CredKey]time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[CredKey]time.Time, len(b.lastUsed))
	for k, v := range b.lastUsed {
		out[k] = v
	}
	return out
}

// RehydrateLastUsed restores timestamps at startup so a restart does not put
// every credential back on an equal footing and pile the next burst onto one.
func (b *Breaker) RehydrateLastUsed(m map[CredKey]time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastUsed == nil {
		b.lastUsed = make(map[CredKey]time.Time, len(m))
	}
	for k, v := range m {
		b.lastUsed[k] = v
	}
}
