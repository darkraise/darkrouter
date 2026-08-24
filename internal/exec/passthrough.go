package exec

import (
	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
)

// forwardKinds maps an inbound dialect onto the one adapter kind whose wire
// format it already speaks. Master design §4.1.
//
// openai-responses is deliberately absent. Its passthrough is populated and its
// model field is called model, so nothing else here would stop it — but a
// Responses body carries input where chat/completions expects messages, and
// forwarding one to an openaicompat target is a 400 rather than a fast path.
var forwardKinds = map[string]string{
	"openai":    "openaicompat",
	"anthropic": "anthropic",
	"gemini":    "gemini",
}

// forwardable reports the forwarder for this attempt, and whether the candidate
// is eligible at all.
//
// It is called once per attempt rather than once per request: master design
// §4.1 decides eligibility per candidate, so an Anthropic-inbound request can
// forward to Anthropic and translate to openaicompat on the next attempt.
func forwardable(dialect string, pt *edge.Passthrough, c router.Candidate,
	p provider.Provider, ad adapter.Adapter) (adapter.Forwarder, bool) {

	if pt == nil || pt.Surface != ir.SurfaceLLM {
		return nil, false
	}
	if forwardKinds[dialect] != c.Kind {
		return nil, false
	}
	// Exactly one identifier form. Neither means the dialect declared nothing
	// rewritable; both would mean two places to rewrite and no rule for which
	// wins.
	if (pt.ModelField == "") == (pt.Method == "") {
		return nil, false
	}
	fw, ok := ad.(adapter.Forwarder)
	if !ok {
		// bedrock and vertex land here, by not implementing the interface.
		return nil, false
	}
	if pt.Stream && c.Kind == "openaicompat" && presetRejectsStreamOptions(p.Preset) {
		// The injection spec §5.2 requires would be rejected by this upstream,
		// so its streaming requests take the IR path. Its unary requests are
		// unaffected.
		return nil, false
	}
	return fw, true
}

// presetRejectsStreamOptions mirrors rerankPath and presetStyle in exec.go:
// preset data is reached from here because the adapter is handed a target and
// knows nothing about presets.
func presetRejectsStreamOptions(preset string) bool {
	if preset == "" {
		return false
	}
	return catalog.Embedded()[preset].HasQuirk("rejects-stream-options")
}
