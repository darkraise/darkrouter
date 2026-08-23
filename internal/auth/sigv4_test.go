package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The known-answer vectors. Both were produced by signing exactly these inputs
// with aws-sdk-go-v2's standalone signer; a canonicalization change anywhere in
// the request builder moves the signature and fails here rather than at a live
// 403 with no cause attached.
const (
	katURL  = "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-5-sonnet-20241022-v2%3A0/converse"
	katBody = `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`

	katAuth = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260824/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-length;content-type;host;x-amz-date, " +
		"Signature=660ed7b1b462eeaeaff156beba4ff25abda5bc3ad8c8bf10cebf4ec2fb4dc740"

	katAuthWithSession = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260824/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token, " +
		"Signature=a1ddcb7dfe82d5ee2f205fa96176dd88c463490e87ea4176f22f4143fad1f035"
)

func katRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", katURL, strings.NewReader(katBody))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

func katTime() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }

func TestSigV4MatchesTheKnownAnswer(t *testing.T) {
	az := signerFor(AWSCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}, "bedrock", "us-east-1", katTime)

	r := katRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("X-Amz-Date"); got != "20260824T120000Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != katAuth {
		t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, katAuth)
	}
}

func TestSigV4CarriesASessionToken(t *testing.T) {
	// Instance-role and assumed-role credentials always carry one, and it is
	// part of the signature rather than an extra header alongside it.
	az := signerFor(AWSCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken:    "sess-token",
	}, "bedrock", "us-east-1", katTime)

	r := katRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("X-Amz-Security-Token"); got != "sess-token" {
		t.Errorf("X-Amz-Security-Token = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != katAuthWithSession {
		t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, katAuthWithSession)
	}
}

func TestSigV4SignsTheBodyItSees(t *testing.T) {
	// The payload hash is part of the signature, so two different bodies must
	// not produce the same one. This is the assertion that fails if a refactor
	// ever signs before the body is materialized.
	sign := func(body string) string {
		r, err := http.NewRequest("POST", katURL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		az := signerFor(AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "k"},
			"bedrock", "us-east-1", katTime)
		if err := az(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		return r.Header.Get("Authorization")
	}
	if sign(katBody) == sign(katBody+" ") {
		t.Fatal("two different bodies produced the same signature")
	}
}

func TestSigV4LeavesTheBodyReadable(t *testing.T) {
	// Signing hashes the body. A signer that consumed it would send an empty
	// payload under a signature computed over the real one.
	r := katRequest(t)
	az := signerFor(AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "k"},
		"bedrock", "us-east-1", katTime)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(katBody))
	n, _ := r.Body.Read(buf)
	if string(buf[:n]) != katBody {
		t.Errorf("body after signing = %q", buf[:n])
	}
}

func TestExplicitAWSCredentialsParse(t *testing.T) {
	got, err := ParseAWSCredentials([]byte(
		`{"access_key_id":"AKID","secret_access_key":"SECRET","session_token":"S"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "AKID" || got.SecretAccessKey != "SECRET" || got.SessionToken != "S" {
		t.Errorf("parsed = %+v", got)
	}
}

func TestIncompleteAWSCredentialsAreRefused(t *testing.T) {
	// A half-filled document signs with an empty secret and produces a 403
	// that reads as a revoked key rather than a configuration mistake.
	if _, err := ParseAWSCredentials([]byte(`{"access_key_id":"AKID"}`)); err == nil {
		t.Fatal("credentials with no secret must be refused")
	}
}

func TestSigV4NeedsARegion(t *testing.T) {
	// Region is an endpoint property on the provider row, spec §3.3. Signing
	// for the wrong one produces a 403 that reads as a bad key, so an absent
	// one is refused up front.
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: StyleSigV4},
		Credential{ID: "k", Kind: "sigv4", Secret: `{"access_key_id":"A","secret_access_key":"B"}`})
	if err == nil {
		t.Fatal("a sigv4 provider with no region must be refused")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("the error should name the cause, got %v", err)
	}
}

func TestSigV4ThroughTheManager(t *testing.T) {
	m := NewManager(Deps{})
	az, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: StyleSigV4, Region: "us-east-1"},
		Credential{ID: "k", Kind: "sigv4",
			Secret: `{"access_key_id":"AKIDEXAMPLE","secret_access_key":"k"}`})
	if err != nil {
		t.Fatal(err)
	}
	r := katRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
}
