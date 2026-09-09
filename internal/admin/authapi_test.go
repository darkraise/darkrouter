package admin

import (
	"strings"
	"testing"
)

func TestLoginBindsTheSessionToTheAccount(t *testing.T) {
	s, _ := testServer(t)
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "Alice", hash); err != nil {
		t.Fatal(err)
	}

	rec := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"correct-horse-battery"}`)
	if rec.Code != 200 {
		t.Fatalf("login = %d, want 200: %s", rec.Code, rec.Body)
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login set no session cookie")
	}
	uid, ok, err := s.deps.DB.TouchSession(t.Context(), cookie, sessionTTL)
	if err != nil || !ok {
		t.Fatalf("the minted session is not valid: %v ok=%v", err, ok)
	}
	if uid != "u1" {
		t.Errorf("session owner = %q, want u1", uid)
	}
}

func TestLoginRefusesAnUnknownUsernameIdentically(t *testing.T) {
	s, _ := testServer(t)
	hash := mustHash(t, "correct-horse-battery")
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", hash); err != nil {
		t.Fatal(err)
	}

	wrongUser := postJSON(t, s, "/api/auth/login", `{"username":"nobody","password":"correct-horse-battery"}`)
	wrongPass := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"wrong-wrong-wrong"}`)

	if wrongUser.Code != 401 || wrongPass.Code != 401 {
		t.Fatalf("codes = %d and %d, want 401 and 401", wrongUser.Code, wrongPass.Code)
	}
	// Identical wording. An operator who can tell the two apart can enumerate
	// which accounts exist without ever guessing a password.
	if wrongUser.Body.String() != wrongPass.Body.String() {
		t.Errorf("an unknown username is distinguishable from a wrong password:\n  %s\n  %s",
			wrongUser.Body.String(), wrongPass.Body.String())
	}
}

func TestLoginOnAnUnclaimedConsoleIsRefusedIdentically(t *testing.T) {
	// The third case the one message covers. A console nobody has claimed must
	// read the same as a wrong password, or the wording announces an open port.
	s, _ := testServer(t)
	unclaimed := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"correct-horse-battery"}`)
	if unclaimed.Code != 401 {
		t.Fatalf("login against an unclaimed console = %d, want 401: %s", unclaimed.Code, unclaimed.Body)
	}

	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", mustHash(t, "correct-horse-battery")); err != nil {
		t.Fatal(err)
	}
	wrongPass := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"wrong-wrong-wrong"}`)
	if unclaimed.Body.String() != wrongPass.Body.String() {
		t.Errorf("an unclaimed console is distinguishable from a wrong password:\n  %s\n  %s",
			unclaimed.Body.String(), wrongPass.Body.String())
	}
}

func TestLoginComparesAHashEvenWhenTheUsernameIsUnknown(t *testing.T) {
	// Timing is what the identical message would otherwise leak. Asserting the
	// comparison happened is stable; asserting on the clock is not.
	//
	// verifyCalls is process-global, so this test must never run with
	// t.Parallel(): a concurrent login would move the counter for it.
	s, _ := testServer(t)
	hash := mustHash(t, "correct-horse-battery")
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", hash); err != nil {
		t.Fatal(err)
	}
	before := verifyCalls.Load()
	postJSON(t, s, "/api/auth/login", `{"username":"nobody","password":"whatever-1234"}`)
	if verifyCalls.Load() == before {
		t.Error("no password comparison ran for an unknown username; the miss is timeable")
	}
}

func TestAuthStatusReportsConfiguredFromAccounts(t *testing.T) {
	s, _ := testServer(t)

	rec := getJSON(t, s, "/api/auth/status")
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Errorf("an unclaimed console reported configured: %s", rec.Body)
	}
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	rec = getJSON(t, s, "/api/auth/status")
	if !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Errorf("a claimed console reported unconfigured: %s", rec.Body)
	}
}
