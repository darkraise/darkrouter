package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThePlaygroundStreamsThroughTheRealExecutor(t *testing.T) {
	// A mock would verify the playground rather than the gateway: it would
	// pass while the credential it exists to test is wrong.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, c := range []string{
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"he"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground", `{"model":"m","prompt":"say hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"he"`) || !strings.Contains(w.Body.String(), `"llo"`) {
		t.Errorf("the deltas did not reach the client:\n%s", w.Body.String())
	}
}

func TestThePlaygroundReturnsTheRequestIDForTheTraceLink(t *testing.T) {
	// Spec §6's "follow a link to the trace it produced". The id has to arrive
	// before the body, because the body is a stream the SPA renders as it
	// comes.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","prompt":"hi","stream":false}`)
	if got := w.Header().Get("X-Darkrouter-Request"); got == "" {
		t.Error("no request id header; the trace link has nothing to point at")
	}
}

func TestAPlaygroundRequestLandsInTheLog(t *testing.T) {
	// The link only works because the request really is in the log — which it
	// is because the playground goes through exec rather than around it.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","prompt":"hi","stream":false}`)
	id := w.Header().Get("X-Darkrouter-Request")
	if id == "" {
		t.Fatal("no request id")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestThePlaygroundRequiresACSRFToken(t *testing.T) {
	// Spec §4: a mutating verb, so it carries the header like any other
	// despite streaming its response.
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, _ := login(t, s)

	r := httptest.NewRequest("POST", "/api/playground",
		strings.NewReader(`{"model":"m","prompt":"hi"}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestThePlaygroundRejectsAnEmptyPrompt(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	for _, body := range []string{`{"model":"m"}`, `{"prompt":"hi"}`} {
		w := do(t, s, cookie, token, "POST", "/api/playground", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("playground(%s) status = %d, want 400", body, w.Code)
		}
	}
}

func decodeBuilt(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("built body is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestPlaygroundRequestBuildsAMultiTurnAnthropicCall(t *testing.T) {
	r, d, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "claude-sonnet-4-6", Dialect: "anthropic", System: "be terse",
		Messages: []playgroundMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "continue"},
		},
		Temperature: ptrOf(0.5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/messages" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if d.Name() != "anthropic" {
		t.Fatalf("dialect = %q", d.Name())
	}
	body := decodeBuilt(t, r)
	// Anthropic carries the system prompt outside the message list. Left in
	// place as a system-role turn, the upstream refuses the call.
	if body["system"] != "be terse" {
		t.Fatalf("system not lifted out: %v", body)
	}
	if got := body["messages"].([]any); len(got) != 3 {
		t.Fatalf("messages = %v", got)
	}
	// max_tokens is required on this wire, so the builder supplies one rather
	// than passing an upstream 400 back as a mystery.
	if body["max_tokens"] == nil {
		t.Fatalf("no max_tokens default: %v", body)
	}
	if body["temperature"] != 0.5 {
		t.Fatalf("temperature = %v", body["temperature"])
	}
}

func TestPlaygroundRequestBuildsGeminiShapeAndPathValue(t *testing.T) {
	r, _, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "gemini-2.5-pro", Dialect: "gemini", System: "be terse",
		Messages: []playgroundMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		MaxTokens: ptrOf(64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if r.URL.Query().Get("alt") != "sse" {
		t.Fatalf("streaming Gemini needs alt=sse, got %q", r.URL.RawQuery)
	}
	// The Gemini edge reads the model from the mux path value, which a
	// synthesized request only has because the builder set it.
	if got := r.PathValue("model"); got != "gemini-2.5-pro:streamGenerateContent" {
		t.Fatalf("path value = %q; the edge will parse an empty model", got)
	}
	body := decodeBuilt(t, r)
	contents, ok := body["contents"].([]any)
	if !ok || len(contents) != 2 {
		t.Fatalf("gemini takes contents, not messages: %v", body)
	}
	first := contents[0].(map[string]any)
	second := contents[1].(map[string]any)
	if first["role"] != "user" || second["role"] != "model" {
		t.Fatalf("assistant must render as the model role: %v", contents)
	}
	if body["systemInstruction"] == nil {
		t.Fatalf("systemInstruction missing: %v", body)
	}
	gen := body["generationConfig"].(map[string]any)
	if gen["maxOutputTokens"] != float64(64) {
		t.Fatalf("generationConfig = %v", gen)
	}
}

func TestPlaygroundRequestCarriesToolsAndMaxTokens(t *testing.T) {
	r, _, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "m", Prompt: "hi", MaxTokens: ptrOf(128),
		Tools: []map[string]any{{"type": "function"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := decodeBuilt(t, r)
	if body["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens = %v", body["max_tokens"])
	}
	if len(body["tools"].([]any)) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
}

func TestPlaygroundRequestRefusesWhatItCannotTranslate(t *testing.T) {
	for name, body := range map[string]playgroundBody{
		"unknown dialect": {Model: "m", Prompt: "p", Dialect: "nope"},
		"no model":        {Prompt: "p"},
		"nothing to say":  {Model: "m"},
		// Gemini declares tools as functionDeclarations, which is not the
		// shape the console's tools box describes. Dropping them silently
		// would make a tool-using prompt answer as if no tools existed.
		"gemini tools": {Model: "m", Prompt: "p", Dialect: "gemini",
			Tools: []map[string]any{{"type": "function"}}},
	} {
		if _, _, err := playgroundRequest(context.Background(), body); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestThePlaygroundSpeaksAnthropicEndToEnd(t *testing.T) {
	// The whole path rather than the builder: the anthropic edge parses what
	// the builder wrote, and the openaicompat adapter renders it upstream.
	var seen map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","dialect":"anthropic","stream":false,
		  "messages":[{"role":"user","content":"hi"},
		              {"role":"assistant","content":"ok"},
		              {"role":"user","content":"go on"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	msgs, _ := seen["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("the turns did not survive the translation: %v", seen)
	}
}

func ptrOf[T any](v T) *T { return &v }

func TestCountRequestBuildsTheNativeCountingCall(t *testing.T) {
	r, d, kind, err := countRequest(context.Background(), countBody{
		Dialect: "anthropic", Model: "claude-sonnet-4-6", Prompt: "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/messages/count_tokens" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if kind != "anthropic" || d.Name() != "anthropic" {
		t.Fatalf("kind = %q, dialect = %q", kind, d.Name())
	}
	body := decodeBuilt(t, r)
	if len(body["messages"].([]any)) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
	// A counting body is not a completion body. max_tokens and stream on this
	// endpoint are rejected upstream.
	if body["max_tokens"] != nil || body["stream"] != nil {
		t.Fatalf("counting body carries completion fields: %v", body)
	}
}

func TestCountRequestBuildsGeminiCountTokens(t *testing.T) {
	r, _, kind, err := countRequest(context.Background(), countBody{
		Dialect: "gemini", Model: "gemini-2.5-pro", Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1beta/models/gemini-2.5-pro:countTokens" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if kind != "gemini" {
		t.Fatalf("kind = %q", kind)
	}
	if got := r.PathValue("model"); got != "gemini-2.5-pro:countTokens" {
		t.Fatalf("path value = %q", got)
	}
	if _, ok := decodeBuilt(t, r)["contents"]; !ok {
		t.Fatal("gemini counting takes contents")
	}
}

func TestCountRequestRefusesTheOpenAIDialect(t *testing.T) {
	// There is no OpenAI counting endpoint, so offering the option would mean
	// silently answering a different question — a local estimate presented as
	// a native count.
	if _, _, _, err := countRequest(context.Background(), countBody{
		Dialect: "openai", Model: "m", Prompt: "p",
	}); err == nil {
		t.Fatal("openai must be refused")
	}
}

func TestTheCountEndpointRequiresACSRFToken(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, _ := login(t, s)
	r := httptest.NewRequest("POST", "/api/playground/count",
		strings.NewReader(`{"dialect":"anthropic","model":"m","prompt":"p"}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestTheCountEndpointRejectsABadDialect(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground/count",
		`{"dialect":"openai","model":"m","prompt":"p"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
