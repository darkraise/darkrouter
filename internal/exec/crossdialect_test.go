package exec

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/provider"
)

// fleetProvider is one upstream in a test fleet.
type fleetProvider struct {
	id      string
	kind    string
	baseURL string
	prio    int
}

func fleetExecutor(t *testing.T, ps []fleetProvider) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")

	var b strings.Builder
	b.WriteString("server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n")
	for _, p := range ps {
		b.WriteString("  - id: " + p.id + "\n    kind: " + p.kind +
			"\n    base_url: " + p.baseURL + "\n    api_key: ${K}\n    priority: " +
			strconv.Itoa(p.prio) + "\n    models: [m]\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	}, Deps{})
}

// serve builds an upstream returning one canned JSON body.
func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func send(t *testing.T, e *Executor, d edge.Dialect, target, body, pathValue string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	if pathValue != "" {
		r.SetPathValue("model", pathValue)
	}
	rec := httptest.NewRecorder()
	e.Handle(rec, r, d)
	return rec
}

func TestAnthropicInboundServedByOpenAICompat(t *testing.T) {
	up := serve(t, `{"id":"chatcmpl-1","model":"m","choices":[{"index":0,
		"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Oslo\"}"}}]},
		"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)

	e := fleetExecutor(t, []fleetProvider{{id: "compat", kind: "openaicompat", baseURL: up.URL}})
	rec := send(t, e, anthropicedge.New(), "/v1/messages",
		`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"weather?"}],
		  "tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, "")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"message"`) {
		t.Errorf("body is not an Anthropic message: %s", body)
	}
	if !strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, "call_a") {
		t.Errorf("the tool call did not survive translation: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Errorf("stop reason = %s", body)
	}
}

func TestGeminiInboundServedByAnthropic(t *testing.T) {
	up := serve(t, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
		"content":[{"type":"text","text":"Oslo is clear."}],
		"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":4}}`)

	e := fleetExecutor(t, []fleetProvider{{id: "anth", kind: "anthropic", baseURL: up.URL}})
	rec := send(t, e, geminiedge.New(), "/v1beta/models/m:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"weather in Oslo?"}]}]}`,
		"m:generateContent")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"candidates"`) || !strings.Contains(body, `"role":"model"`) {
		t.Errorf("body is not a Gemini response: %s", body)
	}
	if !strings.Contains(body, "Oslo is clear.") {
		t.Errorf("content did not survive: %s", body)
	}
}

func TestOpenAIInboundFailsOverFromAnthropicToGemini(t *testing.T) {
	var anthropicHits int
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicHits++
		w.WriteHeader(529)
	}))
	t.Cleanup(failing.Close)

	var geminiPath string
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseId":"r-1","modelVersion":"gemini-x","candidates":[
			{"index":0,"finishReason":"STOP","content":{"role":"model",
			 "parts":[{"text":"42"}]}}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`))
	}))
	t.Cleanup(working.Close)

	// Priority orders the chain: the Anthropic provider is tried first and
	// fails with an overloaded status, then the Gemini one serves.
	e := fleetExecutor(t, []fleetProvider{
		{id: "anth", kind: "anthropic", baseURL: failing.URL, prio: 10},
		{id: "gem", kind: "gemini", baseURL: working.URL, prio: 1},
	})
	rec := send(t, e, openaiedge.New(), "/v1/chat/completions",
		`{"model":"m","messages":[{"role":"user","content":"what is 6*7?"}]}`, "")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if anthropicHits == 0 {
		t.Error("the first provider was never attempted")
	}
	if !strings.Contains(geminiPath, ":generateContent") {
		t.Errorf("the second provider was called at %q", geminiPath)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"object":"chat.completion"`) {
		t.Errorf("body is not an OpenAI completion: %s", body)
	}
	if !strings.Contains(body, "42") {
		t.Errorf("content did not survive two translations: %s", body)
	}
	if rec.Header().Get("X-Darkrouter-Provider") != "gem" {
		t.Errorf("provider header = %q", rec.Header().Get("X-Darkrouter-Provider"))
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "2" {
		t.Errorf("attempts header = %q", rec.Header().Get("X-Darkrouter-Attempts"))
	}
}

func TestAnthropicInboundStreamsThroughOpenAICompat(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	t.Cleanup(up.Close)

	e := fleetExecutor(t, []fleetProvider{{id: "compat", kind: "openaicompat", baseURL: up.URL}})
	rec := send(t, e, anthropicedge.New(), "/v1/messages",
		`{"model":"m","max_tokens":32,"stream":true,
		  "messages":[{"role":"user","content":"hi"}]}`, "")

	body := rec.Body.String()
	for _, want := range []string{
		"event: message_start", "event: content_block_start",
		"event: content_block_delta", "event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("Anthropic sends no DONE sentinel:\n%s", body)
	}
}
