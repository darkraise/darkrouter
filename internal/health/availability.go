package health

import "time"

// Availability is a frozen view of breaker state at one instant.
//
// The router filters on cooldowns, so it needs an answer rather than a live
// query: reading the breaker directly would mean reading a clock inside a
// function whose whole value is that it does not.
//
// The zero value reports everything available, so a caller that forgot to build
// one fails open rather than routing to nothing.
type Availability struct {
	unavailable map[Key]struct{}
}

// Available reports whether the triple was usable at the snapshot instant. The
// credential-level entry gates every model the credential serves, so it is
// consulted first.
func (a Availability) Available(k Key) bool {
	if len(a.unavailable) == 0 {
		return true
	}
	if k.Model != "" {
		if _, cooling := a.unavailable[Key{ProviderID: k.ProviderID, KeyID: k.KeyID}]; cooling {
			return false
		}
	}
	_, cooling := a.unavailable[k]
	return !cooling
}

// SnapshotAvailability freezes the breaker as of at.
//
// It deliberately does not go through Available: that method claims the
// half-open probe as a side effect, and burning the single probe on a candidate
// the router may never attempt would leave the breaker shut with nothing
// testing it.
func (b *Breaker) SnapshotAvailability(at time.Time) Availability {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out map[Key]struct{}
	for k, st := range b.m {
		if st.coolingUntil.IsZero() || !at.Before(st.coolingUntil) {
			continue
		}
		if out == nil {
			out = make(map[Key]struct{})
		}
		out[k] = struct{}{}
	}
	return Availability{unavailable: out}
}
