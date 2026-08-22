package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestExtractModelSplitsOnTheLastColon(t *testing.T) {
	cases := []struct {
		in     string
		model  string
		method string
	}{
		{"gemini-2.0-flash:generateContent", "gemini-2.0-flash", "generateContent"},
		{"models/gemini-2.0-flash:streamGenerateContent", "gemini-2.0-flash", "streamGenerateContent"},
		{"openrouter/anthropic/claude-sonnet-4.5:generateContent",
			"openrouter/anthropic/claude-sonnet-4.5", "generateContent"},
		{"fast:coding:countTokens", "fast:coding", "countTokens"},
		{"gemini-2.0-flash", "gemini-2.0-flash", ""},
		{"models/fast", "fast", ""},
	}
	for _, tc := range cases {
		model, method := ExtractModel(tc.in)
		if model != tc.model || method != tc.method {
			t.Errorf("ExtractModel(%q) = %q, %q; want %q, %q", tc.in, model, method, tc.model, tc.method)
		}
	}
}

// request builds a request whose PathValue is set the way the ServeMux pattern
// "POST /v1beta/models/{model}" sets it.
func request(t *testing.T, segment, query, body string) *http.Request {
	t.Helper()
	target := "/v1beta/models/" + segment
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.SetPathValue("model", segment)
	return r
}

func parsed(t *testing.T, segment, query, body string) *ir.Request {
	t.Helper()
	req, pt, err := ParseRequest(request(t, segment, query, body), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM {
		t.Fatalf("passthrough = %+v", pt)
	}
	if pt.ModelField != "" {
		t.Errorf("ModelField = %q; the Gemini model lives in the URL", pt.ModelField)
	}
	return req
}

func TestParseRequestTakesTheModelFromThePath(t *testing.T) {
	req := parsed(t, "models/gemini-2.0-flash:generateContent", "",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	if req.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q", req.Model)
	}
	if req.Stream {
		t.Error("generateContent is not a streaming method")
	}
}

func TestParseRequestMarksStreamGenerateContent(t *testing.T) {
	req := parsed(t, "gemini-2.0-flash:streamGenerateContent", "alt=sse", `{"contents":[]}`)
	if !req.Stream {
		t.Error("streamGenerateContent must set Stream")
	}
}

func TestParseRequestMapsRolesAndParts(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[{"role":"user","parts":[{"text":"look"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}},{"fileData":{"mimeType":"video/mp4","fileUri":"https://youtu.be/abc"}}]},{"role":"model","parts":[{"text":"weighing","thought":true,"thoughtSignature":"sig-1"},{"functionCall":{"name":"f","args":{"x":1}}}]}]}`)

	if req.Messages[0].Role != ir.RoleUser || req.Messages[1].Role != ir.RoleAssistant {
		t.Fatalf("roles = %q, %q; model maps to assistant", req.Messages[0].Role, req.Messages[1].Role)
	}
	u := req.Messages[0].Content
	if u[1].Type != ir.BlockImage || u[1].Media.Data != "AAAA" {
		t.Errorf("inlineData = %+v", u[1])
	}
	if u[2].Media.URL != "https://youtu.be/abc" {
		t.Errorf("fileData = %+v", u[2])
	}
	m := req.Messages[1].Content
	if m[0].Type != ir.BlockThinking || m[0].Thinking.Signature != "sig-1" {
		t.Errorf("thought = %+v", m[0])
	}
	if m[1].ToolUse == nil || m[1].ToolUse.ID == "" {
		t.Errorf("functionCall = %+v; an absent id must be synthesized", m[1].ToolUse)
	}
}

func TestParseRequestMatchesFunctionResponsesPositionally(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"city":"Oslo"}}},{"functionCall":{"name":"lookup","args":{"city":"Bergen"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"clear"}}},{"functionResponse":{"name":"lookup","response":{"result":"rain"}}}]}]}`)

	calls := req.Messages[0].Content
	resps := req.Messages[1].Content
	if resps[0].ToolResult.ToolUseID != calls[0].ToolUse.ID {
		t.Errorf("first response paired with %q, want %q; matching is positional",
			resps[0].ToolResult.ToolUseID, calls[0].ToolUse.ID)
	}
	if resps[1].ToolResult.ToolUseID != calls[1].ToolUse.ID {
		t.Errorf("second response paired with %q, want %q",
			resps[1].ToolResult.ToolUseID, calls[1].ToolUse.ID)
	}
	if resps[0].ToolResult.Text() != `{"result":"clear"}` {
		t.Errorf("response body = %q", resps[0].ToolResult.Text())
	}
}

func TestParseRequestReadsConfigAndTools(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],"systemInstruction":{"parts":[{"text":"be terse"}]},"tools":[{"functionDeclarations":[{"name":"f","description":"d","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["f"]}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"}],"generationConfig":{"temperature":0.7,"topP":0.9,"topK":40,"maxOutputTokens":512,"stopSequences":["END"],"responseMimeType":"application/json","responseSchema":{"type":"object"},"thinkingConfig":{"thinkingBudget":8000,"includeThoughts":true}}}`)

	if len(req.System) != 1 || req.System[0].Text != "be terse" {
		t.Errorf("system = %+v", req.System)
	}
	if len(req.Tools) != 1 || string(req.Tools[0].Schema) != `{"type":"object"}` {
		t.Errorf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Errorf("tool_choice = %+v; ANY with one allowed name is forcing that tool", req.ToolChoice)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 || req.TopK == nil || *req.TopK != 40 {
		t.Errorf("sampling = %v %v", req.Temperature, req.TopK)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 512 {
		t.Errorf("max tokens = %v", req.MaxTokens)
	}
	if req.ResponseFormat == nil || string(req.ResponseFormat.Schema) != `{"type":"object"}` {
		t.Errorf("response format = %+v", req.ResponseFormat)
	}
	if req.Reasoning == nil || req.Reasoning.Budget != 8000 {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if len(req.Safety) != 1 || req.Safety[0].Threshold != "BLOCK_NONE" {
		t.Errorf("safety = %+v", req.Safety)
	}
}

func TestParseRequestModeAnyWithoutNamesIsAny(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`)
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}
