package gemini

import (
	"bytes"
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
	if u[2].Media.FileID != "https://youtu.be/abc" {
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

func TestParseRequestRecordsWhichSchemaFormTheClientUsed(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],"tools":[{"functionDeclarations":[
		{"name":"a","parameters":{"type":"OBJECT"}},
		{"name":"b","parametersJsonSchema":{"type":"object","additionalProperties":false}}]}],
		"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"STRING"}}}`)
	if len(req.Tools) != 2 {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if a := req.Tools[0]; string(a.Schema) != `{"type":"OBJECT"}` || a.SchemaDialect != ir.SchemaOpenAPI {
		t.Errorf("tools[0] = %+v", a)
	}
	if b := req.Tools[1]; string(b.Schema) != `{"type":"object","additionalProperties":false}` || b.SchemaDialect != "" {
		t.Errorf("tools[1] = %+v; parametersJsonSchema is the declaration's schema", b)
	}
	if rf := req.ResponseFormat; rf == nil || rf.SchemaDialect != ir.SchemaOpenAPI {
		t.Errorf("response format = %+v", rf)
	}
}

func TestParseRequestReadsSnakeCaseParametersJSONSchema(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],"tools":[{"function_declarations":[
		{"name":"b","parameters_json_schema":{"type":"object","additionalProperties":false}}]}]}`)
	if len(req.Tools) != 1 {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if b := req.Tools[0]; string(b.Schema) != `{"type":"object","additionalProperties":false}` || b.SchemaDialect != "" {
		t.Errorf("tools[0] = %+v; parameters_json_schema is the declaration's schema", b)
	}
}

func TestParseRequestModeAnyWithoutNamesIsAny(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`)
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

func TestParseRequestKeepsAnAllowlistOfSeveralFunctions(t *testing.T) {
	for _, mode := range []string{"ANY", "VALIDATED"} {
		req := parsed(t, "m:generateContent", "", `{"contents":[],
		  "tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"},{"name":"c"}]},{"googleSearch":{}}],
		  "toolConfig":{"functionCallingConfig":{"mode":"`+mode+`","allowedFunctionNames":["a","b"]}}}`)
		var names []string
		builtin := false
		for _, tool := range req.Tools {
			if tool.Extra != nil {
				builtin = true
				continue
			}
			names = append(names, tool.Name)
		}
		if strings.Join(names, ",") != "a,b" {
			t.Errorf("%s: functions = %v; a function outside allowedFunctionNames stays callable", mode, names)
		}
		if !builtin {
			t.Errorf("%s: tools = %+v; a built-in tool is not a function declaration", mode, req.Tools)
		}
	}
}

func TestParseCarriesTheURLOperationAndQuery(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	r := httptest.NewRequest("POST",
		"/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse&key=secret",
		bytes.NewReader(body))
	r.SetPathValue("model", "gemini-2.0-flash:streamGenerateContent")

	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Method != "streamGenerateContent" {
		t.Errorf("Method = %q, want streamGenerateContent", pt.Method)
	}
	if !pt.Stream {
		t.Error("Stream = false on streamGenerateContent")
	}
	if got := pt.Query.Get("alt"); got != "sse" {
		t.Errorf("alt = %q, want sse", got)
	}
	// The inbound proxy token must not be replayed onto the upstream URL:
	// forwarding it would send Darkrouter's own proxy_token to the vendor.
	if _, present := pt.Query["key"]; present {
		t.Error("the inbound credential survived into the forwarded query")
	}
}

func TestParseLeavesModelFieldEmptyForURLCarriedModels(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	r := httptest.NewRequest("POST", "/v1beta/models/gemini-2.0-flash:generateContent",
		bytes.NewReader(body))
	r.SetPathValue("model", "gemini-2.0-flash:generateContent")

	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.ModelField != "" {
		t.Errorf("ModelField = %q, want empty", pt.ModelField)
	}
	if pt.Method != "generateContent" || pt.Stream {
		t.Errorf("Method = %q Stream = %v", pt.Method, pt.Stream)
	}
}

// fileUri is a provider-side handle, which is what Media.FileID is for.
// Carrying it as Media.URL makes a non-Gemini target render it as a public
// address the vendor will refuse.
func TestAFileURIParsesAsAProviderHandleNotAURL(t *testing.T) {
	req := parsed(t, "m:generateContent", "",
		`{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"video/mp4","fileUri":"https://youtu.be/abc"}}]}]}`)

	blk := req.Messages[0].Content[0]
	if blk.Media.FileID != "https://youtu.be/abc" {
		t.Errorf("Media.FileID = %q, want the fileUri", blk.Media.FileID)
	}
	if blk.Media.URL != "" {
		t.Errorf("Media.URL = %q, want empty; a fileUri is not a public address", blk.Media.URL)
	}
}
