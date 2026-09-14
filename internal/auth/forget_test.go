package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// heldRefresh is a token endpoint that holds each refresh until released and
// then answers with body, so a test can act while a refresh is in flight.
func heldRefresh(t *testing.T, body string) (m *Manager, tokens *memTokens, arrived, release chan struct{}) {
	t.Helper()
	arrived, release = make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-release
		if body == `{"error":"invalid_grant"}` {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	tokens = newMemTokens()
	return oauthManager(t, srv, tokens), tokens, arrived, release
}

func replacementSecret(t *testing.T) string {
	t.Helper()
	raw, err := Token{AccessToken: "at-replaced", RefreshToken: "rt-replaced",
		ExpiresAt: time.Now().Add(time.Hour)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// An operator replaces the credential while a refresh of the old one is in
// flight. The refresh answers afterwards with tokens descended from the old
// secret, and writing them over the replacement undid the operator's save.
func TestARefreshInFlightDoesNotOverwriteAReplacement(t *testing.T) {
	m, tokens, arrived, release := heldRefresh(t,
		`{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`)
	old := expiring(t, -time.Minute)
	az := oauthAz(t, m, old)

	done := make(chan error, 1)
	go func() { done <- az(context.Background(), blank(t)) }()
	<-arrived

	replaced := replacementSecret(t)
	tokens.seed("cred-1", replaced)
	m.Forget("cred-1")
	close(release)
	<-done

	if got := tokens.stored("cred-1"); got != replaced {
		t.Fatalf("stored = %s; the refresh of the replaced secret overwrote the replacement", got)
	}
	r := blank(t)
	if err := resolveOAuth(t, m, old)(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-replaced" {
		t.Errorf("Authorization = %q, want the replacement's token", got)
	}
}

// A refusal that arrives for the secret an operator has just replaced says
// nothing about the replacement, and must not disable it.
func TestARefusalOfAReplacedSecretDoesNotDisableTheReplacement(t *testing.T) {
	m, tokens, arrived, release := heldRefresh(t, `{"error":"invalid_grant"}`)
	old := expiring(t, -time.Minute)
	az := oauthAz(t, m, old)

	done := make(chan error, 1)
	go func() { done <- az(context.Background(), blank(t)) }()
	<-arrived

	tokens.seed("cred-1", replacementSecret(t))
	m.Forget("cred-1")
	close(release)
	<-done

	if _, disabled := tokens.disabledReason("cred-1"); disabled {
		t.Fatal("the replacement was disabled for the old secret's refusal")
	}
	r := blank(t)
	if err := resolveOAuth(t, m, old)(context.Background(), r); err != nil {
		t.Fatalf("after the replacement: %v", err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-replaced" {
		t.Errorf("Authorization = %q, want the replacement's token", got)
	}
}

// A refresh is persisted without reloading the router, so the provider set a
// request carries can still hold the pair the refresh rotated away. An account
// rebuilt from that after Forget presented the dead refresh token, which a
// vendor that rotates treats as reuse — and the credential was disabled.
func TestForgetDoesNotReplayARotatedRefreshToken(t *testing.T) {
	a, srv := newAuthServer(t)
	tokens := newMemTokens()
	m := oauthManager(t, srv, tokens)
	snapshot := expiring(t, -time.Minute)

	if err := oauthAz(t, m, snapshot)(context.Background(), blank(t)); err != nil {
		t.Fatal(err)
	}
	m.Forget("cred-1")

	r := blank(t)
	if err := resolveOAuth(t, m, snapshot)(context.Background(), r); err != nil {
		t.Fatalf("after Forget: %v", err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("Authorization = %q, want the persisted rotation's token", got)
	}
	if a.count() != 1 {
		t.Errorf("refreshed %d times, want 1: the rotated-away token was replayed", a.count())
	}
	if _, disabled := tokens.disabledReason("cred-1"); disabled {
		t.Error("the credential was disabled after Forget")
	}
}

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
