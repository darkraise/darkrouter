package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func built(t *testing.T, req *ir.Request) (*http.Request, map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := NewFetcher().BuildRequest(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "AIza", Model: "gemini-2.0-flash",
		}, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return hr, body, warns
}

func userMsg(text string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}}}
}

func TestBuildRequestPutsTheModelAndMethodInTheURL(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "AIza" {
		t.Errorf("x-goog-api-key = %q", hr.Header.Get("x-goog-api-key"))
	}
	if hr.URL.Query().Get("key") != "" {
		t.Error("the key belongs in a header, not the query string, where it lands in logs")
	}
}

func TestBuildRequestStreamsWithAltSSE(t *testing.T) {
	hr, body, _ := built(t, &ir.Request{Stream: true, Messages: []ir.Message{userMsg("hi")}})
	if hr.URL.Path != "/v1beta/models/gemini-2.0-flash:streamGenerateContent" {
		t.Errorf("path = %s", hr.URL.Path)
	}
	if hr.URL.Query().Get("alt") != "sse" {
		t.Errorf("query = %s; the JSON-array form is far harder to read incrementally", hr.URL.RawQuery)
	}
	if _, ok := body["stream"]; ok {
		t.Error("Gemini selects streaming by method, not by a body flag")
	}
}

func TestBuildRequestDeclaresEveryFunctionInOneToolsEntry(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Tools: []ir.Tool{
			{Name: "a", Description: "da", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "b", Description: "db", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d entries; one entry per function silently disables calling", len(tools))
	}
	decls := tools[0].(map[string]any)["functionDeclarations"].([]any)
	if len(decls) != 2 {
		t.Fatalf("functionDeclarations = %v", decls)
	}
	if decls[0].(map[string]any)["name"] != "a" {
		t.Errorf("declaration = %v", decls[0])
	}
	if _, ok := decls[0].(map[string]any)["parameters"]; !ok {
		t.Errorf("declaration = %v; Gemini names the schema parameters", decls[0])
	}
}

func TestBuildRequestMapsToolChoiceModes(t *testing.T) {
	cases := []struct {
		mode string
		name string
		want string
	}{
		{"auto", "", "AUTO"},
		{"none", "", "NONE"},
		{"any", "", "ANY"},
		{"tool", "f", "ANY"},
	}
	for _, tc := range cases {
		_, body, _ := built(t, &ir.Request{
			Messages:   []ir.Message{userMsg("hi")},
			ToolChoice: &ir.ToolChoice{Mode: tc.mode, Name: tc.name},
		})
		cfg := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
		if cfg["mode"] != tc.want {
			t.Errorf("mode %q = %v, want %v", tc.mode, cfg["mode"], tc.want)
		}
		if tc.mode == "tool" {
			names := cfg["allowedFunctionNames"].([]any)
			if len(names) != 1 || names[0] != "f" {
				t.Errorf("allowedFunctionNames = %v; forcing one tool needs the allow list", names)
			}
		}
	}
}

func TestBuildRequestFillsGenerationConfig(t *testing.T) {
	temp, top := 0.7, 0.9
	k, max := 40, 512
	_, body, _ := built(t, &ir.Request{
		Messages:       []ir.Message{userMsg("hi")},
		Temperature:    &temp,
		TopP:           &top,
		TopK:           &k,
		MaxTokens:      &max,
		StopSequences:  []string{"END"},
		Reasoning:      &ir.Reasoning{Effort: "medium"},
		ResponseFormat: &ir.ResponseFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	cfg := body["generationConfig"].(map[string]any)
	if cfg["temperature"].(float64) != 0.7 || cfg["topP"].(float64) != 0.9 ||
		cfg["topK"].(float64) != 40 || cfg["maxOutputTokens"].(float64) != 512 {
		t.Errorf("generationConfig = %v", cfg)
	}
	if cfg["stopSequences"].([]any)[0] != "END" {
		t.Errorf("stopSequences = %v", cfg["stopSequences"])
	}
	if cfg["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v; a schema without it is ignored", cfg["responseMimeType"])
	}
	if _, ok := cfg["responseSchema"]; !ok {
		t.Errorf("generationConfig = %v", cfg)
	}
	th := cfg["thinkingConfig"].(map[string]any)
	if th["thinkingBudget"].(float64) != 16384 || th["includeThoughts"] != true {
		t.Errorf("thinkingConfig = %v; medium is 16384 by the fixed table", th)
	}
}

func TestBuildRequestSendsSystemInstructionAndSafety(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{userMsg("hi")},
		Safety: []ir.SafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
		},
	})
	si := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if si["text"] != "be terse" {
		t.Errorf("systemInstruction = %v", body["systemInstruction"])
	}
	s := body["safetySettings"].([]any)[0].(map[string]any)
	if s["category"] != "HARM_CATEGORY_HARASSMENT" || s["threshold"] != "BLOCK_NONE" {
		t.Errorf("safetySettings = %v", body["safetySettings"])
	}
}

func TestBuildRequestWarnsOnParallelToolCallsAndMetadata(t *testing.T) {
	no := false
	_, _, warns := built(t, &ir.Request{
		Messages:          []ir.Message{userMsg("hi")},
		ParallelToolCalls: &no,
		Metadata:          map[string]string{"user_id": "u1"},
	})
	if !hasWarning(warns, "parallel_tool_calls") || !hasWarning(warns, "metadata") {
		t.Errorf("warnings = %+v", warns)
	}
}
