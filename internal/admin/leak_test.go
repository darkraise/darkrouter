package admin

import (
	"strings"
	"testing"
)

// theSecret is deliberately shaped so a substring match cannot produce a false
// positive against ordinary JSON.
const theSecret = "sk-DARKROUTERLEAKCANARY-9f3a7c1e"

func TestTheMaskShowsOnlyASuffix(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	_ = do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`)

	got := do(t, s, cookie, token, "GET", "/api/providers", "").Body.String()
	if !strings.Contains(got, theSecret[len(theSecret)-4:]) {
		t.Errorf("the masked suffix is missing; two keys cannot be told apart:\n%s", got)
	}
}

func TestAShortSecretIsMaskedEntirely(t *testing.T) {
	// Showing three of four characters is not a mask.
	if got := maskSecret("abcd"); strings.Contains(got, "abc") {
		t.Errorf("maskSecret(%q) = %q", "abcd", got)
	}
	if got := maskSecret(""); got != "****" {
		t.Errorf("maskSecret(\"\") = %q", got)
	}
}
