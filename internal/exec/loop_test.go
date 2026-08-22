package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// fleetSource serves a fixed provider set, standing in for the SQLite source.
type fleetSource struct{ ps []provider.Provider }

func (f *fleetSource) Providers(context.Context) ([]provider.Provider, error) { return f.ps, nil }
func (f *fleetSource) Revision() uint64                                       { return 1 }

// scripted answers per credential, so a test can say "this key 429s and that
// one succeeds", and records the order the keys were tried in.
type scripted struct {
	mu   sync.Mutex
	seen []string
	by   map[string]http.HandlerFunc
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.seen = append(s.seen, key)
	h, ok := s.by[key]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (s *scripted) order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func ok200(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
		{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

func loopExecutor(t *testing.T, up *httptest.Server, fleet []provider.Provider,
	logger *captureLogger, extraCfg string) (*Executor, *health.Breaker) {

	t.Helper()
	for i := range fleet {
		fleet[i].BaseURL = up.URL
		fleet[i].Kind = "openaicompat"
	}
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n" + extraCfg
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	b := health.New(3, 15*time.Minute)
	e := New(cfgStore, &fleetSource{ps: fleet}, openaicompat.New(), Deps{
		Log: logger, Health: b, Fleet: b,
	})
	return e, b
}

func twoKeyFleet() []provider.Provider {
	return []provider.Provider{{
		ID: "groq", Models: []string{"m"},
		Credentials: []provider.Credential{
			{ID: "g1", Secret: "g1", Enabled: true},
			{ID: "g2", Secret: "g2", Enabled: true},
		}}}
}

func twoProviderFleet() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Priority: 10, Models: []string{"m"},
			Credentials: []provider.Credential{
				{ID: "g1", Secret: "g1", Enabled: true},
				{ID: "g2", Secret: "g2", Enabled: true},
			}},
		{ID: "cerebras", Priority: 5, Models: []string{"m"},
			Credentials: []provider.Credential{{ID: "c1", Secret: "c1", Enabled: true}}},
	}
}

// A done criterion: two credentials on one provider are both exercised on a 429.
func TestLoop429RotatesToTheSecondCredential(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": status(429), "g2": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger, "")
	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sc.order(); len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Errorf("credential order = %v, want [g1 g2]", got)
	}
	r := logger.only(t)
	if len(r.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want 2", r.Attempts)
	}
	if r.Attempts[0].Outcome != "retryable_provider" || r.Attempts[1].Outcome != "success" {
		t.Errorf("outcomes = %s, %s", r.Attempts[0].Outcome, r.Attempts[1].Outcome)
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "2" {
		t.Errorf("attempts header = %q", rec.Header().Get("X-Darkrouter-Attempts"))
	}
}

// A done criterion: both credentials are skipped on a 5xx.
func TestLoop5xxSkipsTheProvidersRemainingCredentials(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(503), "g2": status(503), "c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, "")
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	got := sc.order()
	if len(got) != 2 || got[0] != "g1" || got[1] != "c1" {
		t.Errorf("order = %v, want [g1 c1] — g2 must be skipped wholesale", got)
	}
}

// A done criterion: a malformed request produces exactly one attempt.
func TestLoopFatalProducesExactlyOneAttempt(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(422), "g2": status(422), "c1": status(422),
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, "")
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	if got := sc.order(); len(got) != 1 {
		t.Errorf("attempts = %v, want exactly 1 — a fatal must not burn candidates", got)
	}
	if r := logger.only(t); len(r.Attempts) != 1 {
		t.Errorf("recorded attempts = %d, want 1", len(r.Attempts))
	}
}

// A done criterion: an unknown model advances without penalizing anyone.
func TestLoop404AdvancesWithoutCoolingTheProvider(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(404), "g2": status(404), "c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, b := loopExecutor(t, up, twoProviderFleet(), logger, "")
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	// 404 steps one at a time, so both groq keys are tried.
	if got := sc.order(); len(got) != 3 {
		t.Errorf("order = %v, want all three tried", got)
	}
	if !b.Available(health.Key{ProviderID: "groq", KeyID: "g1", Model: "m"}) {
		t.Error("a 404 must not cool the provider")
	}
}

func TestLoopExhaustionReturnsTheLastError(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(503), "g2": status(503), "c1": status(503),
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger, "")
	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code < 500 {
		t.Errorf("status = %d, want an upstream error", rec.Code)
	}
	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q", r.Status)
	}
	if len(r.Attempts) != 2 {
		t.Errorf("attempts = %d, want 2 (groq skipped wholesale, then cerebras)", len(r.Attempts))
	}
	// Master design §8: an error response names the last attempted target.
	if rec.Header().Get("X-Darkrouter-Provider") != "cerebras" {
		t.Errorf("provider header = %q, want cerebras", rec.Header().Get("X-Darkrouter-Provider"))
	}
}

func TestLoopHonoursMaxAttempts(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(429), "g2": status(429), "c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger,
		"policy:\n  retry:\n    max_attempts: 2\n")

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if got := sc.order(); len(got) != 2 {
		t.Errorf("attempts = %v, want exactly 2", got)
	}
}

func TestLoopMarksCredentialsUsedAtAttemptStart(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": status(429), "g2": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, b := loopExecutor(t, up, twoKeyFleet(), &captureLogger{}, "")
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	lu := b.LastUsedSnapshot()
	// Both were dispatched to, including the one that failed — a credential
	// that always fails must not keep a stale timestamp and sort first forever.
	if _, ok := lu[health.CredKey{ProviderID: "groq", KeyID: "g1"}]; !ok {
		t.Error("the failing credential was not marked used")
	}
	if _, ok := lu[health.CredKey{ProviderID: "groq", KeyID: "g2"}]; !ok {
		t.Error("the succeeding credential was not marked used")
	}
}

func TestLoopUnroutableModelMakesNoAttempt(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger, "")
	rec := post(t, e, `{"model":"nope","messages":[]}`)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if len(sc.order()) != 0 {
		t.Error("an unroutable model must not consume an attempt")
	}
	r := logger.only(t)
	if len(r.Attempts) != 0 {
		t.Errorf("attempts = %d, want 0", len(r.Attempts))
	}
	// No attempt was made, so no provider may be named.
	if rec.Header().Get("X-Darkrouter-Provider") != "" {
		t.Error("naming a provider implies it was tried")
	}
}

// The trace must explain the realized sequence, including what was skipped.
func TestLoopRecordsSkipsOnTheTrace(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": ok200, "g2": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, b := loopExecutor(t, up, twoKeyFleet(), logger, "")
	// Cool g1 before the request so the router skips it at snapshot time.
	for i := 0; i < 3; i++ {
		b.Record(health.Key{ProviderID: "groq", KeyID: "g1", Model: "m"},
			health.Signal{Outcome: "retryable_provider", StatusCode: 503})
	}
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if len(r.Skips) != 1 || !strings.Contains(r.Skips[0], "g1") ||
		!strings.Contains(r.Skips[0], "cooling") {
		t.Errorf("skips = %v, want one cooling skip naming g1", r.Skips)
	}
	if len(r.Candidates) != 1 || !strings.Contains(r.Candidates[0], "g2") {
		t.Errorf("candidates = %v, want only g2", r.Candidates)
	}
}
