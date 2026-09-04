// The differential suite runs one corpus through both request paths and
// compares them. Spec §10: the IR path is the correctness baseline, and the
// fast path is validated by proving it agrees with that baseline.
//
// It deliberately does not assert on a known IR-path fidelity gap recorded in
// docs/plan/status.md: the IR path emits a usage chunk the client never asked
// for. The sibling defect — that chunk carrying a synthesized choice where
// OpenAI emits an empty array — has since been fixed.
package golden

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// forwardableDialects maps a corpus dialect onto the kind it passes through to.
// The corpus's responses/ and streams/ directories are canned upstreams rather
// than inbound bodies, and are read by name.
var forwardableDialects = map[string]string{
	"openai":    "openaicompat",
	"anthropic": "anthropic",
	"gemini":    "gemini",
}

// unforwardable hides a kind's Forwarder implementation so the same adapter can
// serve as the IR-path baseline against the same upstream. Embedding the
// interface rather than the concrete type is what drops the extra methods.
//
// This is how the suite drives both paths without a production flag that exists
// only for tests — the eligibility predicate declines because the adapter no
// longer claims it can forward, which is the real reason bedrock declines too.
type unforwardable struct{ adapter.Adapter }

// capture keeps the one record a run produces.
type capture struct{ rec *store.RequestRecord }

func (c *capture) Log(r *store.RequestRecord) { c.rec = r }

type pathResult struct {
	UpstreamBody []byte
	ClientBody   []byte
	ClientStatus int
	Record       *store.RequestRecord
}

// executorFor builds an executor over one provider of the given kind. When
// forward is false every adapter is wrapped so none can forward.
func executorFor(t *testing.T, kind, upstreamURL string, forward bool, cap *capture) *exec.Executor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: up\n    kind: " + kind + "\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [target-model]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk-upstream", true })
	if err != nil {
		t.Fatal(err)
	}
	ads := map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		// The same offline fetcher the rest of this package uses: no golden
		// test makes an outbound request for media.
		"gemini": geminiadapter.NewWithFetcher(&offlineFetcher),
	}
	if !forward {
		hidden := make(map[string]adapter.Adapter, len(ads))
		for k, ad := range ads {
			hidden[k] = unforwardable{ad}
		}
		ads = hidden
	}
	return exec.New(cfgStore, provider.NewYAMLSource(cfgStore), ads, exec.Deps{Log: cap})
}

// dialectFor returns the inbound dialect, per request where it is
// request-scoped. Gemini reads ?alt=sse, which decides its stream wire form.
func dialectFor(dialect string, r *http.Request) edge.Dialect {
	if dialect == "gemini" {
		return geminiedge.NewFor(r)
	}
	return dialects()[dialect]
}

// bothPaths drives one inbound body twice against the same canned upstream:
// once forwarding, once translating.
func bothPaths(t *testing.T, dialect, kind string, m meta, inbound []byte,
	upstream http.HandlerFunc) (fast, irp pathResult) {

	t.Helper()
	for _, run := range []struct {
		forward bool
		out     *pathResult
	}{{true, &fast}, {false, &irp}} {
		var got pathResult
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got.UpstreamBody, _ = io.ReadAll(r.Body)
			upstream(w, r)
		}))
		cap := &capture{}
		e := executorFor(t, kind, srv.URL, run.forward, cap)

		req := requestFor(t, dialect, m, inbound)
		w := httptest.NewRecorder()
		e.Handle(w, req, dialectFor(dialect, req))
		srv.Close()

		got.ClientBody, got.ClientStatus, got.Record = w.Body.Bytes(), w.Code, cap.rec
		if got.Record == nil {
			t.Fatalf("no request record for %s/%s (forward=%v)", dialect, kind, run.forward)
		}

		// Every comparison downstream is only meaningful if each run actually
		// took the path it was set up to take. Without this, two identical
		// failures — or the fast run silently falling back to the IR
		// rendering — would read as agreement.
		wantPath := exec.PathIR
		if run.forward {
			wantPath = exec.PathPassthrough
		}
		if len(got.Record.Attempts) == 0 {
			t.Fatalf("%s/%s (forward=%v): no attempts recorded", dialect, kind, run.forward)
		}
		if gotPath := got.Record.Attempts[len(got.Record.Attempts)-1].Path; gotPath != wantPath {
			t.Fatalf("%s/%s (forward=%v): final attempt took path %q, want %q",
				dialect, kind, run.forward, gotPath, wantPath)
		}
		*run.out = got
	}
	return fast, irp
}

// serveBytes answers with a fixed body, which is how both paths are given
// identical upstream responses.
func serveBytes(body []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
}

// minimalBody is the smallest valid inbound body per dialect, used when the
// fixture under test is the upstream response rather than the request.
func minimalBody(dialect string) []byte {
	switch dialect {
	case "anthropic":
		return []byte(`{"model":"target-model","max_tokens":16,` +
			`"messages":[{"role":"user","content":"hi"}]}`)
	case "gemini":
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	default:
		return []byte(`{"model":"target-model","messages":[{"role":"user","content":"hi"}]}`)
	}
}

func streamingBody(dialect string) []byte {
	if dialect == "gemini" {
		// Gemini's stream flag is the URL operation, not a body field.
		return minimalBody(dialect)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(minimalBody(dialect), &top)
	top["stream"] = json.RawMessage("true")
	out, _ := json.Marshal(top)
	return out
}

// streamMeta is the Gemini path a streaming corpus run needs; the other two
// dialects carry no path value.
//
// alt=sse selects Gemini's event-stream wire form. The array form has no
// event boundaries for the fast-path recognizer to find and is therefore
// never passthrough-eligible, so exercising it here would make the "fast"
// run fall back to the IR path on both legs of the comparison.
func streamMeta(dialect string) meta {
	if dialect == "gemini" {
		return meta{Path: "models/target-model:streamGenerateContent?alt=sse"}
	}
	return meta{}
}

// unaryMeta is the Gemini path a non-streaming corpus run needs — the model
// lives in the URL, not the body, so without it routing never finds
// target-model. The other two dialects carry no path value.
func unaryMeta(dialect string) meta {
	if dialect == "gemini" {
		return meta{Path: "models/target-model:generateContent"}
	}
	return meta{}
}

func topLevelKeys(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body is not an object: %s", body)
	}
	out := make(map[string]bool, len(top))
	for k := range top {
		out[k] = true
	}
	return out
}

func topLevelValue(t *testing.T, body []byte, key string) (any, bool) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body is not an object: %s", body)
	}
	raw, ok := top[key]
	if !ok {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v, true
}

// warnedAbout reports whether the IR path's own record explains a projection
// divergence for the named field — spec §10's carve-out for a value the IR
// cannot model verbatim, such as an unrecognized stop reason normalized to a
// known one. ir.Warning.String() renders as "field -> target: reason".
func warnedAbout(warnings []string, field string) bool {
	for _, w := range warnings {
		if strings.HasPrefix(w, field+" ->") {
			return true
		}
	}
	return false
}

func TestDifferentialUpstreamRequests(t *testing.T) {
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, dialect) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				m := readMeta(t, dir)
				inbound := readFixture(t, filepath.Join(dir, "request.json"))
				fast, irp := bothPaths(t, dialect, kind, m, inbound, cannedUnary(kind))

				// 1. The forwarded body is the inbound body. Every fixture
				// names target-model, which is the target's name for it, so no
				// rewrite fires and the bytes are identical.
				if !bytes.Equal(fast.UpstreamBody, inbound) {
					t.Errorf("the forwarded body is not the inbound body\n got: %s\nwant: %s",
						fast.UpstreamBody, inbound)
				}

				// 2. Neither path invented a top-level field.
				fastKeys := topLevelKeys(t, fast.UpstreamBody)
				for k := range topLevelKeys(t, irp.UpstreamBody) {
					if k == "stream_options" {
						continue // master design §4.2's third permitted mutation
					}
					if !fastKeys[k] {
						t.Errorf("the IR path sent a top-level %q the client did not", k)
					}
				}

				// 3. The scalars neither path transforms agree.
				for _, k := range []string{"model", "max_tokens", "temperature",
					"top_p", "stream", "stop_sequences"} {
					a, aok := topLevelValue(t, fast.UpstreamBody, k)
					b, bok := topLevelValue(t, irp.UpstreamBody, k)
					if aok != bok || (aok && !reflect.DeepEqual(a, b)) {
						t.Errorf("%s differs: passthrough %v (%v), ir %v (%v)", k, a, aok, b, bok)
					}
				}
			})
		}
	}
}

func TestDifferentialClientResponses(t *testing.T) {
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, filepath.Join("responses", kind)) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "response.json"))
				fast, irp := bothPaths(t, dialect, kind, unaryMeta(dialect), minimalBody(dialect),
					serveBytes(canned, "application/json"))

				// The client received the provider's own bytes. This is the
				// phase, stated as one assertion.
				if !bytes.Equal(bytes.TrimSpace(fast.ClientBody), bytes.TrimSpace(canned)) {
					t.Errorf("the client did not receive the provider's bytes\n got: %s\nwant: %s",
						fast.ClientBody, canned)
				}
				// And the IR path agrees on everything the IR models.
				a, b := project(t, dialect, fast.ClientBody), project(t, dialect, irp.ClientBody)
				aRest, bRest := a, b
				aRest.Stop, bRest.Stop = "", ""
				if !reflect.DeepEqual(aRest, bRest) {
					t.Errorf("projections differ\npassthrough: %+v\n        ir: %+v", a, b)
				}
				// Spec §10: the corpus deliberately includes bodies carrying
				// fields the IR does not model, where the two paths are
				// expected to differ — those cases assert passthrough
				// preserved the field and the IR path recorded a warning
				// about dropping it, not that the two agree.
				if a.Stop != b.Stop && !warnedAbout(irp.Record.Warnings, "stop_reason") {
					t.Errorf("stop reason differs with no recorded warning: passthrough %q, ir %q",
						a.Stop, b.Stop)
				}
			})
		}
	}
}

func TestDifferentialUsageAgrees(t *testing.T) {
	// spec §11: usage accounting agrees between the two paths across the whole
	// corpus, including Anthropic cache tokens and OpenAI streamed usage.
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, filepath.Join("streams", kind)) {
			t.Run("stream/"+dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "upstream.sse"))
				fast, irp := bothPaths(t, dialect, kind, streamMeta(dialect),
					streamingBody(dialect), serveBytes(canned, "text/event-stream"))
				if bothSucceeded(t, fast, irp) {
					assertSameUsage(t, fast.Record, irp.Record)
				}
				if kind == "openaicompat" {
					// The canned SSE reports usage regardless of whether the
					// injection ran, so the token-count agreement above would
					// stay green even with §5.2's injection deleted. This is
					// what actually pins the injection to the forwarded body.
					assertStreamOptionsInjected(t, fast.UpstreamBody)
				}
			})
		}
		for _, dir := range caseDirs(t, filepath.Join("responses", kind)) {
			t.Run("unary/"+dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "response.json"))
				fast, irp := bothPaths(t, dialect, kind, unaryMeta(dialect), minimalBody(dialect),
					serveBytes(canned, "application/json"))
				if bothSucceeded(t, fast, irp) {
					assertSameUsage(t, fast.Record, irp.Record)
				}
			})
		}
	}
}

// bothSucceeded is the usage comparison's gate. Three rules:
//
//  1. The fast run must always have succeeded. It forwards the provider's own
//     status, and every canned fixture in this corpus serves 200, so anything
//     else means Darkrouter itself failed, not a legitimate divergence.
//  2. Usage is compared only when both runs succeeded — a token count from a
//     run that deliberately errored is not comparable to anything.
//  3. When the IR run did not succeed, its record must carry a non-empty
//     ErrorCode. That is what makes skipping the comparison legitimate rather
//     than a silent pass: it proves the IR path declined this body on
//     purpose — Gemini's blocked-prompt shape is the recorded case, where the
//     IR path synthesizes a 400 and the fast path forwards Google's raw 200,
//     both deliberately — and it is the record's own word for why, not this
//     test's guess.
func bothSucceeded(t *testing.T, fast, irp pathResult) bool {
	t.Helper()
	if fast.ClientStatus != http.StatusOK {
		t.Fatalf("passthrough run did not succeed: status %d, body %s", fast.ClientStatus, fast.ClientBody)
	}
	if irp.ClientStatus == http.StatusOK {
		return true
	}
	if irp.Record.ErrorCode == "" {
		t.Fatalf("ir run failed with status %d and no recorded ErrorCode: body %s",
			irp.ClientStatus, irp.ClientBody)
	}
	return false
}

// assertStreamOptionsInjected checks spec §5.2's claim directly, on the
// forwarded body the fake provider actually received, rather than inferring
// it from usage numbers a canned fixture reports unconditionally.
func assertStreamOptionsInjected(t *testing.T, upstreamBody []byte) {
	t.Helper()
	v, ok := topLevelValue(t, upstreamBody, "stream_options")
	if !ok {
		t.Fatalf("stream_options was not injected into the forwarded body: %s", upstreamBody)
	}
	opts, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("stream_options is not an object: %v", v)
	}
	if include, _ := opts["include_usage"].(bool); !include {
		t.Fatalf("stream_options.include_usage is not true: %v", opts)
	}
}

func assertSameUsage(t *testing.T, fast, irp *store.RequestRecord) {
	t.Helper()
	for _, f := range []struct {
		name        string
		got, wanted int64
	}{
		{"tokens_in", fast.TokensIn, irp.TokensIn},
		{"tokens_out", fast.TokensOut, irp.TokensOut},
		{"cache_read", fast.CacheReadTokens, irp.CacheReadTokens},
		{"cache_write", fast.CacheWriteTokens, irp.CacheWriteTokens},
		{"reasoning", fast.ReasoningTokens, irp.ReasoningTokens},
	} {
		if f.got != f.wanted {
			t.Errorf("%s: passthrough %d, ir %d", f.name, f.got, f.wanted)
		}
	}
}

// cannedUnary is the smallest valid response each kind can return. The request
// comparison does not read it; it exists so the attempt completes.
func cannedUnary(kind string) http.HandlerFunc {
	switch kind {
	case "anthropic":
		return serveBytes([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`),
			"application/json")
	case "gemini":
		return serveBytes([]byte(`{"candidates":[{"content":{"role":"model",
			"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`),
			"application/json")
	default:
		return serveBytes([]byte(`{"id":"c","object":"chat.completion","model":"target-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`),
			"application/json")
	}
}

// projection is what the IR models about a response, read back out of whatever
// the client actually received. Comparing on this rather than on bytes is spec
// §10's rule: the two paths are expected to differ in field order, in fields
// the IR does not carry, and in chunk boundaries.
type projection struct {
	Text  string
	Stop  string
	Model string
	In    float64
	Out   float64
}

func project(t *testing.T, dialect string, body []byte) projection {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("client body is not JSON: %s", body)
	}
	dig := func(path ...any) any {
		cur := any(v)
		for _, p := range path {
			switch k := p.(type) {
			case string:
				m, ok := cur.(map[string]any)
				if !ok {
					return nil
				}
				cur = m[k]
			case int:
				a, ok := cur.([]any)
				if !ok || k >= len(a) {
					return nil
				}
				cur = a[k]
			}
		}
		return cur
	}
	str := func(x any) string { s, _ := x.(string); return s }
	num := func(x any) float64 { n, _ := x.(float64); return n }

	switch dialect {
	case "anthropic":
		return projection{
			Text:  str(dig("content", 0, "text")),
			Stop:  str(dig("stop_reason")),
			Model: str(dig("model")),
			In:    num(dig("usage", "input_tokens")),
			Out:   num(dig("usage", "output_tokens")),
		}
	case "gemini":
		return projection{
			Text: str(dig("candidates", 0, "content", "parts", 0, "text")),
			Stop: str(dig("candidates", 0, "finishReason")),
			In:   num(dig("usageMetadata", "promptTokenCount")),
			Out:  num(dig("usageMetadata", "candidatesTokenCount")),
		}
	default:
		return projection{
			Text:  str(dig("choices", 0, "message", "content")),
			Stop:  str(dig("choices", 0, "finish_reason")),
			Model: str(dig("model")),
			In:    num(dig("usage", "prompt_tokens")),
			Out:   num(dig("usage", "completion_tokens")),
		}
	}
}
