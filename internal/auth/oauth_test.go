package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// authServer is a fake token endpoint that rotates its refresh token on every
// call, which is the behavior spec §5.2 says many vendors have.
type authServer struct {
	mu        sync.Mutex
	refreshes int
	grants    map[string]bool // refresh tokens it will still accept
	status    int
	errBody   string
	expiresIn int
}

func newAuthServer(t *testing.T) (*authServer, *httptest.Server) {
	t.Helper()
	a := &authServer{grants: map[string]bool{"rt-0": true}, expiresIn: 3600}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a.mu.Lock()
		defer a.mu.Unlock()

		if a.status != 0 && a.status != http.StatusOK {
			w.WriteHeader(a.status)
			_, _ = w.Write([]byte(a.errBody))
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			old := r.Form.Get("refresh_token")
			if !a.grants[old] {
				// Rotation-reuse detection: the vendor treats this as theft.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			a.refreshes++
			delete(a.grants, old)
			next := fmt.Sprintf("rt-%d", a.refreshes)
			a.grants[next] = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"at-%d","refresh_token":%q,"token_type":"Bearer","expires_in":%d}`,
				a.refreshes, next, a.expiresIn)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-0","refresh_token":"rt-0","token_type":"Bearer","expires_in":%d}`,
			a.expiresIn)
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *authServer) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshes
}

// memTokens is an in-memory TokenStore recording every persist.
type memTokens struct {
	mu       sync.Mutex
	secrets  map[string]string
	writes   int
	disabled map[string]string
}

func newMemTokens() *memTokens {
	return &memTokens{secrets: map[string]string{}, disabled: map[string]string{}}
}

func (m *memTokens) ReplaceCredentialSecret(_ context.Context, id, secret string, _ *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[id] = secret
	m.writes++
	return nil
}

func (m *memTokens) DisableCredential(_ context.Context, id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabled[id] = reason
	return nil
}

func (m *memTokens) stored(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secrets[id]
}

func (m *memTokens) disabledReason(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.disabled[id]
	return r, ok
}

type fixedPresets struct{ cfg OAuthConfig }

func (f fixedPresets) OAuthFor(string) (OAuthConfig, bool) { return f.cfg, true }

func oauthManager(t *testing.T, srv *httptest.Server, tokens *memTokens) *Manager {
	t.Helper()
	return NewManager(Deps{
		Tokens: tokens,
		OAuth:  fixedPresets{cfg: OAuthConfig{TokenURL: srv.URL, ClientID: "client"}},
		HTTP:   srv.Client(),
	})
}

func expiring(t *testing.T, in time.Duration) string {
	t.Helper()
	tok := Token{AccessToken: "at-0", RefreshToken: "rt-0", ExpiresAt: time.Now().Add(in)}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func oauthAz(t *testing.T, m *Manager, secret string) Authorizer {
	t.Helper()
	az, err := m.For(context.Background(),
		Target{ProviderID: "sub", Style: StyleOAuth, Preset: "anthropic-oauth"},
		Credential{ID: "cred-1", Kind: "oauth", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return az
}

func blank(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", "https://api.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAValidTokenIsUsedWithoutRefreshing(t *testing.T) {
	a, srv := newAuthServer(t)
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, time.Hour))

	r := blank(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-0" {
		t.Errorf("Authorization = %q", got)
	}
	if a.count() != 0 {
		t.Errorf("refreshed %d times for a token valid for an hour", a.count())
	}
}

func TestRotationIsPersistedBeforeTheOldPairIsDropped(t *testing.T) {
	// Spec §5.2's crash window. If the new pair is not durable before the old
	// one stops being valid, a crash here bricks the account.
	a, srv := newAuthServer(t)
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	if err := az(context.Background(), blank(t)); err != nil {
		t.Fatal(err)
	}
	if a.count() != 1 {
		t.Fatalf("refreshed %d times, want 1", a.count())
	}
	stored, err := ParseToken([]byte(tokens.stored("cred-1")))
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "rt-1" {
		t.Errorf("stored refresh token = %q, want the rotated one", stored.RefreshToken)
	}
	if stored.AccessToken != "at-1" {
		t.Errorf("stored access token = %q", stored.AccessToken)
	}
	tokens.mu.Lock()
	writes := tokens.writes
	tokens.mu.Unlock()
	if writes != 1 {
		t.Errorf("persisted %d times, want exactly 1", writes)
	}
}

func TestConcurrentRequestsTriggerOneRefresh(t *testing.T) {
	// Without the per-account mutex, twenty concurrent requests start twenty
	// refreshes; nineteen present the rotated-away token and the vendor sees
	// rotation reuse, which some treat as theft.
	a, srv := newAuthServer(t)
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, -time.Minute))

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- az(context.Background(), blank(t))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a.count() != 1 {
		t.Errorf("refreshed %d times under concurrency, want 1", a.count())
	}
}

func TestInvalidGrantDisablesWithoutRetrying(t *testing.T) {
	// Hammering a refused refresh endpoint is how an account gets locked
	// rather than recovered.
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	for i := 0; i < 3; i++ {
		if err := az(context.Background(), blank(t)); err == nil {
			t.Fatal("a refused refresh must be an error")
		}
	}
	reason, disabled := tokens.disabledReason("cred-1")
	if !disabled {
		t.Fatal("an invalid_grant must disable the credential pending reconnection")
	}
	if !strings.Contains(strings.ToLower(reason), "reconnect") {
		t.Errorf("the reason must tell the operator what to do, got %q", reason)
	}
}

func TestATransientFailureDoesNotDisable(t *testing.T) {
	// A 500 from the token endpoint is the vendor's problem, not the account's.
	// Disabling here turns a five-minute outage into a manual reconnection.
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusInternalServerError, `{"error":"server_error"}`
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	if err := az(context.Background(), blank(t)); err == nil {
		t.Fatal("a failed refresh must be an error")
	}
	if _, disabled := tokens.disabledReason("cred-1"); disabled {
		t.Error("a 5xx from the token endpoint must not disable the credential")
	}
}

func TestATransientFailureLeavesTheStoredPairAlone(t *testing.T) {
	// The old refresh token is still the only one that exists. Overwriting it
	// with nothing would brick the account on a five-minute outage.
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusInternalServerError, `{"error":"server_error"}`
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	_ = az(context.Background(), blank(t))
	if tokens.stored("cred-1") != "" {
		t.Errorf("a transient failure wrote to the store: %q", tokens.stored("cred-1"))
	}
}

func TestNoTokenReachesAnErrorString(t *testing.T) {
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, -time.Minute))

	err := az(context.Background(), blank(t))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range []string{"rt-0", "at-0"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error carries %q: %v", secret, err)
		}
	}
}

func TestAResponseWithNoNewRefreshTokenKeepsTheOldOne(t *testing.T) {
	// Not every vendor rotates. "No new refresh token" means keep using the
	// one you have, not "you now have none".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-new","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)

	tokens := newMemTokens()
	m := NewManager(Deps{
		Tokens: tokens,
		OAuth:  fixedPresets{cfg: OAuthConfig{TokenURL: srv.URL, ClientID: "client"}},
		HTTP:   srv.Client(),
	})
	az := oauthAz(t, m, expiring(t, -time.Minute))
	if err := az(context.Background(), blank(t)); err != nil {
		t.Fatal(err)
	}
	stored, err := ParseToken([]byte(tokens.stored("cred-1")))
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "rt-0" {
		t.Errorf("refresh token = %q; the old one was dropped", stored.RefreshToken)
	}
}

func TestACredentialWithNoRefreshTokenIsDisabled(t *testing.T) {
	// Nothing can renew it, and every request would fail the same way. Saying
	// so once beats failing silently forever.
	_, srv := newAuthServer(t)
	tokens := newMemTokens()
	tok := Token{AccessToken: "at", ExpiresAt: time.Now().Add(-time.Minute)}
	raw, _ := tok.Marshal()
	az := oauthAz(t, oauthManager(t, srv, tokens), string(raw))

	if err := az(context.Background(), blank(t)); err == nil {
		t.Fatal("expected an error")
	}
	if _, disabled := tokens.disabledReason("cred-1"); !disabled {
		t.Error("a credential with no refresh token must be disabled")
	}
}

func TestRefreshJitterStaysInsideItsWindow(t *testing.T) {
	// Spec §5.2 wants jitter so a fleet does not refresh simultaneously. The
	// property that matters is bounded, not the distribution.
	base := 10 * time.Minute
	for i := 0; i < 200; i++ {
		d := jitter(base)
		if d < base/2 || d > base+base/2 {
			t.Fatalf("jitter(%v) = %v, outside the window", base, d)
		}
	}
}

// expiringFake is a one-row ExpiringSource.
type expiringFake struct {
	rows []StoredCredential
}

func (e *expiringFake) Expiring(context.Context, string, int64) ([]StoredCredential, error) {
	return e.rows, nil
}

func TestTheWorkerRefreshesThroughTheSamePath(t *testing.T) {
	// One refresh path exercised two ways. A worker with its own copy would
	// drift, and would not take the per-account mutex a request takes.
	a, srv := newAuthServer(t)
	tokens := newMemTokens()
	m := oauthManager(t, srv, tokens)
	w := NewRefreshWorker(m, &expiringFake{rows: []StoredCredential{{
		ID: "cred-1", ProviderID: "sub", Kind: "oauth",
		Secret: expiring(t, time.Minute), Style: StyleOAuth, Preset: "anthropic-oauth",
	}}}, RefreshOptions{})

	w.Once(context.Background())
	if a.count() != 1 {
		t.Fatalf("the worker refreshed %d times, want 1", a.count())
	}
	stored, err := ParseToken([]byte(tokens.stored("cred-1")))
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "rt-1" {
		t.Errorf("the worker did not persist the rotated pair: %q", stored.RefreshToken)
	}
}

func TestTheWorkerAndARequestShareOneRefresh(t *testing.T) {
	// Spec §5.2: a probe or a worker that consumed a refresh would race the
	// other and present a rotated-away token.
	a, srv := newAuthServer(t)
	m := oauthManager(t, srv, newMemTokens())
	secret := expiring(t, -time.Minute)
	az := oauthAz(t, m, secret)
	w := NewRefreshWorker(m, &expiringFake{rows: []StoredCredential{{
		ID: "cred-1", ProviderID: "sub", Kind: "oauth",
		Secret: secret, Style: StyleOAuth, Preset: "anthropic-oauth",
	}}}, RefreshOptions{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.Once(context.Background()) }()
	go func() { defer wg.Done(); _ = az(context.Background(), blank(t)) }()
	wg.Wait()

	if a.count() != 1 {
		t.Errorf("refreshed %d times, want 1: the worker and the request did not share a mutex", a.count())
	}
}

func TestAManagerWithNoPresetsRefusesOAuth(t *testing.T) {
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "sub", Style: StyleOAuth, Preset: "anthropic-oauth"},
		Credential{ID: "c", Kind: "oauth", Secret: `{"access_token":"a"}`})
	if err == nil {
		t.Fatal("an oauth provider with no preset data must be refused")
	}
}

func TestOAuthBearerCarriesTheBetaHeader(t *testing.T) {
	// Anthropic refuses an OAuth bearer token on /v1/messages unless the
	// request also declares the oauth beta. The authorizer is the one place
	// that knows the credential is an OAuth grant, so it is where the header
	// is added — on the IR and the passthrough path alike.
	_, srv := newAuthServer(t)
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, time.Hour))

	r, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("anthropic-beta"); got != OAuthBeta {
		t.Errorf("anthropic-beta = %q, want %q", got, OAuthBeta)
	}
}

func TestOAuthBetaMergesWithTheClientsList(t *testing.T) {
	// A passthrough request forwards the client's own beta list. Replacing it
	// would strip a feature the client asked for; appending twice would send
	// the oauth beta on every retry of an already-authorized request.
	_, srv := newAuthServer(t)
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, time.Hour))

	r, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	r.Header.Add("anthropic-beta", "context-1m-2025-08-07, interleaved-thinking-2025-05-14")
	r.Header.Add("anthropic-beta", OAuthBeta)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Values("anthropic-beta"); len(got) != 1 {
		t.Fatalf("anthropic-beta = %q, want one merged value", got)
	}
	want := "context-1m-2025-08-07,interleaved-thinking-2025-05-14," + OAuthBeta
	if got := r.Header.Get("anthropic-beta"); got != want {
		t.Errorf("anthropic-beta = %q, want %q", got, want)
	}
}
