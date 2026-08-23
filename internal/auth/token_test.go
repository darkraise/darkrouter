package auth

import (
	"testing"
	"time"
)

func TestTokenRoundTrips(t *testing.T) {
	want := Token{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		Scopes:       []string{"user:inference"},
	}
	raw, err := want.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("tokens did not survive: %+v", got)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("expiry = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "user:inference" {
		t.Errorf("scopes = %v", got.Scopes)
	}
}

func TestExpiredUsesTheDelta(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	tok := Token{ExpiresAt: now.Add(2 * time.Minute)}

	// Inside the delta: still valid by the clock, but a request started now
	// could easily arrive after it expires. Refreshing early is the whole
	// point of the delta.
	if !tok.Expired(now, 5*time.Minute) {
		t.Error("a token inside the refresh delta must count as expired")
	}
	if tok.Expired(now, time.Minute) {
		t.Error("a token outside the delta must not")
	}
}

func TestAZeroExpiryNeverExpires(t *testing.T) {
	// Some vendors issue no expiry at all. Treating the zero time as "expired
	// in 1970" would refresh on every single request.
	tok := Token{AccessToken: "at"}
	if tok.Expired(time.Now(), time.Minute) {
		t.Error("a token with no stated expiry must not be treated as expired")
	}
}

func TestParseRefusesGarbage(t *testing.T) {
	if _, err := ParseToken([]byte("not json")); err == nil {
		t.Fatal("a malformed document must be an error")
	}
}
