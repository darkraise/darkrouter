package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

// testPassword and testHash are the fixture credential.
//
// The hash is computed once per test binary rather than per fixture: bcrypt at
// cost 12 takes about two seconds by design, and paying that in every test that
// wants a server turns a fast package into a slow one. The cost itself is not
// lowered — it is what spec §3 fixes and what the verify path exercises.
const testPassword = "hunter2"

var testHash = sync.OnceValue(func() string {
	h, err := HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// testServer builds an admin server over a migrated database with one known
// password, and returns it alongside the database so a test can seed rows.
func testServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db := store.MigratedForTest(t)
	s, err := New(Deps{DB: db, PasswordHash: testHash()})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// login performs a real login and returns the session cookie and csrf token,
// which is how every mutating test below authenticates.
func login(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
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
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("login set no cookie")
	}
	return w.Result().Cookies()[0], body.CSRF
}

func TestEveryEndpointExceptStatusRequiresASession(t *testing.T) {
	// Spec §4: auth/status is reachable without a session because the SPA
	// calls it to decide whether to render the login screen. Everything else
	// is closed.
	s, _ := testServer(t)
	for _, ep := range []struct {
		method, path string
	}{
		{"GET", "/api/config"}, {"POST", "/api/config/reload"}, {"POST", "/api/auth/logout"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(ep.method, ep.path, strings.NewReader("{}"))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", ep.method, ep.path, w.Code)
		}
	}
}

func TestAuthStatusIsReachableWithoutASession(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Authenticated {
		t.Error("authenticated = true without a session")
	}
}

func TestAMutatingRequestWithoutACSRFTokenIsRejected(t *testing.T) {
	s, _ := testServer(t)
	cookie, _ := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAMutatingRequestWithAnotherSessionsCSRFTokenIsRejected(t *testing.T) {
	// The token is bound by HMAC, so one lifted from another session is
	// worthless. This is what makes the binding worth having.
	s, _ := testServer(t)
	cookie, _ := login(t, s)
	_, otherToken := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set(csrfHeader, otherToken)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAForgedOriginIsRejected(t *testing.T) {
	// Spec §7 names this case. A correct CSRF token with a foreign Origin
	// still fails, because the two checks are independent.
	s, _ := testServer(t)
	cookie, token := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Origin", "https://evil.example")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAMutatingRequestWithNeitherSiteHeaderIsRejected(t *testing.T) {
	// Treating "no header at all" as same-origin makes the check decorative.
	s, _ := testServer(t)
	cookie, token := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set(csrfHeader, token)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAReadRequestNeedsNoCSRFToken(t *testing.T) {
	// GET is not state-changing, and requiring a token on it would mean the
	// SPA cannot render anything until it has one.
	s, _ := testServer(t)
	cookie, _ := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.AddCookie(cookie)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestAnExpiredSessionIsRejected(t *testing.T) {
	s, db := testServer(t)
	cookie, _ := login(t, s)
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE sessions SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.AddCookie(cookie)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestLoginRotatesTheSessionID(t *testing.T) {
	// Spec §3: a fixated id must not survive an authentication.
	s, db := testServer(t)
	first, _ := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	r.AddCookie(first)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)

	second := w.Result().Cookies()[0]
	if second.Value == first.Value {
		t.Error("the session id was reused across a login")
	}
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`,
		first.Value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the old session row survived the rotation")
	}
}

func TestLogoutDeletesTheRow(t *testing.T) {
	s, db := testServer(t)
	cookie, token := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d session rows survived logout", n)
	}
}

func TestTheSessionCookieIsNotSecureOverPlainHTTP(t *testing.T) {
	// The default homelab posture is plain HTTP. A Secure cookie there is
	// dropped by the browser and login silently never works.
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)

	c := w.Result().Cookies()[0]
	if c.Secure {
		t.Error("Secure is set on a plain-HTTP response")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly is not set; page script can read the session")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}

func TestAWrongPasswordIsRefused(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"wrong"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
