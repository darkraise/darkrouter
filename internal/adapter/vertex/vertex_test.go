package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func req() *ir.Request {
	max := 128
	return &ir.Request{
		Model:     "claude-sonnet-4-5",
		MaxTokens: &max,
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
}

func build(t *testing.T, tgt *adapter.Target, r *ir.Request) (*http.Request, map[string]any) {
	t.Helper()
	hr, _, err := New().BuildRequest(context.Background(), tgt, r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, raw)
	}
	// Put it back: the executor reads this body after the builder returns.
	hr.Body = io.NopCloser(bytes.NewReader(raw))
	return hr, body
}

func anthropicTarget() *adapter.Target {
	return &adapter.Target{
		Project: "proj", Location: "us-central1",
		Publisher: PublisherAnthropic, Model: "claude-sonnet-4-5",
	}
}

func googleTarget() *adapter.Target {
	return &adapter.Target{
		Project: "proj", Location: "us-central1",
		Publisher: PublisherGoogle, Model: "gemini-2.5-pro",
	}
}

// The single most important pair of assertions in this package. Spec §4.1: an
// implementer following the earlier draft would 400 on every Claude call.
func TestAnthropicPublisherUsesRawPredict(t *testing.T) {
	hr, body := build(t, anthropicTarget(), req())

	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/" +
		"us-central1/publishers/anthropic/models/claude-sonnet-4-5:rawPredict"
	if hr.URL.String() != want {
		t.Errorf("url = %s\nwant %s", hr.URL, want)
	}
	if body["anthropic_version"] != AnthropicVersion {
		t.Errorf("anthropic_version = %v, want %q", body["anthropic_version"], AnthropicVersion)
	}
	// The model moves into the URL. Vertex rejects a body that also names it.
	if _, present := body["model"]; present {
		t.Errorf("the model is still in the body: %v", body["model"])
	}
	// And it is the Anthropic shape, not the Gemini one.
	if _, ok := body["messages"]; !ok {
		t.Errorf("body is not Anthropic Messages: %v", keys(body))
	}
	if _, ok := body["contents"]; ok {
		t.Errorf("body is the Gemini shape: %v", keys(body))
	}
}

func TestGooglePublisherUsesGenerateContent(t *testing.T) {
	hr, body := build(t, googleTarget(), req())

	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/" +
		"us-central1/publishers/google/models/gemini-2.5-pro:generateContent"
	if hr.URL.String() != want {
		t.Errorf("url = %s\nwant %s", hr.URL, want)
	}
	if _, ok := body["contents"]; !ok {
		t.Errorf("body is not the Gemini shape: %v", keys(body))
	}
	if _, ok := body["anthropic_version"]; ok {
		t.Error("anthropic_version leaked into a Gemini body")
	}
}

func TestStreamingRoutesDifferPerPublisher(t *testing.T) {
	r := req()
	r.Stream = true

	hr, _ := build(t, anthropicTarget(), r)
	if !strings.HasSuffix(hr.URL.Path, ":streamRawPredict") {
		t.Errorf("anthropic streaming path = %s", hr.URL.Path)
	}
	hr, _ = build(t, googleTarget(), r)
	if !strings.HasSuffix(hr.URL.Path, ":streamGenerateContent") {
		t.Errorf("google streaming path = %s", hr.URL.Path)
	}
	// Vertex speaks SSE for the Gemini route only when asked.
	if hr.URL.RawQuery != "alt=sse" {
		t.Errorf("google streaming query = %q, want alt=sse", hr.URL.RawQuery)
	}
}

func TestAnUnknownPublisherIsAnError(t *testing.T) {
	// Llama and Mistral MaaS use a third, OpenAI-compatible route and are out
	// of scope for v1, spec §4.1. Guessing one of the two implemented shapes
	// would 400 with a message about the wrong payload.
	tgt := googleTarget()
	tgt.Publisher = "publishers/meta"
	if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err == nil {
		t.Fatal("an unsupported publisher must be refused")
	}
}

func TestAnEmptyPublisherDefaultsToGoogle(t *testing.T) {
	// A catalog row seeded before the publisher column was populated, or a
	// provider created by hand. Google is the safe default: the vertex preset
	// declares it.
	tgt := googleTarget()
	tgt.Publisher = ""
	hr, body := build(t, tgt, req())
	if _, ok := body["contents"]; !ok {
		t.Errorf("body = %v", keys(body))
	}
	if !strings.HasSuffix(hr.URL.Path, ":generateContent") {
		t.Errorf("path = %s", hr.URL.Path)
	}
}

func TestNoCredentialHeaderIsWritten(t *testing.T) {
	// internal/auth attaches the bearer token. A key written here would be a
	// second credential on the same request.
	for _, tgt := range []*adapter.Target{googleTarget(), anthropicTarget()} {
		tgt.APIKey = "should-be-ignored"
		hr, _ := build(t, tgt, req())
		if hr.Header.Get("Authorization") != "" || hr.Header.Get("x-goog-api-key") != "" ||
			hr.Header.Get("x-api-key") != "" {
			t.Errorf("%s: the builder wrote a credential header: %v", tgt.Publisher, hr.Header)
		}
	}
}

func TestProjectAndLocationAreRequired(t *testing.T) {
	tgt := googleTarget()
	tgt.Project = ""
	if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err == nil {
		t.Fatal("a vertex target with no project must be refused")
	}
	tgt = googleTarget()
	tgt.Location = ""
	if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err == nil {
		t.Fatal("a vertex target with no location must be refused")
	}
}

func TestParseResponseDispatchesOnShape(t *testing.T) {
	// Neither parser is handed a Target, so the publisher is unavailable. The
	// two payloads are unambiguous, which is what makes this safe.
	anthropicBody := `{"id":"msg_1","type":"message","role":"assistant",
	  "content":[{"type":"text","text":"from claude"}],"stop_reason":"end_turn",
	  "usage":{"input_tokens":3,"output_tokens":2}}`
	got, err := parseResponse(&http.Response{
		StatusCode: 200, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(anthropicBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) == 0 || got.Content[0].Text != "from claude" {
		t.Errorf("anthropic response = %+v", got.Content)
	}

	geminiBody := `{"candidates":[{"content":{"parts":[{"text":"from gemini"}],"role":"model"},
	  "finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`
	got, err = parseResponse(&http.Response{
		StatusCode: 200, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(geminiBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) == 0 || got.Content[0].Text != "from gemini" {
		t.Errorf("gemini response = %+v", got.Content)
	}
}

func TestParseStreamDispatchesOnThePrefix(t *testing.T) {
	anthropicSSE := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"m","model":"claude","usage":{"input_tokens":1}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"A"}}` + "\n\n"
	var text string
	for ev, err := range parseStream(strings.NewReader(anthropicSSE), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Delta != nil {
			text += ev.Delta.Text
		}
	}
	if text != "A" {
		t.Errorf("anthropic stream text = %q", text)
	}

	geminiSSE := `data: {"candidates":[{"content":{"parts":[{"text":"B"}],"role":"model"}}]}` + "\n\n"
	text = ""
	for ev, err := range parseStream(strings.NewReader(geminiSSE), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Delta != nil {
			text += ev.Delta.Text
		}
	}
	if text != "B" {
		t.Errorf("gemini stream text = %q", text)
	}
}

func TestSurfacesIsLLMOnly(t *testing.T) {
	// The vertex preset declares embedding, but that route is a third URL
	// shape; claiming the surface would route embeddings to a 404.
	s := New().Surfaces()
	if !s.Has(ir.SurfaceLLM) || s.Has(ir.SurfaceEmbedding) {
		t.Errorf("surfaces = %v", s)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAnExplicitBaseURLWins(t *testing.T) {
	// A private service endpoint, or a test server. Without this the adapter
	// could only ever talk to googleapis.com — which also makes it untestable
	// above the unit level.
	tgt := googleTarget()
	tgt.BaseURL = "https://vertex.internal/v1/projects/p/locations/l"
	hr, _ := build(t, tgt, req())
	want := "https://vertex.internal/v1/projects/p/locations/l/publishers/google/" +
		"models/gemini-2.5-pro:generateContent"
	if hr.URL.String() != want {
		t.Errorf("url = %s\nwant %s", hr.URL, want)
	}

	tgt = anthropicTarget()
	tgt.BaseURL = "https://vertex.internal/v1/projects/p/locations/l"
	hr, _ = build(t, tgt, req())
	if !strings.HasPrefix(hr.URL.String(), "https://vertex.internal/") {
		t.Errorf("anthropic url = %s", hr.URL)
	}
}
