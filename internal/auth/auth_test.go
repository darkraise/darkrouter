package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestStaticStylesAreRecognized(t *testing.T) {
	for _, style := range []string{"bearer", "x-api-key", "api-key", "query-param", "none"} {
		if !IsStatic(style) {
			t.Errorf("%q should be static", style)
		}
	}
	// The empty style is the unconfigured provider row, which falls back to
	// bearer everywhere else and must not be routed to a signer.
	if !IsStatic("") {
		t.Error(`"" should be static`)
	}
	for _, style := range []string{"sigv4", "gcp-sa", "oauth"} {
		if IsStatic(style) {
			t.Errorf("%q must not be static", style)
		}
	}
}

func TestManagerReturnsNilForAStaticStyle(t *testing.T) {
	// nil means "the adapter's own header is correct", which is the whole
	// mechanism: no branch anywhere else has to know about static styles.
	m := NewManager(Deps{})
	a, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: "bearer"},
		Credential{ID: "k", Secret: "sk-x"})
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		t.Error("a static style must not produce an authorizer")
	}
}

func TestManagerRefusesAnUnknownStyle(t *testing.T) {
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: "sigv5"},
		Credential{ID: "k"})
	if err == nil {
		t.Fatal("an unrecognized style must be an error, not a silent bearer")
	}
	if !errors.Is(err, ErrUnsupportedStyle) {
		t.Errorf("error should be ErrUnsupportedStyle, got %v", err)
	}
}

var _ Authorizer = func(context.Context, *http.Request) error { return nil }

func TestOptionalIsStaticAndKeyless(t *testing.T) {
	// Static: the adapter writes the key itself, and writes nothing when there
	// is none — which is the whole of the difference from bearer.
	if !IsStatic(StyleOptional) {
		t.Error("optional must be served by a bare credential, not a signer")
	}
	// Keyless: the router may reach it with no credential at all.
	if !IsKeyless(StyleOptional) || !IsKeyless(StyleNone) {
		t.Error("both styles that serve an unauthenticated request must read as keyless")
	}
}

func TestAnonymousIsStaticAndKeyless(t *testing.T) {
	// The published credential is written by the adapter like any other bare
	// key, and the router reaches the provider without the operator having
	// configured one.
	if !IsStatic(StyleAnonymous) {
		t.Error("anonymous must be served by a bare credential, not a signer")
	}
	if !IsKeyless(StyleAnonymous) {
		t.Error("anonymous must read as keyless: there is nothing to paste")
	}
}

func TestAStyleThatNeedsAKeyIsNotKeyless(t *testing.T) {
	// The guard keyless must not weaken: routing to a bearer provider with no
	// credential would 401 every request.
	for _, style := range []string{"", StyleBearer, StyleXAPIKey, StyleAPIKey, StyleSigV4, StyleOAuth} {
		if IsKeyless(style) {
			t.Errorf("%q read as keyless", style)
		}
	}
}
