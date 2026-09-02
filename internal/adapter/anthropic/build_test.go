package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// built renders against a manual-budget generation, which is what most of these
// cases are about. builtFor names a different one where the generation is the
// thing under test.
func built(t *testing.T, req *ir.Request) (*http.Request, map[string]any, []ir.Warning) {
	t.Helper()
	return builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, req)
}

func builtFor(t *testing.T, model string, req *ir.Request) (*http.Request, map[string]any, []ir.Warning) {
	t.Helper()
	return builtWith(t, model, adapter.ModelInfo{}, req)
}

// builtWith is builtFor with the catalog's view of the model supplied.
//
// From phase 6 the request shape is decided by these traits rather than by the
// model name, so a test that exercises per-generation behavior has to say which
// generation it means. catalog_traits_test.go in internal/catalog is what
// asserts the shipped preset produces these same values for these same names.
func builtWith(t *testing.T, model string, info adapter.ModelInfo, req *ir.Request) (*http.Request, map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-ant", Model: model, Info: info}, req)
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

func TestBuildRequestSetsURLAndHeaders(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if hr.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("x-api-key") != "sk-ant" {
		t.Errorf("x-api-key = %q", hr.Header.Get("x-api-key"))
	}
	if hr.Header.Get("anthropic-version") != DefaultVersion {
		t.Errorf("anthropic-version = %q", hr.Header.Get("anthropic-version"))
	}
	if hr.Header.Get("Authorization") != "" {
		t.Error("Anthropic authenticates with x-api-key; a bearer header confuses some proxies")
	}
}

func TestBuildRequestForwardsAnInboundVersion(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_version": "2024-10-22"},
	})
	if hr.Header.Get("anthropic-version") != "2024-10-22" {
		t.Errorf("anthropic-version = %q; an inbound version is forwarded, not overridden",
			hr.Header.Get("anthropic-version"))
	}
}

func TestBuildRequestSubstitutesMaxTokens(t *testing.T) {
	_, body, warns := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if body["max_tokens"].(float64) != 4096 {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	if !hasWarning(warns, "max_tokens") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestMergesConsecutiveSameRoleTurns(t *testing.T) {
	_, body, _ := built(t, &ir.Request{Messages: []ir.Message{
		userMsg("first"),
		{Role: ir.RoleTool, Content: []ir.ContentBlock{{
			Type:       ir.BlockToolResult,
			ToolResult: &ir.ToolResult{ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}}},
		}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "ok"}}},
	}})
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2; Anthropic rejects consecutive same-role turns", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || len(first["content"].([]any)) != 2 {
		t.Errorf("merged turn = %v", first)
	}
	if msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("second turn = %v", msgs[1])
	}
}

func TestBuildRequestKeepsSystemAsBlocksWithCacheControl(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		System: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "you are terse",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}},
		Messages: []ir.Message{userMsg("hi")},
	})
	sys := body["system"].([]any)
	block := sys[0].(map[string]any)
	if block["text"] != "you are terse" {
		t.Fatalf("system = %v", sys)
	}
	cc := block["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Errorf("cache_control = %v; a cached system prompt is the point of the array form", cc)
	}
}

func TestBuildRequestClampsThinkingBelowMaxTokens(t *testing.T) {
	n := 2000
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "enabled" {
		t.Fatalf("thinking = %v", th)
	}
	if th["budget_tokens"].(float64) != 1999 {
		t.Errorf("budget_tokens = %v; Anthropic requires max_tokens > budget_tokens", th["budget_tokens"])
	}
	if !hasWarning(warns, "reasoning.budget") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestDisablesThinkingBelowTheMinimumBudget(t *testing.T) {
	n := 900
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v; the clamp landed under the 1024 floor, which is a 400", body["thinking"])
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestUsesAdaptiveThinkingOnNewerModels(t *testing.T) {
	_, body, _ := builtWith(t, "claude-opus-4-7", adapter.ModelInfo{Adaptive: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Budget: 16000},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "adaptive" {
		t.Fatalf("thinking = %v; type enabled is a 400 on Claude 4.7 and later", th)
	}
	if _, ok := th["budget_tokens"]; ok {
		t.Error("adaptive thinking takes no budget")
	}
	if body["output_config"].(map[string]any)["effort"] != "medium" {
		t.Errorf("output_config = %v; a budget bands back to an effort", body["output_config"])
	}
}

func TestBuildRequestUsesManualThinkingOnOlderModels(t *testing.T) {
	n := 32000
	_, body, _ := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Effort: "medium"},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "enabled" {
		t.Fatalf("thinking = %v; type adaptive is a 400 on Claude 4.5 and earlier", th)
	}
	if th["budget_tokens"].(float64) != 16384 {
		t.Errorf("budget_tokens = %v; medium is 16384 by the fixed table", th["budget_tokens"])
	}
}

func TestBuildRequestRoundTripsAnInboundThinkingType(t *testing.T) {
	_, body, _ := builtWith(t, "claude-opus-4-7", adapter.ModelInfo{Adaptive: true, TraitsKnown: true}, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_thinking_type": "disabled"},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Errorf("thinking = %v; an explicit off switch must survive", th)
	}
}

func TestBuildRequestOmitsThinkingWhenDisabledOnAnOlderModel(t *testing.T) {
	_, body, _ := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_thinking_type": "disabled"},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v; older models have no disabled type, they just omit it", body["thinking"])
	}
}

func TestBuildRequestDropsSamplingWhenThinkingIsOn(t *testing.T) {
	temp, wide, narrow := 0.7, 0.5, 0.97
	k := 40
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:    []ir.Message{userMsg("hi")},
		Temperature: &temp,
		TopK:        &k,
		TopP:        &wide,
		Reasoning:   &ir.Reasoning{Budget: 1024},
	})
	if _, ok := body["temperature"]; ok {
		t.Error("temperature is rejected alongside thinking")
	}
	if _, ok := body["top_k"]; ok {
		t.Error("top_k is rejected alongside thinking")
	}
	if _, ok := body["top_p"]; ok {
		t.Error("top_p below 0.95 is rejected alongside thinking")
	}
	if !hasWarning(warns, "temperature") || !hasWarning(warns, "top_k") || !hasWarning(warns, "top_p") {
		t.Errorf("warnings = %+v", warns)
	}

	_, body2, _ := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		TopP:      &narrow,
		Reasoning: &ir.Reasoning{Budget: 1024},
	})
	if body2["top_p"].(float64) != 0.97 {
		t.Errorf("top_p = %v; Anthropic accepts 0.95 to 1 with thinking on", body2["top_p"])
	}
}

func TestBuildRequestDropsAllSamplingOnTheNewestGeneration(t *testing.T) {
	temp, top := 0.7, 0.97
	k := 40
	_, body, warns := builtWith(t, "claude-opus-5", adapter.ModelInfo{Adaptive: true, TraitsKnown: true}, &ir.Request{
		Messages:    []ir.Message{userMsg("hi")},
		Temperature: &temp, TopP: &top, TopK: &k,
	})
	for _, f := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := body[f]; ok {
			t.Errorf("%s survived; this generation rejects any non-default sampling value", f)
		}
		if !hasWarning(warns, f) {
			t.Errorf("warnings = %+v, missing %s", warns, f)
		}
	}
}

func TestBuildRequestDropsThinkingForAForcedToolChoice(t *testing.T) {
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages:   []ir.Message{userMsg("hi")},
		Tools:      []ir.Tool{{Name: "f", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &ir.ToolChoice{Mode: "any"},
		Reasoning:  &ir.Reasoning{Budget: 2048},
	})
	if _, ok := body["thinking"]; ok {
		t.Error("manual thinking is incompatible with a forced tool choice")
	}
	if body["tool_choice"].(map[string]any)["type"] != "any" {
		t.Errorf("tool_choice = %v; the client's explicit instruction is the one that survives",
			body["tool_choice"])
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestDropsAPrefillWhenThinkingIsOn(t *testing.T) {
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		Messages: []ir.Message{
			userMsg("return JSON"),
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
		},
		Reasoning: &ir.Reasoning{Budget: 2048},
	})
	if len(body["messages"].([]any)) != 1 {
		t.Errorf("messages = %v; a prefill is rejected while thinking is on", body["messages"])
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestKeepsAPrefillWithoutThinking(t *testing.T) {
	n := 512
	_, body, warns := builtWith(t, "claude-sonnet-4-5", adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true}, &ir.Request{
		MaxTokens: &n,
		Messages: []ir.Message{
			userMsg("return JSON"),
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
		},
	})
	if len(body["messages"].([]any)) != 2 {
		t.Errorf("messages = %v; prefill is Anthropic's own idiom", body["messages"])
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestWarnsOnAnUnknownModelName(t *testing.T) {
	_, _, warns := builtFor(t, "some-proxy/mystery-model", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Budget: 2048},
	})
	if !hasWarning(warns, "model") {
		t.Errorf("warnings = %+v; the generation was guessed and that must be visible", warns)
	}
}

func TestBuildRequestRendersToolsAndChoice(t *testing.T) {
	no := false
	_, body, _ := built(t, &ir.Request{
		Messages:          []ir.Message{userMsg("hi")},
		Tools:             []ir.Tool{{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:        &ir.ToolChoice{Mode: "any"},
		ParallelToolCalls: &no,
	})
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "f" {
		t.Fatalf("tool = %v", tool)
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Errorf("tool = %v; Anthropic names the schema input_schema", tool)
	}
	tc := body["tool_choice"].(map[string]any)
	if tc["type"] != "any" {
		t.Errorf("tool_choice = %v; OpenAI required maps to Anthropic any", tc)
	}
	if tc["disable_parallel_tool_use"] != true {
		t.Errorf("tool_choice = %v; parallel_tool_calls inverts", tc)
	}
}

func TestBuildRequestEmitsStructuredOutputAndWarnsOnSafety(t *testing.T) {
	_, body, warns := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`),
		},
		Safety: []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
	})
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config = %v; structured output is GA under output_config.format",
			body["output_config"])
	}
	if _, ok := format["schema"]; !ok {
		t.Errorf("format = %v", format)
	}
	if hasWarning(warns, "response_format") {
		t.Error("structured output is no longer dropped, so it must not warn")
	}
	if !hasWarning(warns, "safety") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestTraitsComeFromTheTargetNotTheName(t *testing.T) {
	// The case the name heuristic got wrong: a proxied model whose name says
	// nothing, whose catalog entry says everything.
	_, body, warns := builtWith(t, "default",
		adapter.ModelInfo{Adaptive: true, TraitsKnown: true, MaxOutputTokens: 64_000},
		&ir.Request{
			Messages:  []ir.Message{userMsg("hi")},
			Reasoning: &ir.Reasoning{Effort: "high"},
		})

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("no thinking block: %v", body)
	}
	if thinking["type"] != "adaptive" {
		t.Errorf("thinking type = %v, want adaptive", thinking["type"])
	}
	// A known model must not carry the unrecognized-name warning.
	for _, w := range warns {
		if strings.Contains(w.Reason, "unrecognized") {
			t.Errorf("warned about a model the catalog knows: %v", w)
		}
	}
}

func TestUnknownTraitsStayPermissiveAndWarn(t *testing.T) {
	// The behavior for an unknown model is unchanged from phase 4: the request
	// is shaped by what the client asked for, and the guess is warned about.
	_, _, warns := builtFor(t, "who-knows", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Budget: 8192},
	})
	var warned bool
	for _, w := range warns {
		if w.Field == "model" && strings.Contains(w.Reason, "unrecognized") {
			warned = true
		}
	}
	if !warned {
		t.Error("an unrecognized model produced no warning")
	}
}

func TestTheNameHeuristicIsGone(t *testing.T) {
	// A real Anthropic model name with an empty Info must be treated as
	// unknown. If the table were still consulted this would come back adaptive.
	_, body, warns := builtFor(t, "claude-opus-4-5-20251101", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Effort: "high"},
	})
	// The permissive fallback prefers the manual shape when a budget is
	// derivable, and warns that the shape was guessed.
	var warned bool
	for _, w := range warns {
		if w.Field == "model" && strings.Contains(w.Reason, "unrecognized") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("a bare target did not warn; the name table is still being consulted: %v", body)
	}

	// And structurally: the table and its lookup are deleted, not bypassed.
	src, err := os.ReadFile("build.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"func traitsFor", "var generations"} {
		if strings.Contains(string(src), gone) {
			t.Errorf("%s is still in build.go; the heuristic was not removed", gone)
		}
	}
}

func prefillConversation() []ir.Message {
	return []ir.Message{
		userMsg("return JSON"),
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
	}
}

func TestNoPrefillDropsTheTrailingAssistantTurn(t *testing.T) {
	// The 4.6+ generations return a 400 for a trailing assistant turn whether
	// or not thinking is on. The drop is always warned about: silently
	// removing the client's format hint is how a JSON answer turns into prose
	// with no trace of why.
	_, body, warns := builtWith(t, "claude-opus-4-6",
		adapter.ModelInfo{Adaptive: true, ManualBudget: true, FreeSampling: true, NoPrefill: true, TraitsKnown: true},
		&ir.Request{Messages: prefillConversation()})
	if len(body["messages"].([]any)) != 1 {
		t.Errorf("messages = %v; this model rejects a prefill outright", body["messages"])
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestAdaptiveThinkingWarnsWhenItDropsAPrefill(t *testing.T) {
	_, body, warns := builtWith(t, "claude-opus-4-6",
		adapter.ModelInfo{Adaptive: true, ManualBudget: true, FreeSampling: true, TraitsKnown: true},
		&ir.Request{Messages: prefillConversation(), Reasoning: &ir.Reasoning{Effort: "low"}})
	if len(body["messages"].([]any)) != 1 {
		t.Errorf("messages = %v", body["messages"])
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v; a drop under adaptive thinking was silent", warns)
	}
}

func TestThinkingAlwaysOnNeverSendsDisabled(t *testing.T) {
	info := adapter.ModelInfo{Adaptive: true, ThinkingAlwaysOn: true, TraitsKnown: true}
	// A client that asked for thinking off gets a warning and no field: the
	// model rejects the explicit off switch, and omitting it is the only
	// request it accepts.
	_, body, warns := builtWith(t, "claude-fable-5", info, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_thinking_type": "disabled"},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v; this model rejects thinking.type disabled", body["thinking"])
	}
	if !hasWarning(warns, "thinking") {
		t.Errorf("warnings = %+v; the ignored off switch must be visible", warns)
	}

	// The IR's own spelling of the same instruction.
	_, body, warns = builtWith(t, "claude-fable-5", info, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Disabled: true},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v", body["thinking"])
	}
	if !hasWarning(warns, "thinking") {
		t.Errorf("warnings = %+v", warns)
	}

	// A plain request on a model that thinks by default sends nothing and
	// has nothing to warn about.
	_, body, warns = builtWith(t, "claude-fable-5", info, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v", body["thinking"])
	}
	if hasWarning(warns, "thinking") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestAdaptiveModelsStillSendDisabledWhenAsked(t *testing.T) {
	_, body, _ := builtWith(t, "claude-opus-4-7", adapter.ModelInfo{Adaptive: true, TraitsKnown: true}, &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Disabled: true},
	})
	if th, _ := body["thinking"].(map[string]any); th["type"] != "disabled" {
		t.Errorf("thinking = %v; an explicit off is honoured where the model has the switch", body["thinking"])
	}
}

func TestNoForcedToolChoiceDowngradesToAuto(t *testing.T) {
	info := adapter.ModelInfo{Adaptive: true, ThinkingAlwaysOn: true, NoForcedToolChoice: true, TraitsKnown: true}
	for _, tc := range []*ir.ToolChoice{{Mode: "any"}, {Mode: "tool", Name: "f"}} {
		off := false
		_, body, warns := builtWith(t, "claude-fable-5-1", info, &ir.Request{
			Messages:          []ir.Message{userMsg("hi")},
			Tools:             []ir.Tool{{Name: "f"}},
			ToolChoice:        tc,
			ParallelToolCalls: &off,
		})
		choice := body["tool_choice"].(map[string]any)
		if choice["type"] != "auto" {
			t.Errorf("tool_choice for %s = %v; forced tool use is a 400 on this model", tc.Mode, choice)
		}
		if choice["disable_parallel_tool_use"] != true {
			t.Errorf("tool_choice = %v; the parallel setting survives the downgrade", choice)
		}
		if !hasWarning(warns, "tool_choice") {
			t.Errorf("warnings = %+v", warns)
		}
	}
	// none and auto are unaffected.
	_, body, warns := builtWith(t, "claude-fable-5-1", info, &ir.Request{
		Messages: []ir.Message{userMsg("hi")}, Tools: []ir.Tool{{Name: "f"}},
		ToolChoice: &ir.ToolChoice{Mode: "none"},
	})
	if body["tool_choice"].(map[string]any)["type"] != "none" || hasWarning(warns, "tool_choice") {
		t.Errorf("tool_choice = %v, warnings = %+v", body["tool_choice"], warns)
	}
}

func TestBuildRequestEchoesTheBetaHeader(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic-beta": "context-1m-2025-08-07"},
	})
	if got := hr.Header.Get("anthropic-beta"); got != "context-1m-2025-08-07" {
		t.Errorf("anthropic-beta = %q", got)
	}
	hr, _, _ = built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if got := hr.Header.Get("anthropic-beta"); got != "" {
		t.Errorf("anthropic-beta = %q on a request that carried none", got)
	}
}

func TestRenderToolsReEmitsServerToolsAndCacheControl(t *testing.T) {
	_, body, warns := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Tools: []ir.Tool{
			{Name: "web_search", Extra: map[string]json.RawMessage{
				"type": json.RawMessage(`"web_search_20250305"`), "name": json.RawMessage(`"web_search"`),
				"max_uses": json.RawMessage(`5`)}},
			{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`),
				Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral"}`)}},
		},
	})
	tools := body["tools"].([]any)
	srv := tools[0].(map[string]any)
	if srv["type"] != "web_search_20250305" || srv["max_uses"] != float64(5) {
		t.Errorf("server tool = %v", srv)
	}
	if _, ok := srv["input_schema"]; ok {
		t.Errorf("server tool = %v; a typed tool takes no input_schema", srv)
	}
	plain := tools[1].(map[string]any)
	if plain["name"] != "f" || plain["description"] != "d" {
		t.Errorf("plain tool = %v", plain)
	}
	if cc, _ := plain["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Errorf("plain tool = %v; cache_control was dropped", plain)
	}
	if hasWarning(warns, "cache_control") || hasWarning(warns, "tools[].web_search") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestToolCacheControlCountsTowardTheBreakpointBudget(t *testing.T) {
	cc := &ir.CacheControl{Type: "ephemeral"}
	_, body, warns := built(t, &ir.Request{
		Tools:  []ir.Tool{{Name: "f", Extra: map[string]json.RawMessage{"cache_control": json.RawMessage(`{"type":"ephemeral"}`)}}},
		System: []ir.ContentBlock{{Type: ir.BlockText, Text: "s", CacheControl: cc}},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "a", CacheControl: cc},
			{Type: ir.BlockText, Text: "b", CacheControl: cc},
			{Type: ir.BlockText, Text: "c", CacheControl: cc},
		}}},
	})
	if !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v; five breakpoints across tools, system and messages is one too many", warns)
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if _, ok := content[2].(map[string]any)["cache_control"]; ok {
		t.Errorf("the surplus marker survived: %v", content[2])
	}
}
