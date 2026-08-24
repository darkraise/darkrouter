package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
		// The stream_options injection that spec §5.2 requires would be
		// rejected by this upstream, so its streaming requests take the IR path.
		// Its unary requests are unaffected.
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

// ErrNoModelField means a body-carried dialect's body has no top-level model
// key to rewrite. The executor falls back to the IR path rather than reporting
// a client error: the IR parser produces a proper dialect-shaped message if the
// body is genuinely invalid, and this function cannot tell the difference.
var ErrNoModelField = errors.New("exec: passthrough body carries no model field")

// rewriteForward applies master design §4.2's permitted body mutations and
// returns the bytes to forward. injected reports whether stream_options was
// added, which is what tells the response forwarder to strip the extra final
// usage chunk the client never asked for.
//
// The guarantee is semantic preservation, not byte preservation: re-encoding
// compacts whitespace, sorts top-level keys and collapses duplicate top-level
// keys to the last. HTML escaping is disabled for the re-encode of the body's
// own values, which is where prompt text lives. When nothing needs changing the
// original slice is returned unmodified, which is the only path with exact byte
// fidelity — and the most travelled one, because a client usually asks for the
// name the target serves.
func rewriteForward(pt *edge.Passthrough, requested, target, kind string) ([]byte, bool, error) {
	if pt.ModelField == "" {
		// URL-carried. The model is not in the body, and no dialect in this
		// group has a stream_options analogue, so the body is forwarded exactly
		// as it arrived.
		return pt.Body, false, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(pt.Body, &top); err != nil {
		return nil, false, err
	}
	if _, ok := top[pt.ModelField]; !ok {
		return nil, false, ErrNoModelField
	}

	changed, injected := false, false
	if requested != target {
		name, err := json.Marshal(target)
		if err != nil {
			return nil, false, err
		}
		top[pt.ModelField] = name
		changed = true
	}
	if kind == "openaicompat" && pt.Stream {
		if _, ok := top["stream_options"]; !ok {
			// Compatible providers report no stream usage unless asked, and
			// without this token accounting is blind on the most-travelled
			// route. Present-but-false is the client's own choice and is left
			// alone, which also leaves the resulting chunks alone.
			top["stream_options"] = json.RawMessage(`{"include_usage":true}`)
			changed, injected = true, true
		}
	}
	if !changed {
		return pt.Body, false, nil
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Without this, <, > and & inside every RawMessage value are escaped —
	// silently rewriting prompt text on every forwarded request.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(top); err != nil {
		return nil, false, err
	}
	// Encode appends a newline that no provider wants in a JSON body.
	return bytes.TrimRight(buf.Bytes(), "\n"), injected, nil
}

// forwardHeaderAllowlist is spec §5.3. Everything else inbound is dropped, so a
// client cannot inject a header into Darkrouter's upstream call — including the
// inbound proxy credential in any of its three dialect spellings, every
// hop-by-hop header RFC 9110 §7.6.1 names, and Darkrouter's own diagnostics.
//
// content-type and openai-beta are here because an earlier draft of the spec
// omitted them: without the first every upstream call goes out untyped and many
// providers reject it, and without the second this phase's fidelity argument
// fails for half its clients.
var forwardHeaderAllowlist = map[string]bool{
	"content-type":      true,
	"accept":            true,
	"user-agent":        true,
	"anthropic-version": true,
	"anthropic-beta":    true,
	"openai-beta":       true,
}

// forwardHeaders is the inbound header set reduced to what may be forwarded.
// Content-Length is deliberately absent: the body may have been rewritten, and
// the builder sets it from the bytes it actually sends.
func forwardHeaders(r *http.Request) http.Header {
	out := make(http.Header, len(forwardHeaderAllowlist))
	for k, vs := range r.Header {
		if !forwardHeaderAllowlist[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			out.Add(k, v)
		}
	}
	return out
}

// compressedBody reports an inbound body Darkrouter will not decode.
//
// Spec §5.4 refuses rather than downgrades: decoding it to rewrite the model
// and re-encoding it defeats the point of the fast path, and forwarding it
// unmodified is impossible when the model must change. The IR path could not
// serve it either — its parser reads the raw bytes — so the refusal costs
// nothing and says something true.
func compressedBody(r *http.Request) bool {
	enc := strings.TrimSpace(r.Header.Get("Content-Encoding"))
	return enc != "" && !strings.EqualFold(enc, "identity")
}
