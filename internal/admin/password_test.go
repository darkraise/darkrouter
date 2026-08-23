package admin

import "testing"

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
	// An unconfigured DARKROUTER_ADMIN_PASSWORD_HASH must close the admin
	// port, not open it. A helper that returns true here is how a dashboard
	// ends up unauthenticated on a LAN.
	for _, pw := range []string{"", "anything", "admin"} {
		if VerifyPassword("", pw) {
			t.Errorf("an empty hash accepted %q", pw)
		}
	}
}

func TestAMalformedHashRefusesEveryPassword(t *testing.T) {
	// A truncated or hand-edited value in the environment must fail closed.
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
