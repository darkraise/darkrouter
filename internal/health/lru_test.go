package health

import (
	"sync"
	"testing"
	"time"
)

func TestMarkUsedRecordsTheInstant(t *testing.T) {
	b, now := newTestBreaker(t)
	ck := CredKey{ProviderID: "groq", KeyID: "k1"}

	if got := b.LastUsedSnapshot(); len(got) != 0 {
		t.Fatalf("a fresh breaker should have no usage, got %v", got)
	}
	b.MarkUsed(ck, *now)

	snap := b.LastUsedSnapshot()
	if !snap[ck].Equal(*now) {
		t.Errorf("LastUsed = %s, want %s", snap[ck], *now)
	}
}

func TestLastUsedSnapshotIsACopy(t *testing.T) {
	b, now := newTestBreaker(t)
	ck := CredKey{ProviderID: "groq", KeyID: "k1"}
	b.MarkUsed(ck, *now)

	snap := b.LastUsedSnapshot()
	delete(snap, ck)
	// Mutating the returned map must not corrupt the breaker's own state.
	if len(b.LastUsedSnapshot()) != 1 {
		t.Fatal("LastUsedSnapshot handed out its internal map")
	}
}

func TestRehydrateLastUsedRestoresOrderingAcrossARestart(t *testing.T) {
	b, now := newTestBreaker(t)
	a := CredKey{ProviderID: "groq", KeyID: "k1"}
	c := CredKey{ProviderID: "groq", KeyID: "k2"}
	b.RehydrateLastUsed(map[CredKey]time.Time{
		a: now.Add(-time.Hour),
		c: now.Add(-time.Minute),
	})
	snap := b.LastUsedSnapshot()
	if !snap[a].Before(snap[c]) {
		t.Fatal("restored ordering was lost; a restart would pile onto one key")
	}
}

func TestMarkUsedIsSafeUnderConcurrency(t *testing.T) {
	b, now := newTestBreaker(t)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.MarkUsed(CredKey{ProviderID: "groq", KeyID: "k1"}, now.Add(time.Duration(i)))
			_ = b.LastUsedSnapshot()
		}(i)
	}
	wg.Wait()
	if len(b.LastUsedSnapshot()) != 1 {
		t.Fatal("concurrent marks produced the wrong map")
	}
}
