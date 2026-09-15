package vertex

import (
	"context"
	"strings"
	"testing"
)

// Vertex's rawPredict route reads Anthropic beta opt-ins from the same
// anthropic-beta header the direct API does; Anthropic's own Vertex client
// sends it unchanged. A rebuilt request without it silently loses the feature.
func TestAnthropicBetaHeaderReachesVertex(t *testing.T) {
	r := req()
	r.Metadata = map[string]string{"anthropic_beta": "context-1m-2025-08-07,interleaved-thinking-2025-05-14"}
	hr, _ := build(t, anthropicTarget(), r)
	if got := hr.Header.Get("anthropic-beta"); got != r.Metadata["anthropic_beta"] {
		t.Errorf("anthropic-beta = %q, want %q", got, r.Metadata["anthropic_beta"])
	}

	hr, _ = build(t, anthropicTarget(), req())
	if _, set := hr.Header["Anthropic-Beta"]; set {
		t.Errorf("anthropic-beta = %q sent for a request that asked for no beta", hr.Header.Get("anthropic-beta"))
	}
}

// Vertex answers a beta it does not know with a 400 for the whole request,
// and clients such as Claude Code send first-party-only betas by default.
func TestAnthropicBetasVertexDoesNotAcceptAreDropped(t *testing.T) {
	r := req()
	r.Metadata = map[string]string{
		"anthropic_beta": "oauth-2025-04-20, context-1m-2025-08-07,prompt-caching-scope-2026-01-05 ,context-management-2025-06-27",
	}
	hr, warns, err := New().BuildRequest(context.Background(), anthropicTarget(), r)
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.Header.Get("anthropic-beta"); got != "context-1m-2025-08-07,context-management-2025-06-27" {
		t.Errorf("anthropic-beta = %q", got)
	}
	var dropped []string
	for _, w := range warns {
		if w.Field == "anthropic-beta" {
			dropped = append(dropped, w.Reason)
		}
	}
	if len(dropped) != 2 || !strings.Contains(dropped[0], "oauth-2025-04-20") ||
		!strings.Contains(dropped[1], "prompt-caching-scope-2026-01-05") {
		t.Errorf("warnings = %+v; each dropped beta is named", warns)
	}

	r.Metadata = map[string]string{"anthropic_beta": "oauth-2025-04-20"}
	hr, _, err = New().BuildRequest(context.Background(), anthropicTarget(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := hr.Header["Anthropic-Beta"]; set {
		t.Errorf("anthropic-beta = %q; nothing Vertex accepts was asked for", hr.Header.Get("anthropic-beta"))
	}
}
