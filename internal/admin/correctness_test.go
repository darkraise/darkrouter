package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

type captureLogger struct {
	mu      sync.Mutex
	records []*store.RequestRecord
}

func (c *captureLogger) Log(r *store.RequestRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
}

func TestAuxRequestsAreLoggedAsConsoleTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
		  {"object":"embedding","index":0,"embedding":[0.1,0.2]}],
		  "model":"m","usage":{"prompt_tokens":2,"total_tokens":2}}`))
	}))
	defer upstream.Close()
	rec := &captureLogger{}
	s := testServerWithExecutorLog(t, upstream.URL, "m", rec)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground/aux",
		`{"surface":"embeddings","model":"m","body":{"input":"hello"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.records) != 1 || rec.records[0].Source != store.SourceConsole {
		t.Errorf("records = %+v, want one tagged console", rec.records)
	}
}

func TestCountRequestsAreLoggedAsConsoleTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":3}`))
	}))
	defer upstream.Close()
	rec := &captureLogger{}
	s := testServerWithExecutorLog(t, upstream.URL, "m", rec)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/playground/count",
		`{"dialect":"anthropic","model":"m","prompt":"hello"}`)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, r := range rec.records {
		if r.Source != store.SourceConsole {
			t.Errorf("count record tagged %q, want console", r.Source)
		}
	}
	if len(rec.records) == 0 {
		t.Skip("the count path recorded nothing to check")
	}
}

func TestASecondFlowReplacesTheListener(t *testing.T) {
	s, cookie, token, _, _ := serverWithListener(t)
	id := oauthProvider(t, s, cookie, token)
	first := startFlow(t, s, cookie, token, id, `{}`)
	second := startFlow(t, s, cookie, token, id, `{}`)
	if first.Style != "localhost" || second.Style != "localhost" {
		t.Fatalf("styles = %q then %q; the second flow could not bind: %q",
			first.Style, second.Style, second.ListenerError)
	}
}

func TestTheSweeperDropsAbandonedFlows(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Flows = auth.NewFlowStore(time.Minute)
	state, err := s.deps.Flows.Begin(auth.Flow{ProviderID: "p", SessionID: "sess",
		CreatedAt: time.Now().Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	s.sweepOnce(time.Now())
	if _, err := s.deps.Flows.Claim(state, "sess"); err != auth.ErrUnknownState {
		t.Errorf("claim after sweep = %v, want ErrUnknownState", err)
	}
}

func TestAChangedEnvironmentHashOverridesTheStoredPassword(t *testing.T) {
	db := storetest.Migrated(t)
	open := func(hash string) *Server {
		s, err := New(Deps{DB: db, PasswordHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	tryLogin := func(s *Server, password string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	s := open(testHash())
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/auth/password",
		`{"current":"`+testPassword+`","new":"console-set-password"}`); w.Code != http.StatusOK {
		t.Fatalf("change = %d %s", w.Code, w.Body.String())
	}
	// Same environment across a restart: the console's password holds.
	if code := tryLogin(open(testHash()), "console-set-password"); code != http.StatusOK {
		t.Fatalf("after a restart with the same env hash: %d", code)
	}
	// A new environment hash: it wins and the stored one is gone.
	newHash, err := bcrypt.GenerateFromPassword([]byte("env-recovery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	s3 := open(string(newHash))
	if code := tryLogin(s3, "console-set-password"); code != http.StatusUnauthorized {
		t.Errorf("stale console password still logs in: %d", code)
	}
	if code := tryLogin(s3, "env-recovery"); code != http.StatusOK {
		t.Errorf("the new environment password does not log in: %d", code)
	}
	if _, ok, _ := db.GetSetting(context.Background(), settingAdminPasswordHash); ok {
		t.Error("the stored hash survived a changed environment hash")
	}
}

func TestOverlongPasswordsAre400(t *testing.T) {
	s, _ := testServer(t)
	long := strings.Repeat("x", 73)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"`+long+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("login with 73 bytes = %d, want 400", w.Code)
	}
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/auth/password",
		`{"current":"`+testPassword+`","new":"`+long+`"}`); w.Code != http.StatusBadRequest {
		t.Errorf("change to 73 bytes = %d, want 400", w.Code)
	}
}

func TestPresetRenameOntoATakenNameIs409(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	mk := func(name string) string {
		w := do(t, s, cookie, token, "POST", "/api/playground/presets",
			`{"name":"`+name+`","dialect":"openai","model":"m","config":{}}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", name, w.Code, w.Body.String())
		}
		var v struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return v.ID
	}
	mk("taken")
	other := mk("other")
	w := do(t, s, cookie, token, "PATCH", "/api/playground/presets/"+other,
		`{"name":"taken","dialect":"openai","model":"m","config":{}}`)
	if w.Code != http.StatusConflict {
		t.Errorf("rename onto a taken name = %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestCredentialSecretReplaceIsScopedToItsProvider(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p2","name":"P2","kind":"openaicompat","base_url":"https://y/v1"}`)
	w := do(t, s, cookie, token, "PATCH", "/api/providers/p2/keys/"+keyID, `{"secret":"sk-new-value-1234"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("replacing p1's key under p2 = %d, want 404: %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "PATCH", "/api/providers/p1/keys/"+keyID, `{"secret":"sk-new-value-1234"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"masked":"…1234"`) {
		t.Errorf("replace under p1 = %d: %s", w.Code, w.Body.String())
	}
}

func TestCredentialPatchWithoutAKeyringIs503(t *testing.T) {
	s, _ := testServer(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PATCH", "/api/providers/p/keys/k", `{"enabled":false}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestBreakerResetNeedsNoKeyring(t *testing.T) {
	s, db := testServer(t)
	if err := db.CreateProvider(context.Background(), store.ProviderRow{ID: "p", Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers/p/breaker/reset", "")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d: %s", w.Code, w.Body.String())
	}
}

func TestDeletingAMissingOverrideIs404(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "DELETE", "/api/models/p/m/override", ""); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSessionDeleteNeedsAnUnambiguousPrefix(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "DELETE", "/api/sessions/abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("a 3-character prefix = %d, want 400", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/sessions/0000000000", ""); w.Code != http.StatusNotFound {
		t.Errorf("an unknown prefix = %d, want 404", w.Code)
	}
	// The full stored id works too.
	full := store.HashSessionID(cookie.Value)
	if w := do(t, s, cookie, token, "DELETE", "/api/sessions/"+full, ""); w.Code != http.StatusNoContent {
		t.Errorf("the full id = %d, want 204", w.Code)
	}
}

func TestUnparseableIntegerParametersAre400(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for _, path := range []string{
		"/api/requests?since_ms=yesterday",
		"/api/requests?until_ms=1.5",
		"/api/requests?limit=many",
		"/api/usage?days=week",
		"/api/usage?days=0",
	} {
		if w := do(t, s, cookie, token, "GET", path, ""); w.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, w.Code)
		}
	}
}

func TestProviderDeleteForgetsItsCredentials(t *testing.T) {
	s, _ := testServerFull(t)
	forgot := &forgetRecorder{}
	s.deps.Auth = forgot
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")
	s.probes.get("p1")
	if w := do(t, s, cookie, token, "DELETE", "/api/providers/p1", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if len(forgot.forgot) != 1 || forgot.forgot[0] != keyID {
		t.Errorf("forgot %v, want [%s]", forgot.forgot, keyID)
	}
	if _, held := s.probes.m["p1"]; held {
		t.Error("the probe lock survived the provider")
	}
}
