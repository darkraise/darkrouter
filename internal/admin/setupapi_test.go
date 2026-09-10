package admin

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const claimPassword = "correct-horse-battery"

// postJSONCrossSite is postJSON arriving from a foreign site, which is what a
// cross-site refusal has to be shown against: do sets same-origin.
func postJSONCrossSite(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doSite(t, s, "cross-site", nil, "", "POST", path, body)
}

func TestClaimCreatesTheFoundingAdmin(t *testing.T) {
	s, _ := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"Alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 200 {
		t.Fatalf("claim = %d, want 200: %s", rec.Code, rec.Body)
	}
	u, ok, err := s.deps.DB.UserByUsername(t.Context(), "alice")
	if err != nil || !ok {
		t.Fatalf("no account was created: %v ok=%v", err, ok)
	}
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}
	if !VerifyPassword(u.PasswordHash, claimPassword) {
		t.Error("the stored hash does not verify the password that was set")
	}
}

func TestClaimMintsNoSession(t *testing.T) {
	// The password is spent on a real login immediately afterwards, so one
	// code path issues cookies and the stored hash is exercised before the
	// operator relies on it. With no recovery path, discovering a bad hash now
	// rather than when the session expires is the difference between retyping
	// a password and losing the console.
	s, _ := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	// Asserted before the cookies: a refused claim sets none either, so
	// without this the check below passes on a claim that stopped working.
	if rec.Code != 200 {
		t.Fatalf("claim = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Errorf("the claim set %d cookies, want none: %v", len(got), got)
	}
}

func TestClaimRefusesAMismatchedConfirmation(t *testing.T) {
	s, _ := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"correct-horse-batteryX"}`)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 0 {
		t.Error("a mismatched confirmation created an account")
	}
}

func TestClaimRefusesAShortPassword(t *testing.T) {
	s, _ := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup", `{"username":"alice","password":"short","confirm":"short"}`)
	if rec.Code != 400 {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestClaimRefusesAnEmptyUsername(t *testing.T) {
	s, _ := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"   ","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 400 {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestASecondClaimIsRefused(t *testing.T) {
	s, _ := testServer(t)
	postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"mallory","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

// Eight concurrent claims are answered coherently: exactly one 200 and no 500
// from SQLite contention. It does not prove the INSERT is atomic -- bcrypt
// staggers the requests far enough apart that they never collide inside the
// store. TestConcurrentClaimsCreateOneUser in internal/store/users_test.go is
// the guard for that.
//
// A loser is a 409 or a 429. Both are refusals of the same claim: the claim
// endpoint shares the global bcrypt ceiling with login, and eight racers
// against four slots means some are turned away before they hash rather than
// after. What matters here is that no racer is left believing it won.
func TestConcurrentClaimsAreAnsweredCoherently(t *testing.T) {
	s, _ := testServer(t)
	const racers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		lost int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := postJSON(t, s, "/api/auth/setup",
				`{"username":"racer`+string(rune('a'+i))+`","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case 200:
				won++
			case 409, 429:
				lost++
			default:
				t.Errorf("unexpected code %d: %s", rec.Code, rec.Body)
			}
		}(i)
	}
	wg.Wait()
	if won != 1 {
		t.Errorf("winners = %d, want exactly 1", won)
	}
	if lost != racers-1 {
		t.Errorf("losers = %d, want %d", lost, racers-1)
	}
	// The account count is the claim that matters and the recorders cannot
	// make it: one winner among the responses would still be wrong if two rows
	// had landed.
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

// TestAClaimedConsoleRefusesASecondClaimWithoutHashing pins the cheap refusal
// in front of the expensive one. Without it every claim arriving at a console
// somebody claimed months ago -- an unauthenticated request anyone who can
// reach the port may send -- pays for a cost-12 bcrypt before the INSERT is
// allowed to say no.
//
// hashCalls is process-global, so this test must never run with t.Parallel():
// a concurrent claim or password change would move the counter for it.
func TestAClaimedConsoleRefusesASecondClaimWithoutHashing(t *testing.T) {
	s, _ := testServer(t)
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", mustHash(t, claimPassword)); err != nil {
		t.Fatal(err)
	}
	before := hashCalls.Load()
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"mallory","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409: %s", rec.Code, rec.Body)
	}
	if n := hashCalls.Load() - before; n != 0 {
		t.Errorf("the refused claim ran %d bcrypt hashes, want 0", n)
	}
}

// TestAClaimIsRefusedWhenEveryHashSlotIsBusy holds the global ceiling closed
// and shows the claim declining to hash anyway. The per-address bucket cannot
// stand in for this: a flood spread across addresses empties none of them,
// which is the whole reason loginConcurrency exists.
func TestAClaimIsRefusedWhenEveryHashSlotIsBusy(t *testing.T) {
	s, _ := testServer(t)
	for i := 0; i < loginConcurrency; i++ {
		release, ok := s.logins.acquire()
		if !ok {
			t.Fatalf("slot %d was already taken", i)
		}
		t.Cleanup(release)
	}
	before := hashCalls.Load()
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 429 {
		t.Fatalf("code = %d, want 429: %s", rec.Code, rec.Body)
	}
	if n := hashCalls.Load() - before; n != 0 {
		t.Errorf("the shed claim ran %d bcrypt hashes, want 0", n)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 0 {
		t.Error("a shed claim created an account")
	}
}

func TestClaimIsRefusedCrossSite(t *testing.T) {
	s, _ := testServer(t)
	rec := postJSONCrossSite(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cross-site") {
		t.Errorf("body = %s", rec.Body)
	}
}
