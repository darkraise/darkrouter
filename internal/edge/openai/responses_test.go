package openai

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parseResponses(t *testing.T, body string) (*ir.Request, error) {
	t.Helper()
	req, _, _, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(body)), 1<<20)
	return req, err
}

func parseResponsesEcho(t *testing.T, body string) *responsesEcho {
	t.Helper()
	_, _, echo, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(body)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return echo
}

func TestParseResponsesTurnsABareInputIntoAUserTurn(t *testing.T) {
	req, err := parseResponses(t, `{"model":"gpt-4o","input":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" || len(req.Messages) != 1 {
		t.Fatalf("request = %+v", req)
	}
	if req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "hello" {
		t.Errorf("message = %+v", req.Messages[0])
	}
}

func TestParseResponsesMapsInstructionsToSystem(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","instructions":"be terse","input":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 1 || req.System[0].Text != "be terse" {
		t.Errorf("system = %+v", req.System)
	}
}

func TestParseResponsesReadsMessageItems(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"role":"user","content":"first"},
	  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]},
	  {"type":"message","role":"user","content":[
	     {"type":"input_text","text":"third"},
	     {"type":"input_image","image_url":"data:image/png;base64,AAA"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[1].Role != ir.RoleAssistant || req.Messages[1].Content[0].Text != "second" {
		t.Errorf("assistant turn = %+v", req.Messages[1])
	}
	last := req.Messages[2].Content
	if len(last) != 2 || last[0].Text != "third" || last[1].Type != ir.BlockImage {
		t.Errorf("multimodal turn = %+v", last)
	}
	if last[1].Media == nil || last[1].Media.MIME != "image/png" || last[1].Media.Data != "AAA" {
		t.Errorf("image = %+v", last[1].Media)
	}
}

func TestParseResponsesReadsAToolCallRoundTrip(t *testing.T) {
	// This is what an agent loop replays on its second turn. Losing either
	// half strands the model waiting for a result it already produced.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"role":"user","content":"weather?"},
	  {"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"},
	  {"type":"function_call_output","call_id":"call_1","output":"12C"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	call := req.Messages[1]
	if call.Role != ir.RoleAssistant || call.Content[0].Type != ir.BlockToolUse {
		t.Fatalf("call turn = %+v", call)
	}
	if call.Content[0].ToolUse.ID != "call_1" || call.Content[0].ToolUse.Name != "get_weather" {
		t.Errorf("tool use = %+v", call.Content[0].ToolUse)
	}
	out := req.Messages[2]
	if out.Content[0].Type != ir.BlockToolResult ||
		out.Content[0].ToolResult.ToolUseID != "call_1" ||
		out.Content[0].ToolResult.Text() != "12C" {
		t.Errorf("output turn = %+v", out.Content[0])
	}
}

func TestParseResponsesReadsFunctionTools(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","tools":[
	  {"type":"function","name":"f","description":"d","parameters":{"type":"object"}}],
	  "tool_choice":"required","parallel_tool_calls":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "f" || req.Tools[0].Description != "d" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Errorf("tool choice = %+v", req.ToolChoice)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel_tool_calls = %v", req.ParallelToolCalls)
	}
}

func TestParseResponsesReadsSamplingAndFormat(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","max_output_tokens":128,
	  "temperature":0.2,"top_p":0.9,"stream":true,"reasoning":{"effort":"high"},
	  "text":{"format":{"type":"json_schema","name":"s","schema":{"type":"object"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 128 {
		t.Errorf("max tokens = %v", req.MaxTokens)
	}
	if !req.Stream || req.Temperature == nil || *req.Temperature != 0.2 {
		t.Errorf("request = %+v", req)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "high" {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response format = %+v", req.ResponseFormat)
	}
	// The schema, not only its type. Responses flattens text.format while chat
	// nests it under json_schema, so decoding with chat's struct leaves this
	// nil and the request reaches the provider with no schema at all.
	if !strings.Contains(string(req.ResponseFormat.Schema), `"object"`) {
		t.Errorf("schema = %q; the flattened text.format was not read",
			req.ResponseFormat.Schema)
	}
}

func TestParseResponsesCarriesAReplayedRefusal(t *testing.T) {
	// An assistant turn that refused is replayed on the next turn. Rejecting
	// it would 400 a legitimate agent loop.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot"}]},
	  {"role":"user","content":"why?"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 || req.Messages[0].Content[0].Text != "I cannot" {
		t.Errorf("messages = %+v", req.Messages)
	}
}

func TestParseResponsesWarnsOnStrictTools(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","tools":[
	  {"type":"function","name":"f","parameters":{"type":"object"},"strict":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0].Reason, "strict") {
		t.Errorf("warnings = %+v; a guarantee the client asked for is not forwarded", req.Warnings)
	}
}

func TestParseResponsesRejectsAStatefulRequest(t *testing.T) {
	// Degrading either of these returns a fluent, confident, amnesic answer
	// that looks entirely successful and no client can detect.
	for _, body := range []string{
		`{"model":"m","input":"hi","previous_response_id":"resp_abc"}`,
		`{"model":"m","input":"hi","conversation":"conv_abc"}`,
		`{"model":"m","input":"hi","conversation":{"id":"conv_abc"}}`,
		`{"model":"m","input":"hi","background":true}`,
	} {
		_, err := parseResponses(t, body)
		if err == nil {
			t.Errorf("ParseResponses(%s) served a stateful request", body)
			continue
		}
		if !strings.Contains(err.Error(), "stateless") {
			t.Errorf("err = %v; it must tell the client what will work", err)
		}
	}
}

func TestParseResponsesRejectsBuiltInTools(t *testing.T) {
	for _, kind := range []string{"web_search", "web_search_preview", "file_search",
		"code_interpreter", "image_generation", "computer_use_preview", "mcp", "local_shell"} {
		_, err := parseResponses(t, `{"model":"m","input":"hi","tools":[{"type":"`+kind+`"}]}`)
		if err == nil {
			t.Errorf("a %s tool was accepted; answering without it is the same lie as answering "+
				"without the conversation", kind)
			continue
		}
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("err = %v; it must name the tool that cannot be served", err)
		}
	}
}

func TestParseResponsesAcceptsStore(t *testing.T) {
	// store defaults to true, so refusing it would fail every request an SDK
	// writes with its defaults. The response body is what says the id is not
	// resumable.
	if _, err := parseResponses(t, `{"model":"m","input":"hi","store":true}`); err != nil {
		t.Fatalf("store:true was refused: %v", err)
	}
}

func TestParseResponsesDropsReasoningItemsWithAWarning(t *testing.T) {
	// An encrypted reasoning item means something only to the provider that
	// minted it, and this turn may be going somewhere else entirely.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"xxx"},
	  {"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Errorf("messages = %+v; the reasoning item was replayed", req.Messages)
	}
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0].Reason, "reasoning") {
		t.Errorf("warnings = %+v", req.Warnings)
	}
}

func TestParseResponsesReportsTheSurface(t *testing.T) {
	// The passthrough carries the surface so the executor's record says llm
	// rather than guessing from the route.
	_, pt, _, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi"}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM || pt.ModelField != "model" {
		t.Errorf("passthrough = %+v", pt)
	}
}

func TestWriteResponsesEmitsItems(t *testing.T) {
	w := httptest.NewRecorder()
	err := WriteResponses(w, &ir.Response{
		ID: "chatcmpl-1", Model: "gpt-4o",
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "pondering"}},
			{Type: ir.BlockText, Text: "the answer"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_1", Name: "f", Input: []byte(`{"a":1}`)}},
		},
		StopReason: ir.StopToolUse,
		Usage:      ir.Usage{InputTokens: 5, OutputTokens: 7, ReasoningTokens: 3},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Store  bool   `json:"store"`
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		OutputText string `json:"output_text"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			OutDetails   struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "response" || body.Status != "completed" || body.Model != "gpt-4o" {
		t.Fatalf("body = %s", w.Body.String())
	}
	if !strings.HasPrefix(body.ID, "resp_dr_") {
		t.Errorf("id = %q; it must be legible as non-resumable", body.ID)
	}
	if body.Store {
		t.Error("store = true; Darkrouter persists nothing and the client must be told")
	}
	if len(body.Output) != 3 {
		t.Fatalf("output = %+v", body.Output)
	}
	if body.Output[0].Type != "reasoning" || body.Output[0].Summary[0].Text != "pondering" {
		t.Errorf("reasoning item = %+v", body.Output[0])
	}
	if body.Output[1].Type != "message" || body.Output[1].Role != "assistant" ||
		body.Output[1].Content[0].Type != "output_text" ||
		body.Output[1].Content[0].Text != "the answer" {
		t.Errorf("message item = %+v", body.Output[1])
	}
	if body.Output[2].Type != "function_call" || body.Output[2].CallID != "call_1" ||
		body.Output[2].Name != "f" || body.Output[2].Arguments != `{"a":1}` {
		t.Errorf("function call item = %+v", body.Output[2])
	}
	if body.OutputText != "the answer" {
		t.Errorf("output_text = %q; the SDK convenience field is wrong", body.OutputText)
	}
	if body.Usage.InputTokens != 5 || body.Usage.OutputTokens != 7 ||
		body.Usage.TotalTokens != 12 || body.Usage.OutDetails.ReasoningTokens != 3 {
		t.Errorf("usage = %+v; Responses names these differently from chat", body.Usage)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteResponsesReportsATruncatedAnswerAsIncomplete(t *testing.T) {
	// A client that reads status "completed" stops asking. A length-truncated
	// answer is not completed, and Responses has a field that says so.
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{
		ID: "x", Model: "m",
		Content:    []ir.ContentBlock{{Type: ir.BlockText, Text: "half an ans"}},
		StopReason: ir.StopMaxTokens,
	}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Status     string `json:"status"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "incomplete" {
		t.Errorf("status = %q", body.Status)
	}
	if body.Incomplete == nil || body.Incomplete.Reason != "max_output_tokens" {
		t.Errorf("incomplete_details = %+v", body.Incomplete)
	}
}

func TestWriteResponsesReportsAFilteredAnswerAsIncomplete(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{
		ID: "x", Model: "m", StopReason: ir.StopContentFilter,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: ""}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Status     string `json:"status"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Status != "incomplete" || body.Incomplete == nil ||
		body.Incomplete.Reason != "content_filter" {
		t.Errorf("status = %q, incomplete = %+v", body.Status, body.Incomplete)
	}
}

func TestWriteResponsesEchoesTheRequiredRequestFields(t *testing.T) {
	// tools, tool_choice and parallel_tool_calls have no default in the SDK's
	// Response model; a body omitting them fails strict validation, and the
	// Agents SDK reads response.tools directly.
	echo := parseResponsesEcho(t, `{"model":"m","input":"hi","instructions":"be terse",
	  "temperature":0.25,"top_p":0.9,"parallel_tool_calls":false,
	  "tools":[{"type":"function","name":"f","parameters":{"type":"object"}}],
	  "tool_choice":"required"}`)

	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, echo); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tools", "tool_choice", "parallel_tool_calls",
		"error", "incomplete_details", "instructions", "metadata", "temperature", "top_p"} {
		if _, present := body[k]; !present {
			t.Errorf("response omits %q, which the SDK model requires", k)
		}
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("tools = %v; the client's own array must be echoed", body["tools"])
	}
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v; it must be echoed verbatim, not translated", body["tool_choice"])
	}
	if body["parallel_tool_calls"] != false || body["instructions"] != "be terse" {
		t.Errorf("body = %s", w.Body.String())
	}
	if body["temperature"].(float64) != 0.25 || body["top_p"].(float64) != 0.9 {
		t.Errorf("sampling echo = %v / %v", body["temperature"], body["top_p"])
	}
}

func TestWriteResponsesWithNoEchoStillCarriesTheRequiredFields(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["parallel_tool_calls"] != true || body["tool_choice"] != "auto" {
		t.Errorf("defaults = %v / %v", body["parallel_tool_calls"], body["tool_choice"])
	}
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want an empty array rather than null", body["tools"])
	}
}

func TestWriteResponsesEmitsAnEmptyOutputArrayNotNull(t *testing.T) {
	// An SDK ranges over output. null there is a crash rather than no items.
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"output":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
}
