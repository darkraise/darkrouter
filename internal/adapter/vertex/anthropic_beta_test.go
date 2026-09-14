package vertex

import "testing"

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
