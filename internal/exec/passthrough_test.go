package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRewriteSwapsTheModelAndNothingElse(t *testing.T) {
	pt := &edge.Passthrough{
		Body: []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],` +
			`"some_parameter_shipped_last_week":{"nested":true}}`),
		ModelField: "model", Surface: ir.SurfaceLLM,
	}
	body, injected, err := rewriteForward(pt, "claude-sonnet-4-5", "claude-opus-4-5", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("stream_options injected on a non-openaicompat kind")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "claude-opus-4-5" {
		t.Errorf("model = %v", got["model"])
	}
	// The unmodelled parameter is the whole point of the phase.
	if _, ok := got["some_parameter_shipped_last_week"]; !ok {
		t.Error("an unmodelled top-level field was dropped")
	}
	if _, ok := got["stream_options"]; ok {
		t.Error("a fourth mutation appeared")
	}
}

func TestRewriteDoesNotEscapeHTMLSignificantCharacters(t *testing.T) {
	// json.Marshal escapes <, > and & inside a RawMessage by default, which
	// would silently rewrite prompt text on every forwarded request.
	pt := &edge.Passthrough{
		Body:       []byte(`{"model":"a","messages":[{"role":"user","content":"if x < y && y > z"}]}`),
		ModelField: "model", Surface: ir.SurfaceLLM,
	}
	body, _, err := rewriteForward(pt, "a", "b", "openaicompat")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`if x < y && y > z`)) {
		t.Errorf("prompt text was escaped: %s", body)
	}
}

func TestRewriteForwardsTheOriginalBytesWhenNothingChanges(t *testing.T) {
	// The Claude Code case: the client already asked for the name the target
	// serves, and a unary request needs no injection.
	raw := []byte(`{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`)
	pt := &edge.Passthrough{Body: raw, ModelField: "model", Surface: ir.SurfaceLLM}

	body, injected, err := rewriteForward(pt, "claude-opus-4-5", "claude-opus-4-5", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("injected on a unary request")
	}
	if &body[0] != &raw[0] {
		t.Errorf("the body was re-encoded rather than forwarded\n got: %s\nwant: %s", body, raw)
	}
}

func TestRewriteInjectsStreamOptionsOnlyWhenAbsent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		kind         string
		stream       bool
		wantInjected bool
	}{
		{"absent on a streaming openaicompat request",
			`{"model":"a","stream":true}`, "openaicompat", true, true},
		{"already present, so the chunk is the client's",
			`{"model":"a","stream":true,"stream_options":{"include_usage":true}}`,
			"openaicompat", true, false},
		{"present but disabled is still the client's choice",
			`{"model":"a","stream":true,"stream_options":{"include_usage":false}}`,
			"openaicompat", true, false},
		{"unary needs no usage chunk", `{"model":"a"}`, "openaicompat", false, false},
		{"anthropic has no such parameter", `{"model":"a","stream":true}`, "anthropic", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pt := &edge.Passthrough{
				Body: []byte(tc.body), ModelField: "model",
				Surface: ir.SurfaceLLM, Stream: tc.stream,
			}
			body, injected, err := rewriteForward(pt, "a", "a", tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if injected != tc.wantInjected {
				t.Errorf("injected = %v, want %v", injected, tc.wantInjected)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			_, present := got["stream_options"]
			if tc.kind == "openaicompat" && tc.stream && !present {
				t.Error("a streaming openaicompat request must carry stream_options")
			}
		})
	}
}

func TestRewriteLeavesAURLCarriedBodyUntouched(t *testing.T) {
	raw := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	pt := &edge.Passthrough{Body: raw, Method: "generateContent", Surface: ir.SurfaceLLM, Stream: true}

	body, injected, err := rewriteForward(pt, "gemini-2.0-flash", "gemini-2.5-pro", "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("Gemini has no stream_options analogue")
	}
	if &body[0] != &raw[0] {
		t.Errorf("a URL-carried body was rewritten: %s", body)
	}
}

func TestRewriteReportsAMissingModelField(t *testing.T) {
	pt := &edge.Passthrough{
		Body: []byte(`{"messages":[]}`), ModelField: "model", Surface: ir.SurfaceLLM,
	}
	if _, _, err := rewriteForward(pt, "a", "b", "openaicompat"); !errors.Is(err, ErrNoModelField) {
		t.Errorf("err = %v, want ErrNoModelField", err)
	}
}

func TestRewriteReportsMalformedJSON(t *testing.T) {
	pt := &edge.Passthrough{Body: []byte(`{"model":`), ModelField: "model", Surface: ir.SurfaceLLM}
	if _, _, err := rewriteForward(pt, "a", "b", "openaicompat"); err == nil {
		t.Fatal("want an error for a malformed body")
	}
}

func TestForwardHeadersKeepsOnlyTheAllowlist(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
	for k, v := range map[string]string{
		"Content-Type":      "application/json",
		"Accept":            "text/event-stream",
		"User-Agent":        "claude-cli/2.0.0",
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "context-1m-2025-08-07",
		"openai-beta":       "assistants=v2",

		// Dropped: the inbound credential in all three dialect spellings,
		// hop-by-hop headers, and anything a client invented.
		"Authorization":         "Bearer proxy-token",
		"x-api-key":             "proxy-token",
		"x-goog-api-key":        "proxy-token",
		"Connection":            "keep-alive",
		"Keep-Alive":            "timeout=5",
		"Transfer-Encoding":     "chunked",
		"Te":                    "trailers",
		"Upgrade":               "h2c",
		"Proxy-Authorization":   "Basic abc",
		"Host":                  "evil.example",
		"X-Forwarded-For":       "10.0.0.1",
		"Accept-Encoding":       "gzip",
		"Cookie":                "session=abc",
		"X-Darkrouter-Provider": "spoofed",
	} {
		r.Header.Set(k, v)
	}

	h := forwardHeaders(r)
	for _, want := range []string{
		"Content-Type", "Accept", "User-Agent", "anthropic-version",
		"anthropic-beta", "openai-beta",
	} {
		if h.Get(want) == "" {
			t.Errorf("%s was dropped", want)
		}
	}
	if len(h) != 6 {
		t.Errorf("forwarded %d headers, want 6: %v", len(h), h)
	}
}

func TestCompressedInboundBodiesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		enc  string
		want bool
	}{
		{"", false},
		{"identity", false},
		{"gzip", true},
		{"br", true},
		{"gzip, identity", true},
	} {
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
		if tc.enc != "" {
			r.Header.Set("Content-Encoding", tc.enc)
		}
		if got := compressedBody(r); got != tc.want {
			t.Errorf("Content-Encoding %q: compressedBody = %v, want %v", tc.enc, got, tc.want)
		}
	}
}

func TestNoAuxiliarySurfaceIsEverForwarded(t *testing.T) {
	// Master design §4.1: multipart and binary surfaces take the IR path, and
	// what enforces it is the op having no passthrough body to offer. A
	// transcription request's model lives inside a multipart form; an
	// embedding request has no messages at all.
	//
	// Zero values are enough: this asserts on the method set, not on behavior.
	for name, op := range map[string]SurfaceOp{
		"transcription": &transcriptionOp{},
		"embedding":     &embedOp{},
		"image":         &imageOp{},
		"speech":        &speechOp{},
		"rerank":        &rerankOp{},
		"moderation":    &moderationOp{},
	} {
		if _, ok := op.(passthroughOp); ok {
			t.Errorf("%s offered a passthrough body", name)
		}
	}
}

func TestAPromptWithHTMLCharactersSurvivesTheRewrite(t *testing.T) {
	// The end-to-end version of the rewrite's escaping test: the client sends <,
	// > and &, the model name changes so the body must be re-encoded, and the
	// provider must still see the original characters.
	var seen []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"ok"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer up.Close()

	runChatModel(t, up.URL, "anthropic", "other-model",
		`{"model":"client-model","max_tokens":16,
		  "messages":[{"role":"user","content":"if x < y && y > z"}]}`)

	if !bytes.Contains(seen, []byte("if x < y && y > z")) {
		t.Errorf("the prompt was escaped: %s", seen)
	}
}
