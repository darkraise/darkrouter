package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parse(t *testing.T, body string) (*ir.Request, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req, _, err := ParseRequest(r, 1<<20)
	return req, err
}

func TestParseSimpleTextRequest(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "m" || len(req.Messages) != 1 {
		t.Fatalf("got %+v", req)
	}
	if req.Messages[0].Content[0].Text != "hi" {
		t.Fatalf("content = %+v", req.Messages[0].Content)
	}
}

func TestParseTreatsDeveloperRoleAsSystem(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"developer","content":"be brief"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Role != ir.RoleSystem {
		t.Fatalf("role = %q", req.Messages[0].Role)
	}
}

func TestParseMultiPartContentWithImage(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"what is this"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 || blocks[1].Type != ir.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !req.Needs().Vision {
		t.Fatal("expected Needs().Vision")
	}
}

func TestParseTools(t *testing.T) {
	body := `{"model":"m","messages":[],"tools":[{"type":"function","function":
		{"name":"get_weather","description":"d","parameters":{"type":"object"}}}],
		"tool_choice":"required"}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Fatalf("tool_choice = %+v", req.ToolChoice)
	}
	if !req.Needs().Tools {
		t.Fatal("expected Needs().Tools")
	}
}

func TestParseCapturesPassthroughBody(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt.Body) != body || pt.ModelField != "model" {
		t.Fatalf("passthrough = %+v", pt)
	}
}

func TestParseRejectsOversizedBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 100)))
	if _, _, err := ParseRequest(r, 10); err == nil {
		t.Fatal("expected an oversized-body error")
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := parse(t, `{"model":`); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestParseRequestReportsTheLLMSurfaceTyped(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Surface != ir.SurfaceLLM {
		t.Errorf("Surface = %q, want %q", pt.Surface, ir.SurfaceLLM)
	}
}

func parsed(t *testing.T, body string) *ir.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req, _, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestParseRequestReadsAssistantToolCalls(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}]}
	]}`)
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	blocks := req.Messages[1].Content
	if len(blocks) != 1 || blocks[0].Type != ir.BlockToolUse {
		t.Fatalf("assistant blocks = %+v", blocks)
	}
	tu := blocks[0].ToolUse
	if tu.ID != "call_a" || tu.Name != "get_weather" || string(tu.Input) != `{"city":"Oslo"}` {
		t.Errorf("tool_use = %+v", tu)
	}
}

func TestParseRequestReadsAToolMessage(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"tool","tool_call_id":"call_a","content":"17C"}]}`)
	blocks := req.Messages[0].Content
	if req.Messages[0].Role != ir.RoleTool {
		t.Errorf("role = %q", req.Messages[0].Role)
	}
	if len(blocks) != 1 || blocks[0].Type != ir.BlockToolResult {
		t.Fatalf("blocks = %+v", blocks)
	}
	tr := blocks[0].ToolResult
	if tr.ToolUseID != "call_a" || tr.Text() != "17C" {
		t.Errorf("tool_result = %+v", tr)
	}
}

func TestParseRequestAcceptsALegacyFunctionMessage(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"function","name":"get_weather","content":"17C"}]}`)
	tr := req.Messages[0].Content[0].ToolResult
	if tr == nil || tr.Text() != "17C" {
		t.Fatalf("tool_result = %+v", tr)
	}
	if tr.ToolUseID == "" {
		t.Error("a legacy function message carries no id; one must be synthesized")
	}
}

func TestParseRequestAcceptsLegacyFunctionDeclarations(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],
		"functions":[{"name":"f","description":"d","parameters":{"type":"object"}}],
		"function_call":{"name":"f"}}`)
	if len(req.Tools) != 1 || req.Tools[0].Name != "f" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

func TestParseRequestAcceptsStopAsAStringOrArray(t *testing.T) {
	if got := parsed(t, `{"model":"m","messages":[],"stop":"END"}`).StopSequences; len(got) != 1 || got[0] != "END" {
		t.Errorf("stop as string = %v", got)
	}
	if got := parsed(t, `{"model":"m","messages":[],"stop":["A","B"]}`).StopSequences; len(got) != 2 {
		t.Errorf("stop as array = %v", got)
	}
	if got := parsed(t, `{"model":"m","messages":[]}`).StopSequences; got != nil {
		t.Errorf("stop absent = %v, want nil", got)
	}
}

func TestParseRequestPrefersMaxCompletionTokens(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[],"max_tokens":100,"max_completion_tokens":200}`)
	if req.MaxTokens == nil || *req.MaxTokens != 200 {
		t.Errorf("max tokens = %v; max_completion_tokens is the current spelling", req.MaxTokens)
	}
}

func TestParseRequestReadsMultiPartMediaParts(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"look"},
		{"type":"image_url","image_url":{"url":"https://x.example/a.png"}},
		{"type":"input_audio","input_audio":{"data":"UklGR","format":"wav"}},
		{"type":"file","file":{"file_id":"file-1"}}]}]}`)
	blocks := req.Messages[0].Content
	if len(blocks) != 4 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[1].Type != ir.BlockImage || blocks[1].Media.URL != "https://x.example/a.png" {
		t.Errorf("image = %+v", blocks[1])
	}
	if blocks[2].Type != ir.BlockAudio || blocks[2].Media.MIME != "audio/wav" || blocks[2].Media.Data != "UklGR" {
		t.Errorf("audio = %+v", blocks[2])
	}
	if blocks[3].Type != ir.BlockDocument || blocks[3].Media.FileID != "file-1" {
		t.Errorf("file = %+v", blocks[3])
	}
}

func TestParseRequestReadsResponseFormatAndFlags(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[],"parallel_tool_calls":false,
		"metadata":{"user_id":"u1"},
		"response_format":{"type":"json_schema","json_schema":{"name":"r","schema":{"type":"object"}}}}`)
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %+v", req.ResponseFormat)
	}
	if string(req.ResponseFormat.Schema) != `{"type":"object"}` {
		t.Errorf("schema = %s; the inner schema is what the IR carries", req.ResponseFormat.Schema)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel_tool_calls = %v", req.ParallelToolCalls)
	}
	if req.Metadata["user_id"] != "u1" {
		t.Errorf("metadata = %v", req.Metadata)
	}
}

func TestParseRequestSplitsADataURIImage(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw=="}}]}]}`)
	m := req.Messages[0].Content[0].Media
	if m.Data != "iVBORw==" || m.MIME != "image/png" {
		t.Errorf("media = %+v; a data URI is inline content, not an address", m)
	}
	if m.URL != "" {
		t.Errorf("media = %+v; leaving it in URL makes Gemini try to fetch \"data:\"", m)
	}
}

func TestParseRequestKeepsAPublicImageURL(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":[
		{"type":"image_url","image_url":{"url":"https://x.example/a.png"}}]}]}`)
	m := req.Messages[0].Content[0].Media
	if m.URL != "https://x.example/a.png" || m.Data != "" {
		t.Errorf("media = %+v", m)
	}
}

func TestParseCarriesTheStreamFlagOnPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"streaming", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`, true},
		{"unary", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
			_, pt, err := ParseRequest(r, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if pt.Stream != tc.want {
				t.Errorf("Stream = %v, want %v", pt.Stream, tc.want)
			}
			// The model is body-carried here, so there is no URL operation.
			if pt.Method != "" {
				t.Errorf("Method = %q, want empty", pt.Method)
			}
		})
	}
}
