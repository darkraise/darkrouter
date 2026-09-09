package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

func TestSessionsListMarksTheCaller(t *testing.T) {
	// An operator revoking sessions needs to know which row logs them out.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_, _ = login(t, s) // a second, unrelated session

	w := do(t, s, cookie, token, "GET", "/api/sessions", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/sessions = %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Sessions []sessionView `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	got := env.Sessions
	if len(got) < 2 {
		t.Fatalf("listed %d sessions, want at least 2", len(got))
	}
	current := 0
	for _, v := range got {
		if v.Current {
			current++
		}
	}
	if current != 1 {
		t.Errorf("%d sessions marked current, want exactly 1", current)
	}
}

func TestSessionsListNeverShowsAFullID(t *testing.T) {
	// The id is the credential the cookie carries: a screenshot of the
	// settings screen must not be able to authenticate.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/sessions", "")
	if strings.Contains(w.Body.String(), cookie.Value) {
		t.Error("the listing reproduced a full session id")
	}
}

func TestSessionDeleteRevokes(t *testing.T) {
	s, _ := testServerFull(t)
	victim, _ := login(t, s)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/sessions", "")
	var env struct {
		Sessions []sessionView `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	listed := env.Sessions
	var target string
	for _, v := range listed {
		if !v.Current {
			target = v.ID
		}
	}
	if target == "" {
		t.Fatal("no other session to revoke")
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/sessions/"+target, ""); w.Code != 204 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
	}
	// The revoked cookie no longer authenticates.
	if w := do(t, s, victim, token, "GET", "/api/sessions", ""); w.Code != 401 {
		t.Errorf("a revoked session still authenticates: %d", w.Code)
	}
}

func TestPasswordChangeRequiresTheCurrentOne(t *testing.T) {
	// Without it a stolen cookie becomes a permanent takeover rather than one
	// that expires.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/auth/password",
		`{"current":"wrong","new":"a-much-longer-password"}`)
	if w.Code != 401 {
		t.Fatalf("change with a wrong current password = %d, want 401", w.Code)
	}
}

func TestPasswordChangeTakesEffectAndRevokesOthers(t *testing.T) {
	s, _ := testServerFull(t)
	victim, _ := login(t, s)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/auth/password",
		`{"current":"`+testPassword+`","new":"a-much-longer-password"}`)
	if w.Code != 200 {
		t.Fatalf("change = %d: %s", w.Code, w.Body.String())
	}

	// The caller keeps working; anything else would log the operator out of
	// the screen they just used.
	if w := do(t, s, cookie, token, "GET", "/api/sessions", ""); w.Code != 200 {
		t.Errorf("the caller's session was revoked: %d", w.Code)
	}
	if w := do(t, s, victim, token, "GET", "/api/sessions", ""); w.Code != 401 {
		t.Errorf("another session survived the change: %d", w.Code)
	}
	u, found, err := s.deps.DB.UserByID(t.Context(), "u-admin")
	if err != nil || !found {
		t.Fatalf("the caller's account is gone: %v found=%v", err, found)
	}
	if !VerifyPassword(u.PasswordHash, "a-much-longer-password") {
		t.Error("the new password does not verify")
	}
	if VerifyPassword(u.PasswordHash, testPassword) {
		t.Error("the old password still verifies")
	}
}

func TestPasswordChangeRejectsAShortPassword(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/auth/password",
		`{"current":"`+testPassword+`","new":"short"}`)
	if w.Code != 400 {
		t.Fatalf("short password = %d, want 400", w.Code)
	}
}

func TestAuthStatusSaysWhetherTheConsoleIsClaimed(t *testing.T) {
	// §12: a fresh install must explain itself rather than present a login
	// that refuses every password. The status endpoint is unauthenticated, so
	// this is the only place the SPA can learn it before trying.
	s, _ := testServerFull(t)
	seedTestAccount(t, s)
	r := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	var body struct {
		Authenticated bool `json:"authenticated"`
		Configured    bool `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Authenticated {
		t.Error("an unauthenticated request reported a session")
	}
	if !body.Configured {
		t.Error("a claimed console reported itself unconfigured")
	}
}

func TestListSessionsShowsOnlyTheCallersOwn(t *testing.T) {
	s, _, cookie := newServerWithSession(t) // account u1
	seedSecondAccountWithSession(t, s)      // account u2, cookie "other"

	rec := getJSONAs(t, s, "/api/sessions", cookie)
	if rec.Code != 200 {
		t.Fatalf("GET /api/sessions = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), store.HashSessionID("other")[:sessionIDPrefix]) {
		t.Errorf("another account's session was listed: %s", rec.Body)
	}
	// Two-sided on purpose: an empty listing would satisfy the check above
	// while proving nothing.
	if !strings.Contains(rec.Body.String(), store.HashSessionID(cookie)[:sessionIDPrefix]) {
		t.Errorf("the caller's own session was not listed: %s", rec.Body)
	}
}

func TestRevokingAnotherAccountsSessionIs404(t *testing.T) {
	s, _, cookie := newServerWithSession(t)
	seedSecondAccountWithSession(t, s)

	rec := deleteAs(t, s, "/api/sessions/"+store.HashSessionID("other")[:sessionIDPrefix], cookie)
	if rec.Code != 404 {
		t.Errorf("code = %d, want 404: one account must not reach another's session", rec.Code)
	}
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), "other", sessionTTL); !ok {
		t.Error("the other account's session was revoked")
	}
}

func TestChangingMyPasswordUpdatesMyRowOnly(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)
	hash := mustHash(t, "old-password-here")
	if err := s.deps.DB.SetUserPassword(t.Context(), uid, hash); err != nil {
		t.Fatal(err)
	}
	otherID, _ := seedSecondAccountWithSession(t, s)

	rec := postJSONAs(t, s, "/api/auth/password",
		`{"current":"old-password-here","new":"a-brand-new-password"}`, cookie)
	if rec.Code != 200 {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	u, _, _ := s.deps.DB.UserByID(t.Context(), uid)
	if !VerifyPassword(u.PasswordHash, "a-brand-new-password") {
		t.Error("the new password does not verify against the stored hash")
	}
	// The other account's own hash is untouched.
	other, _, _ := s.deps.DB.UserByID(t.Context(), otherID)
	if !VerifyPassword(other.PasswordHash, "correct-horse-battery") {
		t.Error("a password change rewrote an unrelated account's hash")
	}
	// The other account keeps its session: their password did not change.
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), "other", sessionTTL); !ok {
		t.Error("a password change signed out an unrelated account")
	}
	// The caller keeps the session they are using.
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), cookie, sessionTTL); !ok {
		t.Error("the caller was signed out of the screen they just used")
	}
}

func TestChangingMyPasswordRefusesAWrongCurrent(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)
	if err := s.deps.DB.SetUserPassword(t.Context(), uid, mustHash(t, "old-password-here")); err != nil {
		t.Fatal(err)
	}
	rec := postJSONAs(t, s, "/api/auth/password",
		`{"current":"not-the-password","new":"a-brand-new-password"}`, cookie)
	if rec.Code != 401 {
		t.Errorf("code = %d, want 401", rec.Code)
	}
}

// The environment hash is retired. A deployment that still sets it must get no
// effect at all -- not a login fallback, not a seeded account -- because the
// variable outlives the code that read it in every operator's compose file.
func TestTheEnvironmentHashIsNotRead(t *testing.T) {
	t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", mustHash(t, "correct-horse-battery"))
	s, _ := testServer(t)

	if n, _ := s.deps.DB.UserCount(t.Context()); n != 0 {
		t.Error("the environment hash seeded an account")
	}
	rec := postJSON(t, s, "/api/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != 401 {
		t.Errorf("the environment hash authenticated a login: %d %s", rec.Code, rec.Body)
	}
}
