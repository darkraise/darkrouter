package exec

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The authorizer must see a body it can read to the end and a request whose
// body is still replayable afterwards. Signing consumes the body; a retry that
// found it drained would send an empty payload under a valid signature.
func TestAuthorizeRunsOnAMaterializedBody(t *testing.T) {
	hr, err := http.NewRequest("POST", "https://example.invalid/x",
		io.NopCloser(strings.NewReader(`{"a":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := makeReplayable(hr); err != nil {
		t.Fatal(err)
	}

	var sawLen int
	authorizer := func(_ context.Context, r *http.Request) error {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		sawLen = len(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.Header.Set("Authorization", "signed")
		return nil
	}

	if err := applyAuthorizer(context.Background(), hr, authorizer); err != nil {
		t.Fatal(err)
	}
	if sawLen != len(`{"a":1}`) {
		t.Errorf("authorizer saw %d bytes, want %d", sawLen, len(`{"a":1}`))
	}
	if hr.Header.Get("Authorization") != "signed" {
		t.Error("the authorizer's header did not survive")
	}
	replay, err := hr.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	again, err := io.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != `{"a":1}` {
		t.Errorf("replayed body = %q, want the original", again)
	}
}

func TestAuthorizeIsANoOpWhenNil(t *testing.T) {
	hr, err := http.NewRequest("GET", "https://example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyAuthorizer(context.Background(), hr, nil); err != nil {
		t.Fatalf("a nil authorizer must be the static path, got %v", err)
	}
}
