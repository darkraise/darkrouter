package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
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
	var got []sessionView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
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
	var listed []sessionView
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
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
	if !VerifyPassword(s.currentPasswordHash(t.Context()), "a-much-longer-password") {
		t.Error("the new password does not verify")
	}
	if VerifyPassword(s.currentPasswordHash(t.Context()), testPassword) {
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

func TestAuthStatusSaysWhetherAPasswordExists(t *testing.T) {
	// §12: a fresh install must explain itself rather than present a login
	// that refuses every password. The status endpoint is unauthenticated, so
	// this is the only place the SPA can learn it before trying.
	s, _ := testServerFull(t)
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
		t.Error("a server with a password hash reported itself unconfigured")
	}
}
