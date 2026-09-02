package health

import (
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func newTestBreaker(t *testing.T) (*Breaker, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := New(3, 15*time.Minute)
	b.now = func() time.Time { return now }
	return b, &now
}

var triple = Key{ProviderID: "groq", KeyID: "k1", Model: "m"}

func TestBreakerStartsAvailable(t *testing.T) {
	b, _ := newTestBreaker(t)
	if !b.Available(triple) {
		t.Fatal("an unseen triple must be available")
	}
}

// The spec is explicit: a single 5xx does not cool a candidate.
func TestSingleRetryableFailureDoesNotCool(t *testing.T) {
	b, _ := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.Available(triple) {
		t.Fatal("one 5xx cooled the triple; trip_after is 3")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.Available(triple) {
		t.Fatal("two 5xx cooled the triple; trip_after is 3")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if b.Available(triple) {
		t.Fatal("three consecutive 5xx must cool the triple")
	}
}

func TestOutcomeTable(t *testing.T) {
	cases := []struct {
		name     string
		signals  []Signal
		wantCool bool
		checkKey Key
	}{
		{
			name:     "429 cools immediately without waiting for trip_after",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429}},
			wantCool: true, checkKey: triple,
		},
		{
			name: "success resets the ladder",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeSuccess, StatusCode: 200},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name: "fatal resets the ladder too",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeFatal, StatusCode: 400},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name:     "401 cools the credential across every model",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 401}},
			wantCool: true, checkKey: Key{ProviderID: "groq", KeyID: "k1", Model: "other-model"},
		},
		{
			name: "a 400 after a 402 leaves the credential cooling",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402},
				{Outcome: adapter.OutcomeFatal, StatusCode: 400},
			},
			wantCool: true, checkKey: triple,
		},
		{
			name:     "client cancellation touches nothing",
			signals:  []Signal{{Outcome: adapter.OutcomeClientCancelled}},
			wantCool: false, checkKey: triple,
		},
		{
			name: "three cancellations still touch nothing",
			signals: []Signal{
				{Outcome: adapter.OutcomeClientCancelled},
				{Outcome: adapter.OutcomeClientCancelled},
				{Outcome: adapter.OutcomeClientCancelled},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name:     "an unknown model does not penalize the provider",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableModel, StatusCode: 404}},
			wantCool: false, checkKey: triple,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newTestBreaker(t)
			for _, s := range tc.signals {
				b.Record(triple, s)
			}
			if got := !b.Available(tc.checkKey); got != tc.wantCool {
				t.Errorf("cooling = %v, want %v", got, tc.wantCool)
			}
		})
	}
}

func TestRetryAfterIsHonouredAndClamped(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{
		Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429,
		RetryAfter: 24 * time.Hour, HasRetryAfter: true,
	})
	if b.Available(triple) {
		t.Fatal("a 429 with Retry-After must cool the triple")
	}
	// Clamped to policy.cooldown.max. Without the clamp a provider sending
	// Retry-After: 86400 removes itself for a day.
	*now = now.Add(15*time.Minute + time.Second)
	if !b.Available(triple) {
		t.Fatal("Retry-After was not clamped to 15m")
	}
}

// A Retry-After cooldown never tripped a ladder, so it closes on expiry with no
// probe: the very next caller is admitted.
func TestRetryAfterCooldownClosesWithoutAProbe(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{
		Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429,
		RetryAfter: 10 * time.Second, HasRetryAfter: true,
	})
	*now = now.Add(11 * time.Second)
	if !b.Available(triple) {
		t.Fatal("first caller after expiry was refused")
	}
	if !b.Available(triple) {
		t.Fatal("second caller was refused; a Retry-After expiry admits everyone")
	}
}

func TestLadderEscalatesAndClamps(t *testing.T) {
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second,
		240 * time.Second, 480 * time.Second, 900 * time.Second,
		900 * time.Second,
	}
	for level, w := range want {
		if got := cooldownFor(level, 15*time.Minute); got != w {
			t.Errorf("level %d: cooldown = %s, want %s", level, got, w)
		}
	}
}

func TestLadderDoesNotOverflowAtHighLevels(t *testing.T) {
	if got := cooldownFor(500, 15*time.Minute); got != 15*time.Minute {
		t.Errorf("cooldown at level 500 = %s, want the clamp", got)
	}
}

func TestHalfOpenAdmitsExactlyOneProbeUnderConcurrency(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b.Available(triple) {
		t.Fatal("the triple should be cooling")
	}
	*now = now.Add(2 * time.Second) // past the level-0 cooldown of 1s

	const goroutines = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Available(triple) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if admitted != 1 {
		t.Fatalf("%d probes admitted at expiry, want exactly 1", admitted)
	}
}

func TestProbeSuccessClosesTheBreaker(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("no probe was admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeSuccess, StatusCode: 200})
	if !b.Available(triple) {
		t.Fatal("a successful probe must close the breaker")
	}
	if !b.Available(triple) {
		t.Fatal("the breaker did not stay closed")
	}
}

func TestProbeFailureReTripsAtTheNextLadderLevel(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("no probe was admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	// Level 1 is 2s: still cooling at +1s, open again at +3s.
	*now = now.Add(time.Second)
	if b.Available(triple) {
		t.Fatal("the breaker re-opened before the level-1 cooldown elapsed")
	}
	*now = now.Add(3 * time.Second)
	if !b.Available(triple) {
		t.Fatal("the level-1 cooldown never expired")
	}
}

func TestSnapshotAndRehydrateRetainFailureCounts(t *testing.T) {
	b, _ := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	snap := b.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// A restart must not hand a flapping provider a clean slate.
	restored := New(3, 15*time.Minute)
	restored.Rehydrate(snap)
	restored.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if restored.Available(triple) {
		t.Fatal("the third failure after a restart should have cooled the triple")
	}
}

func TestRehydrateDropsExpiredCooldowns(t *testing.T) {
	b, _ := newTestBreaker(t)
	// Relative to the breaker's clock, not the wall clock: mixing the two makes
	// the test depend on the date it runs.
	past := b.now().Add(-time.Hour)
	b.Rehydrate([]Entry{{
		Key: triple, CoolingUntil: past, BackoffLevel: 4, ConsecutiveFailures: 7,
	}})
	if !b.Available(triple) {
		t.Fatal("an expired cooldown must not survive rehydration")
	}
	snap := b.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 7 {
		t.Fatalf("failure count must be retained across rehydration, got %+v", snap)
	}
}

func TestTakeDirtyReportsAndClears(t *testing.T) {
	b, _ := newTestBreaker(t)
	if b.TakeDirty() {
		t.Fatal("a fresh breaker must not be dirty")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.TakeDirty() {
		t.Fatal("recording a failure must mark the breaker dirty")
	}
	if b.TakeDirty() {
		t.Fatal("TakeDirty must clear the flag")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in     string
		want   time.Duration
		wantOK bool
	}{
		{"", 0, false},
		{"30", 30 * time.Second, true},
		{"  30  ", 30 * time.Second, true},
		{"0", 0, true},
		{"-5", 0, false},
		{"nonsense", 0, false},
		{"Sat, 22 Aug 2026 12:01:00 GMT", time.Minute, true},
		// A date already in the past means "retry now", not a negative wait.
		{"Sat, 22 Aug 2026 11:59:00 GMT", 0, true},
	}
	for _, tc := range cases {
		got, ok := ParseRetryAfter(tc.in, now)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("ParseRetryAfter(%q) = %s, %v; want %s, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// tripAndExpire cools the triple through the ladder and moves the clock past
// the cooldown, so the next Available call is the half-open probe claim.
func tripAndExpire(t *testing.T, b *Breaker, now *time.Time, k Key) {
	t.Helper()
	for i := 0; i < 3; i++ {
		b.Record(k, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b.Available(k) {
		t.Fatal("three 5xx must cool the triple")
	}
	*now = now.Add(2 * time.Second)
	if !b.Available(k) {
		t.Fatal("the first caller after expiry must be admitted as the probe")
	}
	if b.Available(k) {
		t.Fatal("a second caller must wait while the probe is out")
	}
}

// A probe whose attempt never reached the provider must not leave the entry
// half-open forever: every outcome the attempt can end in releases it.
func TestProbeIsReleasedByOutcomesThatDoNotChangeTheLadder(t *testing.T) {
	for _, o := range []adapter.Outcome{adapter.OutcomeClientCancelled, adapter.OutcomeRetryableModel} {
		t.Run(string(o), func(t *testing.T) {
			b, now := newTestBreaker(t)
			tripAndExpire(t, b, now, triple)
			b.Record(triple, Signal{Outcome: o})
			if !b.Available(triple) {
				t.Fatalf("%s left the probe claimed; the triple is unavailable forever", o)
			}
		})
	}
}

func TestProbeIsReleasedByACredentialFailure(t *testing.T) {
	b, now := newTestBreaker(t)
	tripAndExpire(t, b, now, triple)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 401})
	if b.Available(triple) {
		t.Fatal("a 401 must cool the credential")
	}
	// Past the credential cooldown (level 0: 1s). The model-level entry's own
	// cooldown expired earlier, and its probe must have been released by the
	// credential failure rather than still being out.
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("the model-level probe was never released after the credential failure")
	}
}

// The credential-level entry claims its own probe when a credential cooldown
// expires. A success on the model that probed it must release that claim too,
// or the credential stays half-open with nothing ever testing it again.
func TestASuccessfulProbeReleasesTheCredentialLevelClaim(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 401})
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("the first caller after the credential cooldown must be admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeSuccess, StatusCode: 200})
	if !b.Available(triple) {
		t.Fatal("a successful probe left the credential-level entry claimed")
	}
}

func TestAFatalProbeReleasesTheCredentialClaimWithoutResettingIt(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402})
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("the first caller after the credential cooldown must be admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeFatal, StatusCode: 400})
	if !b.Available(triple) {
		t.Fatal("a fatal probe left the credential-level entry claimed")
	}
	// The ladder was not reset: the next credential failure escalates.
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402})
	*now = now.Add(1500 * time.Millisecond)
	if b.Available(triple) {
		t.Fatal("the credential ladder was reset by a 400; level 1 is 2s")
	}
}

// Retry-After on a 503 or 529 is as precise an instruction as on a 429, and
// a provider that says how long it needs deserves exactly that long.
func TestRetryAfterOnAServerErrorCoolsWithoutTheLadder(t *testing.T) {
	for _, code := range []int{503, 529} {
		b, now := newTestBreaker(t)
		b.Record(triple, Signal{
			Outcome: adapter.OutcomeRetryableProvider, StatusCode: code,
			RetryAfter: 5 * time.Second, HasRetryAfter: true,
		})
		if b.Available(triple) {
			t.Fatalf("%d with Retry-After did not cool", code)
		}
		*now = now.Add(6 * time.Second)
		if !b.Available(triple) {
			t.Fatalf("%d cooldown outlived its Retry-After", code)
		}
		// No ladder was tripped, so there is no probe: a second caller is
		// admitted as well.
		if !b.Available(triple) {
			t.Fatalf("%d Retry-After cooldown left a probe behind", code)
		}
	}
}
