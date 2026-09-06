package provider

import (
	"strings"
	"testing"
)

func TestABaseURLWithNoPlaceholderIsUnchanged(t *testing.T) {
	got, err := ResolveBaseURL("https://api.groq.com/openai/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.groq.com/openai/v1" {
		t.Errorf("ResolveBaseURL = %q", got)
	}
}

func TestThePlaceholderTakesTheCredentialsAccount(t *testing.T) {
	got, err := ResolveBaseURL("https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://api.cloudflare.com/client/v4/accounts/abc123/ai/v1"
	if got != want {
		t.Errorf("ResolveBaseURL = %q, want %q", got, want)
	}
}

// Snowflake's placeholder is the hostname, so an unresolved one would send the
// request to a host literally named "{account_id}". Failing is the only safe
// answer.
//
// The message matters as much as the failure: "you have not supplied one" and
// "what you supplied is not one" send an operator to different places, and the
// pattern below would reject an empty string either way.
func TestAMissingAccountSaysItIsMissingRatherThanMalformed(t *testing.T) {
	_, err := ResolveBaseURL("https://{account_id}.snowflakecomputing.com/api/v2", "")
	if err == nil {
		t.Fatal("a base URL with an unfilled placeholder was accepted")
	}
	if !strings.Contains(err.Error(), "carries none") {
		t.Errorf("a missing account reads as malformed: %v", err)
	}
}

// The value lands in a hostname for one provider and a path for another, so a
// separator in it is a redirect to somewhere the operator did not configure.
func TestAnAccountWithASeparatorIsRefused(t *testing.T) {
	for _, bad := range []string{
		"abc/../../evil",
		"evil.com/x",
		"abc def",
		"abc?x=1",
		"abc@evil.com",
		"abc#frag",
		"abc:8080",
	} {
		if _, err := ResolveBaseURL("https://{account_id}.example.com/v1", bad); err == nil {
			t.Errorf("account %q was accepted into a hostname", bad)
		}
	}
}

func TestAnOrdinaryAccountIdentifierIsAccepted(t *testing.T) {
	for _, ok := range []string{"abc123", "a1b2-c3d4", "my_org.eu-west-1", "ACME-1"} {
		if _, err := ResolveBaseURL("https://{account_id}.example.com/v1", ok); err != nil {
			t.Errorf("account %q was refused: %v", ok, err)
		}
	}
}

// An account id supplied where the URL has no placeholder is not an error --
// it is a credential carrying a field this provider stopped needing.
func TestAnUnusedAccountIsIgnored(t *testing.T) {
	got, err := ResolveBaseURL("https://api.groq.com/openai/v1", "abc123")
	if err != nil || got != "https://api.groq.com/openai/v1" {
		t.Errorf("ResolveBaseURL = %q, %v", got, err)
	}
}

func TestNeedsAccountReportsThePlaceholder(t *testing.T) {
	if !NeedsAccount("https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1") {
		t.Error("a templated base URL does not report needing an account")
	}
	if NeedsAccount("https://api.groq.com/openai/v1") {
		t.Error("a plain base URL reports needing an account")
	}
}
