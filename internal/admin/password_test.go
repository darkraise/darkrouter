package admin

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/store/storetest"
	"golang.org/x/crypto/bcrypt"
)

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
