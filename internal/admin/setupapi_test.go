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
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Errorf("the claim set %d cookies, want none: %v", len(got), got)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("the claim minted a session; it must not")
		}
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

func TestConcurrentClaimsProduceExactlyOneWinner(t *testing.T) {
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
			case 409:
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
