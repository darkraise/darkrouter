package exec

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// breakerExecutor is loopExecutor with a breaker whose cooldowns are short
// enough to expire inside a test, so the half-open probe can be exercised
// without a fake clock.
func breakerExecutor(t *testing.T, up *httptest.Server, fleet []provider.Provider,
	deps Deps, extraCfg string) (*Executor, *health.Breaker) {

	t.Helper()
	for i := range fleet {
		fleet[i].BaseURL = up.URL
		if fleet[i].Kind == "" {
			fleet[i].Kind = "openaicompat"
		}
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
	b := health.New(3, 20*time.Millisecond)
	deps.Health, deps.Fleet = b, b
	e := New(cfgStore, &fleetSource{ps: fleet}, map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
	}, deps)
	return e, b
}

var groqKey = health.Key{ProviderID: "groq", KeyID: "g1", Model: "m"}

// oneKeyFleet is one provider with one credential, so every request lands on
// the same breaker entry rather than rotating across two.
func oneKeyFleet() []provider.Provider {
	return []provider.Provider{{
		ID: "groq", Models: []string{"m"},
		Credentials: []provider.Credential{{ID: "g1", Secret: "g1", Enabled: true}},
	}}
}

func tripKey(b *health.Breaker, k health.Key) {
	for i := 0; i < 3; i++ {
		b.Record(k, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
}

// The loop claims the half-open probe before it resolves the credential. When
// that resolution fails, the attempt never reaches the provider — and the
// probe still has to be given back, or the candidate is shut for good.
func TestACredentialFailureOnTheProbeReleasesIt(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	fleet := []provider.Provider{{
		ID: "groq", Models: []string{"m"}, Preset: "anthropic-oauth",
		Credentials: []provider.Credential{{ID: "g1", Secret: "g1", Enabled: true}},
	}}
	e, b := breakerExecutor(t, up, fleet, Deps{
		Log: &captureLogger{}, Auth: failingResolver{providerID: "groq"},
	}, "")

	tripKey(b, groqKey)
	time.Sleep(40 * time.Millisecond)
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if len(sc.order()) != 0 {
		t.Fatal("a credential that cannot be built must not reach the provider")
	}
	// The credential cooldown this failure started has expired too.
	time.Sleep(40 * time.Millisecond)
	if !b.Available(groqKey) {
		t.Fatal("the probe claimed for a failed credential was never released")
	}
}

// postAnthropic drives the IR path: an Anthropic-shaped request to an
// openaicompat upstream cannot be forwarded, so the response is parsed.
func postAnthropic(t *testing.T, e *Executor, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.Handle(rec, r, anthropicedge.New())
	return rec
}

const anthropicPing = `{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`

// A 200 whose body cannot be parsed is a provider fault. Three in a row must
// trip the breaker exactly as three 503s would: the status line alone is not
// proof of a healthy provider.
func TestAnUnparseable200TripsTheBreaker(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`this is not json`))
	}}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, b := breakerExecutor(t, up, oneKeyFleet(), Deps{Log: &captureLogger{}},
		"policy:\n  cooldown:\n    max: 1h\n")
	for i := 0; i < 3; i++ {
		if rec := postAnthropic(t, e, anthropicPing); rec.Code == 200 {
			t.Fatalf("request %d: an unparseable body was served as 200", i)
		}
	}
	if b.Available(groqKey) {
		t.Fatal("three unparseable 200s did not trip the breaker")
	}
}

// The same on the passthrough path: a body that ends before its declared
// length is a read failure before anything reached the client.
func TestATruncated200OnThePassthroughPathTripsTheBreaker(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "500")
		_, _ = w.Write([]byte(`{"id":"x"`))
	}}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, b := breakerExecutor(t, up, oneKeyFleet(), Deps{Log: &captureLogger{}}, "")
	for i := 0; i < 3; i++ {
		if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code == 200 {
			t.Fatalf("request %d: a truncated body was served as 200", i)
		}
	}
	if b.Available(groqKey) {
		t.Fatal("three truncated 200s did not trip the breaker")
	}
}

// A 200 with a good body still closes the ladder, so the fix above cannot
// have turned every success into a non-event.
func TestAHealthy200StillResetsTheLadder(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, b := breakerExecutor(t, up, oneKeyFleet(), Deps{Log: &captureLogger{}}, "")
	b.Record(groqKey, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	b.Record(groqKey, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if rec := postAnthropic(t, e, anthropicPing); rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	b.Record(groqKey, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.Available(groqKey) {
		t.Fatal("a healthy 200 did not reset the failure count")
	}
}
