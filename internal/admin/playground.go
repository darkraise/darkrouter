package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
)

type playgroundMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type playgroundBody struct {
	Model       string              `json:"model"`
	Prompt      string              `json:"prompt,omitempty"`
	System      string              `json:"system,omitempty"`
	Messages    []playgroundMessage `json:"messages,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
	Tools       []map[string]any    `json:"tools,omitempty"`
	Stream      *bool               `json:"stream,omitempty"`
	Dialect     string              `json:"dialect,omitempty"`
}

// turns is the conversation the caller described, however they described it.
// A bare prompt is one user turn, which is what the old two-field body was.
func (b playgroundBody) turns() []playgroundMessage {
	if len(b.Messages) > 0 {
		return b.Messages
	}
	return []playgroundMessage{{Role: "user", Content: b.Prompt}}
}

// playgroundRequest synthesizes the proxy request the executor will serve.
//
// Separate from the handler so the shape it builds is assertable without a
// fake executor: the handler's job after this is one call, and every
// interesting decision — which wire, which path, where the system prompt goes
// — is made here.
func playgroundRequest(ctx context.Context, body playgroundBody) (*http.Request, edge.Dialect, error) {
	if body.Model == "" {
		return nil, nil, errors.New("model is required")
	}
	if len(body.Messages) == 0 && body.Prompt == "" {
		return nil, nil, errors.New("prompt is required when messages is empty")
	}
	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}

	switch body.Dialect {
	case "", "openai":
		return buildJSONRequest(ctx, "/v1/chat/completions",
			openaiPlaygroundBody(body, stream), openaiedge.New())
	case "anthropic":
		return buildJSONRequest(ctx, "/v1/messages",
			anthropicPlaygroundBody(body, stream), anthropicedge.New())
	case "gemini":
		if len(body.Tools) > 0 {
			return nil, nil, errors.New(
				"gemini declares tools as functionDeclarations; " +
					"send tools through the openai or anthropic dialect")
		}
		method := "generateContent"
		suffix := ""
		if stream {
			method, suffix = "streamGenerateContent", "?alt=sse"
		}
		segment := body.Model + ":" + method
		r, _, err := buildJSONRequest(ctx,
			"/v1beta/models/"+url.PathEscape(body.Model)+":"+method+suffix,
			geminiPlaygroundBody(body), nil)
		if err != nil {
			return nil, nil, err
		}
		// The Gemini edge reads the model out of the mux path value. A
		// synthesized request has none, and without this the edge parses an
		// empty model and the router is asked to route nothing.
		r.SetPathValue("model", segment)
		return r, geminiedge.NewFor(r), nil
	default:
		return nil, nil, errors.New("dialect must be openai, anthropic or gemini")
	}
}

func buildJSONRequest(ctx context.Context, path string, payload map[string]any,
	d edge.Dialect) (*http.Request, edge.Dialect, error) {

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, d, nil
}

func openaiPlaygroundBody(b playgroundBody, stream bool) map[string]any {
	msgs := []map[string]any{}
	if b.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": b.System})
	}
	for _, m := range b.turns() {
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
	}
	out := map[string]any{"model": b.Model, "messages": msgs, "stream": stream}
	if b.Temperature != nil {
		out["temperature"] = *b.Temperature
	}
	if b.MaxTokens != nil {
		out["max_tokens"] = *b.MaxTokens
	}
	if len(b.Tools) > 0 {
		out["tools"] = b.Tools
	}
	return out
}

func anthropicPlaygroundBody(b playgroundBody, stream bool) map[string]any {
	msgs := []map[string]any{}
	for _, m := range b.turns() {
		if m.Role == "system" {
			continue
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
	}
	out := map[string]any{"model": b.Model, "messages": msgs, "stream": stream}
	if b.System != "" {
		out["system"] = b.System
	}
	// Required on this wire. A request without one is refused upstream, which
	// reads to the operator as a broken provider rather than a missing field.
	out["max_tokens"] = 1024
	if b.MaxTokens != nil {
		out["max_tokens"] = *b.MaxTokens
	}
	if b.Temperature != nil {
		out["temperature"] = *b.Temperature
	}
	if len(b.Tools) > 0 {
		out["tools"] = b.Tools
	}
	return out
}

func geminiPlaygroundBody(b playgroundBody) map[string]any {
	contents := []map[string]any{}
	for _, m := range b.turns() {
		role := "user"
		if m.Role == "assistant" || m.Role == "model" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}
	out := map[string]any{"contents": contents}
	if b.System != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": b.System}},
		}
	}
	gen := map[string]any{}
	if b.Temperature != nil {
		gen["temperature"] = *b.Temperature
	}
	if b.MaxTokens != nil {
		gen["maxOutputTokens"] = *b.MaxTokens
	}
	if len(gen) > 0 {
		out["generationConfig"] = gen
	}
	return out
}

// handlePlayground runs a real request through the real executor.
//
// Anything else would verify the playground rather than the gateway: a mock
// would pass while the credential it exists to test is wrong. Going through
// exec.Handle also means the playground inherits failover, the budget gate and
// the request log for free, and the trace link works because the request really
// is in the log.
func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body playgroundBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, d, err := playgroundRequest(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// exec.Handle writes X-Darkrouter-Request before anything else, which is
	// what gives the SPA the id for the trace link before the stream starts.
	s.deps.Exec.Handle(w, pr, d)
}
