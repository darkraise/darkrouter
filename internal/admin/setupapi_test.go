package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// setupPassword is long enough for the twelve-character floor the setup and
// change-password paths share; testPassword is deliberately shorter.
const setupPassword = "correct horse battery"

// unconfigured builds an admin server over an empty database with no password
// in the environment, which is what a fresh deployment starts as.
func unconfigured(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	s, err := New(Deps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// loginAs is login for a password other than the fixture's.
func loginAs(t *testing.T, s *Server, password string) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0], body.CSRF
}

func postSetup(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(body))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAnUnconfiguredServerMintsASetupToken(t *testing.T) {
	s, _ := unconfigured(t)
	if s.setupToken == "" {
		t.Fatal("no setup token was minted for an unconfigured server")
	}
}

// The token is the claim on an unclaimed console. A server that already has a
// password has nothing to claim, and minting one would leave a credential in
// memory that opens nothing.
func TestAConfiguredServerMintsNoSetupToken(t *testing.T) {
	s, _ := testServer(t)
	if s.setupToken != "" {
		t.Error("a configured server minted a setup token")
	}
}

func TestSetupClaimsTheConsoleWithTheToken(t *testing.T) {
	s, _ := unconfigured(t)
	w := postSetup(t, s, `{"token":"`+s.setupToken+`","password":"`+setupPassword+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("setup = %d, body = %s", w.Code, w.Body.String())
	}
	// The password it set is the one login now accepts, which is the whole
	// point: a claim that did not change what logins check is not a claim.
	if !VerifyPassword(s.currentPasswordHash(context.Background()), setupPassword) {
		t.Error("the password set through setup is not the one logins check")
	}
}

// Setup mints no session. Issuing one here would be a second place that
// writes the session cookie, with its own attributes to keep in step with
// handleLogin's; the console logs in immediately afterwards instead.
func TestSetupIssuesNoSession(t *testing.T) {
	s, _ := unconfigured(t)
	w := postSetup(t, s, `{"token":"`+s.setupToken+`","password":"`+setupPassword+`"}`)
	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("setup set %d cookies, want none", len(cookies))
	}
}

func TestSetupRefusesAWrongToken(t *testing.T) {
	s, _ := unconfigured(t)
	w := postSetup(t, s, `{"token":"not-the-token","password":"`+setupPassword+`"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("setup with a wrong token = %d, want 401", w.Code)
	}
	if VerifyPassword(s.currentPasswordHash(context.Background()), setupPassword) {
		t.Error("a refused setup still set the password")
	}
}

// Reaching the endpoint on a console someone else already claimed must not
// evaluate the token at all, let alone overwrite the password.
func TestSetupRefusesOnceConfigured(t *testing.T) {
	s, _ := testServer(t)
	w := postSetup(t, s, `{"token":"anything","password":"`+setupPassword+`"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("setup on a configured console = %d, want 409", w.Code)
	}
	if !VerifyPassword(s.currentPasswordHash(context.Background()), testPassword) {
		t.Error("setup overwrote a password that was already set")
	}
}

func TestSetupRefusesAShortPassword(t *testing.T) {
	s, _ := unconfigured(t)
	w := postSetup(t, s, `{"token":"`+s.setupToken+`","password":"short"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("setup with a short password = %d, want 400", w.Code)
	}
}

// The token opens the console once. Leaving it live would let anyone who read
// the log line re-claim a console after the operator changed the password.
func TestTheSetupTokenIsSpentOnceUsed(t *testing.T) {
	s, _ := unconfigured(t)
	token := s.setupToken
	if w := postSetup(t, s, `{"token":"`+token+`","password":"`+setupPassword+`"}`); w.Code != http.StatusOK {
		t.Fatalf("first setup = %d", w.Code)
	}
	if s.setupToken != "" {
		t.Error("the setup token survived the claim that spent it")
	}
}

// A password set through setup runs with no environment hash, and the
// fingerprint of that empty environment is what lets a later
// DARKROUTER_ADMIN_PASSWORD_HASH read as newer. Without the row the
// documented recovery from a lost password silently does nothing.
func TestAnEnvHashSetAfterSetupWinsOnRestart(t *testing.T) {
	s, db := unconfigured(t)
	if w := postSetup(t, s, `{"token":"`+s.setupToken+`","password":"`+setupPassword+`"}`); w.Code != http.StatusOK {
		t.Fatalf("setup = %d", w.Code)
	}
	// The operator lost the password and seeds the environment to recover.
	restarted, err := New(Deps{DB: db, PasswordHash: testHash()})
	if err != nil {
		t.Fatal(err)
	}
	hash := restarted.currentPasswordHash(context.Background())
	if VerifyPassword(hash, setupPassword) {
		t.Error("the password set through setup still wins after the environment was seeded")
	}
	if !VerifyPassword(hash, testPassword) {
		t.Error("the environment hash did not take effect on restart")
	}
}

// The same gap on the change-password path: reachable only once setup exists,
// because before it nobody could log in without an environment hash.
func TestAnEnvHashSetAfterAPasswordChangeWinsOnRestart(t *testing.T) {
	s, db := unconfigured(t)
	if w := postSetup(t, s, `{"token":"`+s.setupToken+`","password":"`+setupPassword+`"}`); w.Code != http.StatusOK {
		t.Fatalf("setup = %d", w.Code)
	}
	cookie, csrf := loginAs(t, s, setupPassword)
	const changed = "a different long password"
	body, _ := json.Marshal(map[string]string{"current": setupPassword, "new": changed})
	if w := do(t, s, cookie, csrf, "POST", "/api/auth/password", string(body)); w.Code != http.StatusOK {
		t.Fatalf("change password = %d, body = %s", w.Code, w.Body.String())
	}
	restarted, err := New(Deps{DB: db, PasswordHash: testHash()})
	if err != nil {
		t.Fatal(err)
	}
	if VerifyPassword(restarted.currentPasswordHash(context.Background()), changed) {
		t.Error("the changed password still wins after the environment was seeded")
	}
}

// /healthz and the config endpoint serve Deps.Warnings without a session. A
// claim token that reached that slice would be readable by exactly the caller
// requiring a token is meant to exclude.
func TestTheSetupTokenNeverReachesTheWarnings(t *testing.T) {
	db := storetest.Migrated(t)
	s, err := New(Deps{DB: db, Warnings: []string{"an existing warning"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.setupToken == "" {
		t.Fatal("no token was minted, so this proves nothing")
	}
	for _, w := range s.deps.Warnings {
		if strings.Contains(w, s.setupToken) {
			t.Fatalf("the setup token is in an unauthenticated warning: %q", w)
		}
	}
}
