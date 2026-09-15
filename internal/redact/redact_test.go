package redact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// A transport error quotes the request URL in full, and a query-param
// credential is part of it. Wrapping does not help: fmt.Errorf copies the text
// when it is created, so the key survives in every error built on top.
func TestAQueryStringIsStrippedFromATransportError(t *testing.T) {
	const secret = "sk-live/secret+value="
	u := "https://api.example/v1/models?key=" + url.QueryEscape(secret) + "&page=2"
	err := fmt.Errorf("list models: %w",
		&url.Error{Op: "Get", URL: u, Err: context.DeadlineExceeded})

	got := Error(err)
	if strings.Contains(got.Error(), "key=") || strings.Contains(got.Error(), url.QueryEscape(secret)) {
		t.Errorf("message %q still carries the query string", got)
	}
	if want := `list models: Get "https://api.example/v1/models": context deadline exceeded`; got.Error() != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Error("the redacted error no longer wraps what it wrapped, so a timeout classifies as something else")
	}
	var ue *url.Error
	if !errors.As(got, &ue) {
		t.Error("the redacted error no longer exposes the *url.Error")
	}
}

func TestASecretIsRedactedInEveryEscapedForm(t *testing.T) {
	const secret = `sk-live/se"cret+va\lue=`
	for name, form := range map[string]string{
		"raw":         secret,
		"query":       url.QueryEscape(secret),
		"path":        url.PathEscape(secret),
		"quoted":      strconv.Quote(secret),
		"quoted path": strconv.Quote(url.PathEscape(secret)),
	} {
		t.Run(name, func(t *testing.T) {
			got := Error(errors.New("provider said: "+form+" is not valid"), secret).Error()
			for _, leak := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), "se\"cret", `se\"cret`} {
				if strings.Contains(got, leak) {
					t.Errorf("message %q still carries the credential as %q", got, leak)
				}
			}
			if !strings.HasPrefix(got, "provider said: ") || !strings.HasSuffix(got, " is not valid") {
				t.Errorf("message %q lost the text around the credential", got)
			}
		})
	}
}

// Replacing a short secret everywhere it appears rewrites ordinary words: a
// key of "rate" would turn "rate limited" into nonsense and hide the reason.
func TestATriviallyShortSecretLeavesTheMessageAlone(t *testing.T) {
	err := errors.New("upstream returned 429: rate limited")
	if got := Error(err, "rate", ""); got.Error() != err.Error() {
		t.Errorf("message = %q, want it unchanged", got)
	}
}

func TestAnErrorWithNothingToRedactIsReturnedAsIs(t *testing.T) {
	if Error(nil, "sk-live-secret") != nil {
		t.Error("nil did not stay nil")
	}
	err := errors.New("connection refused")
	if got := Error(err, "sk-live-secret"); got != err {
		t.Errorf("Error = %#v, want the same error back", got)
	}
}
