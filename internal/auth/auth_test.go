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
