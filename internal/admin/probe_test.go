package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
)

func listingUpstream(models ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := `{"object":"list","data":[`
		for i, m := range models {
			if i > 0 {
				body += ","
			}
			body += `{"id":"` + m + `"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body + `]}`))
	}
}

func TestASuccessfulProbeReportsWhatItFound(t *testing.T) {
	upstream := httptest.NewServer(listingUpstream("m1", "m2"))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK         bool   `json:"ok"`
		Kind       string `json:"probe"`
		ModelCount int    `json:"model_count"`
		LatencyMs  int64  `json:"latency_ms"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Fatalf("ok = false: %s", w.Body.String())
	}
	if body.ModelCount != 2 {
		t.Errorf("model_count = %d, want 2", body.ModelCount)
	}
	if body.Kind != "listing" {
		t.Errorf("probe = %q; the operator must know which kind ran", body.Kind)
	}
	if body.LatencyMs < 0 {
		t.Errorf("latency = %d", body.LatencyMs)
	}
}

func TestAKeylessProviderWithNoCredentialIsStillProbed(t *testing.T) {
	// The console offers Test on every configured row, and a keyless provider
	// is configured the moment it exists. Refusing it left the one button on
	// the row answering "this provider has no credential to test" for a
	// provider that needs none — and for an anonymous one, whose published key
	// is exactly what an operator wants tested.
	upstream := httptest.NewServer(listingUpstream("m1", "m2", "m3"))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"keyless","name":"keyless","kind":"openaicompat","base_url":"`+
			upstream.URL+`","auth_style":"none"}`); w.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "POST", "/api/providers/keyless/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK         bool `json:"ok"`
		ModelCount int  `json:"model_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ModelCount != 3 {
		t.Errorf("ok = %v, model_count = %d: %s", body.OK, body.ModelCount, w.Body)
	}
}

func TestAFailedProbeCarriesTheProvidersOwnMessage(t *testing.T) {
	// A status line is a poor answer when the provider said something
	// actionable. The local-CLI transport returns 502 for a CLI that is merely
	// logged out, and "Bad Gateway" alone sends the operator to the network.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"auggie listed no models: run auggie login"}}`))
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatal("ok = true for a 502")
	}
	if !strings.Contains(body.Error, "run auggie login") {
		t.Errorf("error = %q, want the provider's own words", body.Error)
	}
}

func TestAProbeAgainstABadCredentialReportsFailureNot500(t *testing.T) {
	// A rejected key is an answer, not a server error. Returning 500 would make
	// the settings screen show "something broke" for the one outcome the button
	// exists to discover.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.OK {
		t.Error("ok = true against a 401")
	}
	if body.Error == "" {
		t.Error("no error message; the operator learns nothing")
	}
}

func TestASuccessfulProbeClearsTheCredentialCooldown(t *testing.T) {
	// The whole reason the probe exists. A credential-level cooldown lives
	// under a key with an EMPTY model, so clearing only the triples would
	// leave the operator reading "probe OK" beside "still cooling".
	upstream := httptest.NewServer(listingUpstream("m1"))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	br := s.deps.Breaker
	credKey := health.Key{ProviderID: "p1", KeyID: keyID}
	for i := 0; i < 5; i++ {
		br.Record(credKey, health.Signal{Outcome: adapter.OutcomeRetryableCredential})
	}
	if br.Available(credKey) {
		t.Fatal("the fixture did not cool the credential")
	}

	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !br.Available(credKey) {
		t.Error("the credential is still cooling after a successful probe")
	}
}

func TestAFailedProbeDoesNotClearACooldown(t *testing.T) {
	// The reset is on success only. A failing probe that cleared the ladder
	// would let a dead provider back into rotation on every click.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	br := s.deps.Breaker
	credKey := health.Key{ProviderID: "p1", KeyID: keyID}
	for i := 0; i < 5; i++ {
		br.Record(credKey, health.Signal{Outcome: adapter.OutcomeRetryableCredential})
	}
	_ = do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if br.Available(credKey) {
		t.Error("a failed probe cleared the cooldown")
	}
}

func TestADoubleClickIssuesOneProbe(t *testing.T) {
	// Spec §4.3. Two probes racing on one credential produce two answers and
	// the operator cannot tell which is current.
	var hits atomic.Int64
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"}]}`))
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = do(t, s, cookie, token, "POST", "/api/providers/p1/test", "").Code
		}(i)
	}
	// Let the first probe reach the upstream and the second reach the mutex.
	time.Sleep(300 * time.Millisecond)
	first := hits.Load()
	close(release)
	wg.Wait()

	if first != 1 {
		t.Errorf("%d probes reached the upstream concurrently; the mutex did not hold", first)
	}
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("probe %d status = %d", i, c)
		}
	}
}

func TestProbingAProviderWithNoCredentialIsARefusal(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestProbingAnUnknownProviderIs404(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers/nope/test", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestASuccessfulProbeClearsTripleCooldownsToo(t *testing.T) {
	// The other half of spec §4.3's ladder reset, and the half a
	// credential-only test misses entirely: a triple cooldown lives under a key
	// WITH a model. Clearing only the credential leaves every model of that
	// provider cooling behind a probe that just reported OK.
	upstream := httptest.NewServer(listingUpstream("m1"))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	// The probe reads the catalog to learn which models to clear, so the
	// snapshot has to carry them.
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{
		{ProviderID: "p1", ModelID: "m1", State: catalog.StateLive},
		{ProviderID: "p1", ModelID: "m2", State: catalog.StateLive},
	}, []string{"p1"}))
	s.deps.Catalog = cat

	br := s.deps.Breaker
	for _, model := range []string{"m1", "m2"} {
		k := health.Key{ProviderID: "p1", KeyID: keyID, Model: model}
		for i := 0; i < 5; i++ {
			br.Record(k, health.Signal{
				Outcome: adapter.OutcomeRetryableProvider, StatusCode: 500,
			})
		}
		if br.Available(k) {
			t.Fatalf("the fixture did not cool %s", model)
		}
	}

	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	for _, model := range []string{"m1", "m2"} {
		if !br.Available(health.Key{ProviderID: "p1", KeyID: keyID, Model: model}) {
			t.Errorf("%s is still cooling after a successful probe", model)
		}
	}
}
