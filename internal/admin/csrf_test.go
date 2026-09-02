package admin

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func csrfFor(t *testing.T) (*CSRF, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	c, err := NewCSRF(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return c, db
}

func TestATokenValidatesForItsOwnSession(t *testing.T) {
	c, _ := csrfFor(t)
	tok := c.Token("sess-1")
	if tok == "" {
		t.Fatal("empty token")
	}
	if !c.Valid("sess-1", tok) {
		t.Error("a token did not validate for its own session")
	}
}

func TestATokenDoesNotValidateForAnotherSession(t *testing.T) {
	// This is the entire point of binding. A token an attacker obtained or
	// planted is useless against the session it arrives with.
	c, _ := csrfFor(t)
	tok := c.Token("sess-1")
	if c.Valid("sess-2", tok) {
		t.Error("a token from one session validated against another")
	}
}

func TestGarbageDoesNotValidate(t *testing.T) {
	c, _ := csrfFor(t)
	for _, tok := range []string{"", "not-base64!!", "YWJj"} {
		if c.Valid("sess-1", tok) {
			t.Errorf("token %q validated", tok)
		}
	}
}

func TestTheTokenIsStableAcrossRestarts(t *testing.T) {
	// The secret lives in settings rather than in the process. A per-process
	// secret would invalidate every outstanding token on every deploy and log
	// the operator out for no reason.
	c1, db := csrfFor(t)
	tok := c1.Token("sess-1")

	c2, err := NewCSRF(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Valid("sess-1", tok) {
		t.Error("a token minted before a restart did not survive it")
	}
}

func TestTwoDatabasesDoNotShareASecret(t *testing.T) {
	// A secret that was somehow a constant would make every deployment's
	// tokens interchangeable, which is the failure this test would catch.
	c1, _ := csrfFor(t)
	c2, _ := csrfFor(t)
	if c1.Token("sess-1") == c2.Token("sess-1") {
		t.Error("two independent databases produced the same token")
	}
}
