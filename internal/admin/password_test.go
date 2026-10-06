package admin

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/store/storetest"
	"golang.org/x/crypto/bcrypt"
)

// The floor is characters, the ceiling bytes. Four CJK characters are twelve
// UTF-8 bytes, which a byte floor took for a twelve-character password; and a
// password of short characters that runs past 72 bytes is still one bcrypt
// would silently truncate.
func TestPasswordProblemCountsCharactersForTheFloorAndBytesForTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, password string
		want           string
	}{
		{"four CJK characters", "密码密码", "at least 12 characters"},
		{"eleven ASCII characters", strings.Repeat("a", 11), "at least 12 characters"},
		{"twelve CJK characters", strings.Repeat("密", 12), ""},
		{"twelve ASCII characters", strings.Repeat("a", 12), ""},
		{"72 bytes exactly", strings.Repeat("a", 72), ""},
		{"25 CJK characters, 75 bytes", strings.Repeat("密", 25), "at most 72 bytes"},
	} {
		got := passwordProblem("the password", tc.password)
		if tc.want == "" && got != "" {
			t.Errorf("%s: refused with %q, want accepted", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to say %q", tc.name, got, tc.want)
		}
	}
}

func TestServerPasswordDefaultsUseProductionCost(t *testing.T) {
	// Deliberately bypass testServer: its private overrides must never change
	// what a normally constructed server uses for writes or unknown users.
	s, err := New(Deps{DB: storetest.Migrated(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	hash, err := s.hashPassword("production-password")
	if err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]string{"account": hash, "unknown user": s.dummyHash} {
		cost, err := bcrypt.Cost([]byte(h))
		if err != nil || cost != 12 {
			t.Errorf("%s bcrypt cost = %d, err = %v; want 12", name, cost, err)
		}
	}
}

func TestAHashedPasswordVerifies(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Error("the correct password did not verify")
	}
	if VerifyPassword(h, "wrong") {
		t.Error("a wrong password verified")
	}
}

func TestAnEmptyHashRefusesEveryPassword(t *testing.T) {
	// An account row with an empty hash must close the admin port, not open
	// it. A helper that returns true here is how a dashboard ends up
	// unauthenticated on a LAN.
	for _, pw := range []string{"", "anything", "admin"} {
		if VerifyPassword("", pw) {
			t.Errorf("an empty hash accepted %q", pw)
		}
	}
}

func TestAMalformedHashRefusesEveryPassword(t *testing.T) {
	// A truncated or hand-edited hash must fail closed.
	for _, h := range []string{"not-a-hash", "$2a$", "$2a$12$tooshort"} {
		if VerifyPassword(h, "anything") {
			t.Errorf("malformed hash %q accepted a password", h)
		}
	}
}

func TestTheHashIsBcryptCostTwelve(t *testing.T) {
	// Spec §3 fixes the cost. A lower one is a silent downgrade that no
	// behavioral test would catch.
	h, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) < 4 || h[:4] != "$2a$" {
		t.Fatalf("hash = %q, want a bcrypt 2a hash", h)
	}
	if h[4:6] != "12" {
		t.Errorf("cost = %q, want 12", h[4:6])
	}
}
