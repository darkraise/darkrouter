package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	bedrockadapter "github.com/darkraise/darkrouter/internal/adapter/bedrock"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	vertexadapter "github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
)

func chatPassthrough() *edge.Passthrough {
	return &edge.Passthrough{
		Body: []byte(`{"model":"m","messages":[]}`), ModelField: "model",
		Surface: ir.SurfaceLLM,
	}
}

func TestEligibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect string
		pt      *edge.Passthrough
		cand    router.Candidate
		prov    provider.Provider
		ad      adapter.Adapter
		want    bool
	}{
		{
			name: "openai to openaicompat", dialect: "openai", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: true,
		},
		{
			name: "anthropic to anthropic", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "anthropic"}, ad: anthropicadapter.New(), want: true,
		},
		{
			name:    "gemini to gemini",
			dialect: "gemini",
			pt: &edge.Passthrough{
				Body: []byte(`{"contents":[]}`), Method: "generateContent", Surface: ir.SurfaceLLM,
			},
			cand: router.Candidate{Kind: "gemini"}, ad: geminiadapter.New(), want: true,
		},
		{
			// Cross-dialect is the IR path's entire reason for existing.
			name: "anthropic to openaicompat", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			// SigV4 signs a payload hash: the body must be materialized and
			// signed, which forwarding it cannot be. This test stops earlier at the
			// dialect map: anthropic maps to anthropic, not bedrock.
			name: "bedrock is never eligible", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "bedrock"}, ad: bedrockadapter.New(), want: false,
		},
		{
			// The Vertex URL encodes publisher and model together. This test stops
			// earlier at the dialect map: gemini maps to gemini, not vertex.
			name: "vertex is never eligible", dialect: "gemini",
			pt:   &edge.Passthrough{Body: []byte(`{}`), Method: "generateContent", Surface: ir.SurfaceLLM},
			cand: router.Candidate{Kind: "vertex", Publisher: "google"}, ad: vertexadapter.New(), want: false,
		},
		{
			// This isolates the fourth condition: matching dialect and kind, but the
			// adapter does not implement Forwarder. This is the branch protecting the
			// signed-body exclusion.
			name: "a matching kind whose adapter cannot forward", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "anthropic"}, ad: bedrockadapter.New(), want: false,
		},
		{
			// The Responses body is not a chat-completions body, whatever its
			// model field is called.
			name: "openai-responses is never eligible", dialect: "openai-responses", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name: "an auxiliary surface is never eligible", dialect: "openai",
			pt:   &edge.Passthrough{Body: []byte(`{}`), ModelField: "model", Surface: ir.SurfaceEmbedding},
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name: "no passthrough at all", dialect: "openai", pt: nil,
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name:    "no rewritable identifier",
			dialect: "openai",
			pt:      &edge.Passthrough{Body: []byte(`{}`), Surface: ir.SurfaceLLM},
			cand:    router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name: "both rewritable identifiers", dialect: "openai",
			pt:   &edge.Passthrough{Body: []byte(`{}`), ModelField: "model", Method: "generateContent", Surface: ir.SurfaceLLM},
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := forwardable(tc.dialect, tc.pt, tc.cand, tc.prov, tc.ad)
			if ok != tc.want {
				t.Errorf("forwardable = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestAQuirkDeclaringPresetIsIneligibleForStreamingOnly(t *testing.T) {
	// spec §5.2: injecting stream_options into a provider that rejects it turns
	// a working request into a 400. Its unary requests are unaffected, and
	// excluding those too would give up fidelity for nothing.
	p := provider.Provider{Preset: quirkPresetName(t)}
	c := router.Candidate{Kind: "openaicompat"}

	streaming := chatPassthrough()
	streaming.Stream = true
	if _, ok := forwardable("openai", streaming, c, p, openaicompat.New()); ok {
		t.Error("a rejects-stream-options preset must not stream through the fast path")
	}
	if _, ok := forwardable("openai", chatPassthrough(), c, p, openaicompat.New()); !ok {
		t.Error("its unary requests are still eligible")
	}
}

// quirkPresetName registers a preset declaring rejects-stream-options and
// returns its name.
//
// catalog.Embedded() returns the package-level map itself rather than a copy,
// so an entry added here is visible to presetRejectsStreamOptions. It is
// removed on cleanup: leaving it behind would give every later test in this
// package a preset that does not ship.
func quirkPresetName(t *testing.T) string {
	t.Helper()
	const name = "test-rejects-stream-options"
	catalog.Embedded()[name] = catalog.Preset{
		Name: "Strict", Kind: "openaicompat",
		Quirks: []string{"rejects-stream-options"},
	}
	t.Cleanup(func() { delete(catalog.Embedded(), name) })
	return name
}
