package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A terminal refusal marks the account dead so the endpoint is not called
// again. The mark is process-lifetime, and nothing outside the manager could
// clear it: an operator who reconnected the same credential row watched it go
// on failing with no way out but a restart.
func TestForgetLetsARefusedCredentialAuthorizeAgain(t *testing.T) {
	a, srv := newAuthServer(t)
	m := oauthManager(t, srv, newMemTokens())

	a.status, a.errBody = 400, `{"error":"invalid_grant"}`
	az := oauthAz(t, m, expiring(t, -time.Minute))
	if err := az(context.Background(), blank(t)); !errors.Is(err, ErrNeedsReconnect) {
		t.Fatalf("first refresh error = %v, want a terminal refusal", err)
	}

	// The operator reconnects: the row carries a working token again.
	a.status, a.errBody = 0, ""
	m.Forget("cred-1")

	az = oauthAz(t, m, expiring(t, time.Hour))
	r := blank(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatalf("after Forget: %v; a replaced credential must not inherit the "+
			"old one's terminal refusal", err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-0" {
		t.Errorf("Authorization = %q, want the new credential's token", got)
	}
}

// Without Forget the cached account is keyed by credential id alone, so a
// replaced secret keeps presenting the token the old one minted.
func TestForgetDropsACachedToken(t *testing.T) {
	_, srv := newAuthServer(t)
	m := oauthManager(t, srv, newMemTokens())

	az := oauthAz(t, m, expiring(t, time.Hour))
	if err := az(context.Background(), blank(t)); err != nil {
		t.Fatal(err)
	}

	replaced := Token{AccessToken: "at-replaced", RefreshToken: "rt-9",
		ExpiresAt: time.Now().Add(time.Hour)}
	raw, err := replaced.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	m.Forget("cred-1")

	r := blank(t)
	if err := oauthAz(t, m, string(raw))(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-replaced" {
		t.Errorf("Authorization = %q, want the replaced credential's token", got)
	}
}
