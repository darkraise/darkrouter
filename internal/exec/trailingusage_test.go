package exec

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/adapter"
	bedrockadapter "github.com/darkraise/darkrouter/internal/adapter/bedrock"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// openAIStreamWithTrailingUsage is an OpenAI stream as stream_options
// include_usage delivers it: the usage chunk, with an empty choices array,
// follows the chunk carrying finish_reason.
func openAIStreamWithTrailingUsage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":1000,"total_tokens":2000,"completion_tokens_details":{"reasoning_tokens":800}}}`,
	} {
		_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
	}
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

// bedrockStreamWithTrailingUsage is a ConverseStream response, whose usage
// arrives in a metadata event after messageStop.
func bedrockStreamWithTrailingUsage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	var buf bytes.Buffer
	for _, ev := range []struct {
		name    string
		payload string
	}{
		{"messageStart", `{"role":"assistant"}`},
		{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"pong"}}`},
		{"contentBlockStop", `{"contentBlockIndex":0}`},
		{"messageStop", `{"stopReason":"end_turn"}`},
		{"metadata", `{"usage":{"inputTokens":1000,"outputTokens":1000,"totalTokens":2000},"metrics":{"latencyMs":120}}`},
	} {
		_ = eventstream.NewEncoder().Encode(&buf, eventstream.Message{
			Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(ev.name)},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			},
			Payload: []byte(ev.payload),
		})
	}
	_, _ = w.Write(buf.Bytes())
}

func trailingUsageExecutor(t *testing.T, kind string, h http.HandlerFunc, logger *captureLogger) *Executor {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	fleet := []provider.Provider{{
		ID: "up", Kind: kind, BaseURL: up.URL, Region: "us-east-1", Models: []string{"m"},
		Credentials: []provider.Credential{{ID: "k1", Secret: "sk", Enabled: true}},
	}}
	return New(config.NewStoreOf(testConfig(t, nil)), &fleetSource{ps: fleet},
		map[string]adapter.Adapter{
			"openaicompat": openaicompat.New(),
			"bedrock":      bedrockadapter.New(),
		}, Deps{Log: logger, Catalog: catalogOf(catalog.Model{
			ProviderID: "up", ModelID: "m", Surfaces: []ir.Surface{ir.SurfaceLLM},
			Source: catalog.SourceInferred, State: catalog.StateLive,
			Pricing: catalog.Pricing{
				InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 2_000_000, Known: true,
			},
		})})
}

type sseFrame struct {
	event string
	data  map[string]any
}

func sseFrames(t *testing.T, body string) []sseFrame {
	t.Helper()
	var out []sseFrame
	for _, block := range strings.Split(body, "\n\n") {
		var f sseFrame
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				f.event = name
			}
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				if err := json.Unmarshal([]byte(payload), &f.data); err != nil {
					t.Fatalf("frame is not JSON: %q", payload)
				}
			}
		}
		if f.data != nil {
			out = append(out, f)
		}
	}
	return out
}

func TestTrailingStreamUsageReachesTheClientAndTheRecord(t *testing.T) {
	// Both upstreams end the message before they report usage. A client whose
	// dialect wants usage inside its terminal frame, and the request record
	// priced from it, must still see the counts that followed.
	upstreams := []struct {
		name           string
		kind           string
		handler        http.HandlerFunc
		wantCandidates float64
		wantThoughts   float64
	}{
		{"openaicompat", "openaicompat", openAIStreamWithTrailingUsage, 200, 800},
		{"bedrock", "bedrock", bedrockStreamWithTrailingUsage, 1000, 0},
	}
	for _, up := range upstreams {
		t.Run(up.name+" to anthropic", func(t *testing.T) {
			logger := &captureLogger{}
			e := trailingUsageExecutor(t, up.kind, up.handler, logger)
			r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(
				`{"model":"m","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"ping"}]}`))
			w := httptest.NewRecorder()
			e.Handle(w, r, anthropicedge.New())
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}

			frames := sseFrames(t, w.Body.String())
			if len(frames) < 2 || frames[len(frames)-1].event != "message_stop" ||
				frames[len(frames)-2].event != "message_delta" {
				t.Fatalf("stream does not end message_delta, message_stop:\n%s", w.Body)
			}
			usage, _ := frames[len(frames)-2].data["usage"].(map[string]any)
			if usage["input_tokens"] != 1000.0 || usage["output_tokens"] != 1000.0 {
				t.Errorf("message_delta usage = %v, want 1000 in and 1000 out", usage)
			}
			assertPricedRecord(t, logger)
		})

		t.Run(up.name+" to gemini", func(t *testing.T) {
			logger := &captureLogger{}
			e := trailingUsageExecutor(t, up.kind, up.handler, logger)
			r := httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent?alt=sse",
				strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`))
			r.SetPathValue("model", "m:streamGenerateContent")
			w := httptest.NewRecorder()
			e.Handle(w, r, geminiedge.NewFor(r))
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}

			frames := sseFrames(t, w.Body.String())
			if len(frames) == 0 {
				t.Fatalf("no chunks:\n%s", w.Body)
			}
			last := frames[len(frames)-1].data
			cands, _ := last["candidates"].([]any)
			if len(cands) != 1 || cands[0].(map[string]any)["finishReason"] != "STOP" {
				t.Fatalf("last chunk is not the terminal one: %v", last)
			}
			usage, _ := last["usageMetadata"].(map[string]any)
			if usage["promptTokenCount"] != 1000.0 || usage["candidatesTokenCount"] != up.wantCandidates ||
				usage["thoughtsTokenCount"] != up.wantThoughts || usage["totalTokenCount"] != 2000.0 {
				t.Errorf("usageMetadata = %v, want 1000 prompt, %v candidates, %v thoughts, 2000 total",
					usage, up.wantCandidates, up.wantThoughts)
			}
			assertPricedRecord(t, logger)
		})
	}
}

func assertPricedRecord(t *testing.T, logger *captureLogger) {
	t.Helper()
	rec := logger.only(t)
	if rec.TokensIn != 1000 || rec.TokensOut != 1000 {
		t.Errorf("recorded tokens in/out = %d/%d, want 1000/1000", rec.TokensIn, rec.TokensOut)
	}
	if rec.CostMicros == nil {
		t.Fatal("recorded cost is nil for a priced model")
	}
	if *rec.CostMicros != 3000 {
		t.Errorf("recorded cost = %d micros, want 3000", *rec.CostMicros)
	}
}
