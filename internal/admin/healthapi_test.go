package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
)

type breakerEntryView struct {
	ProviderID          string `json:"provider_id"`
	KeyID               string `json:"key_id"`
	Model               string `json:"model"`
	CoolingUntil        string `json:"cooling_until"`
	BackoffLevel        int    `json:"backoff_level"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

func healthEntries(t *testing.T, s *Server) []breakerEntryView {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/health/providers", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/health/providers = %d: %s", w.Code, w.Body.String())
	}
	var out []breakerEntryView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return out
}

// coolCredential trips the breaker for one credential by recording enough
// consecutive failures to cross trip_after.
func coolCredential(s *Server, providerID, keyID string) {
	for i := 0; i < 5; i++ {
		s.deps.Breaker.Record(healthKey(providerID, keyID, ""),
			health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
}

func TestHealthProvidersReportsBreakerDetail(t *testing.T) {
	s, _ := testServerFull(t)
	coolCredential(s, "groq", "k1")

	got := healthEntries(t, s)
	if len(got) == 0 {
		t.Fatal("no breaker entries after tripping one")
	}
	var found *breakerEntryView
	for i := range got {
		if got[i].ProviderID == "groq" && got[i].KeyID == "k1" {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("the tripped credential is missing: %+v", got)
	}
	if found.CoolingUntil == "" {
		t.Error("cooling_until is empty on a cooling credential")
	}
	if found.ConsecutiveFailures == 0 {
		t.Error("consecutive_failures is zero after five failures")
	}
}

func TestHealthProvidersIsAnArrayWhenNothingIsCooling(t *testing.T) {
	// A JSON null where the console expects an array is a defect this project
	// has already fixed once, on the usage series.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/health/providers", "")
	if strings.TrimSpace(w.Body.String()) == "null" {
		t.Error("an empty breaker serialized as null rather than []")
	}
}

func TestBreakerResetClearsACooldown(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	// The real credential id, not a made-up one: a cooldown is keyed on the
	// credential the router actually attempted.
	keyID := seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	coolCredential(s, "groq", keyID)
	if s.deps.Breaker.Available(healthKey("groq", keyID, "")) {
		t.Fatal("the credential did not cool")
	}

	w := do(t, s, cookie, token, "POST", "/api/providers/groq/breaker/reset", "")
	if w.Code != 200 {
		t.Fatalf("reset = %d: %s", w.Code, w.Body.String())
	}
	if !s.deps.Breaker.Available(healthKey("groq", keyID, "")) {
		t.Error("the credential is still cooling after a reset")
	}
}

func TestBreakerResetOnAnUnknownProviderIs404(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers/nosuch/breaker/reset", "")
	if w.Code != 404 {
		t.Fatalf("reset on an unknown provider = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestForcedSweepsAreAccepted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	for _, path := range []string{
		"/api/providers/groq/discover",
		"/api/catalog/sync",
	} {
		w := do(t, s, cookie, token, "POST", path, "")
		if w.Code != 202 && w.Code != 200 {
			t.Errorf("POST %s = %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestHealthWritesNeedASession(t *testing.T) {
	s, _ := testServerFull(t)
	for _, path := range []string{
		"/api/providers/groq/breaker/reset",
		"/api/providers/groq/discover",
		"/api/catalog/sync",
	} {
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("unauthenticated POST %s = %d, want 401", path, w.Code)
		}
	}
}
