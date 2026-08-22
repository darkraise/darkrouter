package health

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestZeroAvailabilityAdmitsEverything(t *testing.T) {
	var a Availability
	if !a.Available(triple) {
		t.Fatal("a zero Availability must fail open")
	}
}

func TestSnapshotReportsACoolingTriple(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}

	a := b.SnapshotAvailability(*now)
	if a.Available(triple) {
		t.Fatal("a cooling triple must be reported unavailable")
	}
	// A different model on the same credential is unaffected by a triple-level
	// cooldown.
	other := Key{ProviderID: "groq", KeyID: "k1", Model: "other"}
	if !a.Available(other) {
		t.Fatal("a triple cooldown must not cool the credential's other models")
	}
}

func TestSnapshotCredentialCooldownCoolsEveryModel(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402})

	a := b.SnapshotAvailability(*now)
	for _, m := range []string{"m", "other", "third"} {
		k := Key{ProviderID: "groq", KeyID: "k1", Model: m}
		if a.Available(k) {
			t.Errorf("model %q available despite a credential-level cooldown", m)
		}
	}
	// A different credential on the same provider is untouched.
	if !a.Available(Key{ProviderID: "groq", KeyID: "k2", Model: "m"}) {
		t.Error("a credential cooldown must not cool the provider's other credentials")
	}
}

func TestSnapshotAtALaterInstantSeesTheCooldownExpired(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b.SnapshotAvailability(*now).Available(triple) {
		t.Fatal("should be cooling at the trip instant")
	}
	// The instant is an input, so the same breaker answers differently for a
	// later evaluation time. This is what makes the router's time-dependent
	// cases testable without sleeping.
	later := now.Add(2 * time.Second)
	if !b.SnapshotAvailability(later).Available(triple) {
		t.Fatal("the level-0 cooldown of 1s should have expired by +2s")
	}
}

// The router must not burn the single half-open probe just by looking.
func TestSnapshotDoesNotClaimTheHalfOpenProbe(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)

	for i := 0; i < 10; i++ {
		b.SnapshotAvailability(*now)
	}
	// The live path must still admit exactly one probe.
	if !b.Available(triple) {
		t.Fatal("snapshotting consumed the half-open probe")
	}
	if b.Available(triple) {
		t.Fatal("a second live caller was admitted; the claim is broken")
	}
}
