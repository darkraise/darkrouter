package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// testPassword and testHash are the fixture credential.
//
// Hashed at bcrypt's minimum cost, and computed once per test binary.
// CompareHashAndPassword reads the cost out of the hash it is given, so a
// fixture hashed at the production cost charges every login in this package
// the full two seconds — and the package logs in about a hundred times. Under
// -race that came to eight minutes, which is how internal/admin hit the
// ten-minute test timeout the first time CI ran.
//
// Lowering it here lowers nothing in production: passwordCost is untouched,
// HashPassword still uses it, and the password tests still exercise a real
// hash at the real cost.
const testPassword = "hunter2"

var testHash = sync.OnceValue(func() string {
	h, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(h)
})

// testUsername is the account every login helper in this package authenticates
// as. It is created on demand rather than by testServer, because an unclaimed
// console is a state several tests assert on.
const testUsername = "admin"

// seedTestAccount claims the console for testUsername, unless a login helper
// already did. Idempotent, because a test that wants two sessions calls login
// twice.
func seedTestAccount(t *testing.T, s *Server) {
	t.Helper()
	if _, ok, err := s.deps.DB.UserByUsername(t.Context(), testUsername); err != nil {
		t.Fatal(err)
	} else if ok {
		return
	}
	claimed, err := s.deps.DB.ClaimFirstUser(t.Context(), "u-admin", testUsername, testHash())
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("the console was already claimed by another account")
	}
}

// testCredentials is the JSON body the login helpers post.
func testCredentials(password string) string {
	return `{"username":"` + testUsername + `","password":"` + password + `"}`
}

// testServer builds an admin server over a migrated database with one known
// password, and returns it alongside the database so a test can seed rows.
func testServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	s, err := New(Deps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// login performs a real login and returns the session cookie and csrf token,
// which is how every mutating test below authenticates.
func login(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	seedTestAccount(t, s)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(testCredentials(testPassword)))
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
		strings.NewReader(testCredentials(testPassword)))
	r.AddCookie(first)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)

	second := w.Result().Cookies()[0]
	if second.Value == first.Value {
		t.Error("the session id was reused across a login")
	}
	var n int
	// The cookie carries the raw id; the row is keyed by its digest.
	if err := db.Read.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`,
		store.HashSessionID(first.Value)).Scan(&n); err != nil {
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
	seedTestAccount(t, s)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(testCredentials(testPassword)))
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
	seedTestAccount(t, s)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(testCredentials("wrong")))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireSessionPutsTheOwnerInContext(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)

	var seen string
	h := s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		seen = userFrom(r.Context())
	})
	req := httptest.NewRequest("GET", "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	h(httptest.NewRecorder(), req)

	if seen != uid {
		t.Errorf("userFrom = %q, want %q", seen, uid)
	}
}

func TestRequireCSRFInheritsTheOwner(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)

	var seen string
	h := s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		seen = userFrom(r.Context())
	})
	req := httptest.NewRequest("POST", "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(csrfHeader, s.csrf.Token(cookie))
	h(httptest.NewRecorder(), req)

	if seen != uid {
		t.Errorf("userFrom through requireCSRF = %q, want %q", seen, uid)
	}
}
