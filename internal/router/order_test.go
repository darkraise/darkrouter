package router

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

func creds(ids ...string) []provider.Credential {
	out := make([]provider.Credential, 0, len(ids))
	for _, id := range ids {
		out = append(out, provider.Credential{ID: id, Secret: "sk-" + id, Enabled: true})
	}
	return out
}

func idsOf(cs []provider.Credential) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOrderPutsLeastRecentlyUsedFirst(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "a"}: now.Add(-time.Minute),
		{ProviderID: "groq", KeyID: "b"}: now.Add(-time.Hour),
		{ProviderID: "groq", KeyID: "c"}: now.Add(-time.Second),
	}
	got := idsOf(orderCredentials("groq", creds("a", "b", "c"), lastUsed))
	if !eq(got, "b", "a", "c") {
		t.Errorf("order = %v, want [b a c]", got)
	}
}

// A key never dispatched to should be tried before one carrying the load.
func TestOrderPutsNeverUsedFirst(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "a"}: now.Add(-time.Hour),
	}
	got := idsOf(orderCredentials("groq", creds("a", "fresh"), lastUsed))
	if !eq(got, "fresh", "a") {
		t.Errorf("order = %v, want [fresh a]", got)
	}
}

// Determinism: equal timestamps must break by id, or the sequence depends on
// map iteration order and stops being explainable.
func TestOrderBreaksTiesByKeyID(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "c"}: now,
		{ProviderID: "groq", KeyID: "a"}: now,
		{ProviderID: "groq", KeyID: "b"}: now,
	}
	for i := 0; i < 20; i++ {
		got := idsOf(orderCredentials("groq", creds("c", "a", "b"), lastUsed))
		if !eq(got, "a", "b", "c") {
			t.Fatalf("run %d: order = %v, want [a b c]", i, got)
		}
	}
}

func TestOrderIgnoresOtherProvidersTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		// Same key id, different provider: must not influence groq's order.
		{ProviderID: "cerebras", KeyID: "a"}: now.Add(-time.Hour),
		{ProviderID: "groq", KeyID: "b"}:     now.Add(-time.Hour),
	}
	got := idsOf(orderCredentials("groq", creds("a", "b"), lastUsed))
	// "a" is unused on groq, so it sorts before b's one-hour-old timestamp.
	if !eq(got, "a", "b") {
		t.Errorf("order = %v, want [a b]", got)
	}
}

func TestOrderDoesNotMutateItsInput(t *testing.T) {
	in := creds("c", "a", "b")
	orderCredentials("groq", in, nil)
	if !eq(idsOf(in), "c", "a", "b") {
		t.Errorf("input was reordered: %v", idsOf(in))
	}
}
