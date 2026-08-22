package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// DefaultVersion is the anthropic-version Darkrouter pins when the client did
// not send one. Anthropic requires the header on every request.
const DefaultVersion = "2023-06-01"

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	cb := &cacheBudget{}
	var warns []ir.Warning

	body := map[string]any{"model": t.Model}

	sysBlocks, w := xlate.CollectSystemBlocks(req, targetName)
	warns = append(warns, w...)
	if len(sysBlocks) > 0 {
		rendered, w := renderBlocks(sysBlocks, cb)
		warns = append(warns, w...)
		body["system"] = rendered
	}

	maxTok, w := xlate.RequiredMaxTokens(req, targetName)
	warns = append(warns, w...)
	body["max_tokens"] = maxTok

	outputConfig := map[string]any{}
	traits := traitsFor(t.Model)

	// Thinking splits by model generation, and the two modes are mutually
	// exclusive per generation: type "enabled" is a 400 on Claude 4.7 and
	// later, and type "adaptive" is a 400 on Claude 4.5 and earlier. Getting
	// this wrong makes every reasoning request against that generation fail.
	thinking, manual := false, false
	mode := thinkingMode(req, traits)
	if mode != modeNone && !traits.known {
		// Warned here rather than unconditionally: an unrecognized name only
		// costs something once the guess decides a wire shape, and warning on
		// every request to a self-hosted Anthropic-compatible endpoint would be
		// noise that trains people to ignore warnings.
		warns = append(warns, ir.Warning{
			Field: "model", Target: targetName,
			Reason: "unrecognized Anthropic model name; the thinking mode was guessed from the request",
		})
	}
	switch mode {
	case modeAdaptive:
		body["thinking"] = map[string]any{"type": "adaptive"}
		if e := adaptiveEffort(req); e != "" {
			outputConfig["effort"] = e
		}
		thinking = true

	case modeManual:
		budget := req.Reasoning.Budget
		if budget == 0 {
			budget = xlate.EffortBudget(req.Reasoning.Effort, 0)
		}
		// max_tokens must be strictly greater than the budget. Clamping keeps a
		// servable request servable; raising max_tokens instead would silently
		// multiply the bill on the one control the client actually set.
		if budget >= maxTok {
			budget = maxTok - 1
			warns = append(warns, ir.Warning{
				Field: "reasoning.budget", Target: targetName,
				Reason: "clamped below max_tokens, which Anthropic requires to be larger",
			})
		}
		switch {
		// The clamp can land under Anthropic's 1024 floor, which is a
		// guaranteed 400. Below it there is no budget worth asking for.
		case budget < 1024:
			warns = append(warns, ir.Warning{
				Field: "reasoning", Target: targetName,
				Reason: "budget below Anthropic's 1024-token minimum; thinking disabled",
			})
		// Forced tool use is incompatible with manual thinking, though not with
		// adaptive. The forced tool is the client's explicit instruction and an
		// agentic loop depends on it; the reasoning depth is the softer ask.
		case req.ToolChoice != nil && (req.ToolChoice.Mode == "any" || req.ToolChoice.Mode == "tool"):
			warns = append(warns, ir.Warning{
				Field: "reasoning", Target: targetName,
				Reason: "manual thinking is incompatible with a forced tool choice; thinking disabled",
			})
		default:
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
			thinking, manual = true, true
		}

	case modeDisabled:
		// Only adaptive-capable models have an explicit off switch; on older
		// ones, omitting the field is what "no thinking" means.
		if traits.adaptive {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	}

	// Sampling parameters. On the newest generation any non-default value is a
	// 400 on every request, thinking or not. On older models the restriction
	// applies only while thinking is on, and top_p survives inside a narrow band.
	drop := func(field, reason string) {
		warns = append(warns, ir.Warning{Field: field, Target: targetName, Reason: reason})
	}
	const sealed = "this model rejects any non-default sampling parameter"
	const withThinking = "rejected by Anthropic alongside thinking"
	if req.Temperature != nil {
		switch {
		case !traits.freeSampling:
			drop("temperature", sealed)
		case thinking:
			drop("temperature", withThinking)
		default:
			body["temperature"] = *req.Temperature
		}
	}
	if req.TopP != nil {
		switch {
		case !traits.freeSampling:
			drop("top_p", sealed)
		case thinking && (*req.TopP < 0.95 || *req.TopP > 1):
			drop("top_p", "with thinking on, Anthropic accepts top_p only between 0.95 and 1")
		default:
			body["top_p"] = *req.TopP
		}
	}
	if req.TopK != nil {
		switch {
		case !traits.freeSampling:
			drop("top_k", sealed)
		case thinking:
			drop("top_k", withThinking)
		default:
			body["top_k"] = *req.TopK
		}
	}

	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}
	if req.Stream {
		body["stream"] = true
	}
	if len(req.Tools) > 0 {
		body["tools"] = renderTools(req.Tools)
	}
	if tc := renderToolChoice(req.ToolChoice, req.ParallelToolCalls); tc != nil {
		body["tool_choice"] = tc
	}
	// Structured output is generally available: no beta header, and the schema
	// lives under output_config.format.
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		outputConfig["format"] = map[string]any{
			"type": "json_schema", "schema": req.ResponseFormat.Schema,
		}
	}
	if len(outputConfig) > 0 {
		body["output_config"] = outputConfig
	}
	if len(req.Safety) > 0 {
		warns = append(warns, ir.Warning{
			Field: "safety", Target: targetName, Reason: "safety settings are Gemini-only",
		})
	}
	if uid := req.Metadata["user_id"]; uid != "" {
		body["metadata"] = map[string]any{"user_id": uid}
	}

	// Rendered last, because the assistant-prefill rule depends on whether
	// thinking ended up enabled: a prefill is rejected while it is.
	msgs, w := renderMessages(req, cb, thinking)
	warns = append(warns, w...)
	if thinking && manual && droppedPrefill(req) {
		warns = append(warns, ir.Warning{
			Field: "messages[last].assistant_prefill", Target: targetName,
			Reason: "response prefill is rejected while thinking is on; the turn was dropped",
		})
	}
	body["messages"] = msgs

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/messages"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("x-api-key", t.APIKey)
	}
	version := DefaultVersion
	if v := req.Metadata["anthropic_version"]; v != "" {
		version = v
	}
	hr.Header.Set("anthropic-version", version)
	return hr, warns, nil
}

// renderMessages merges consecutive same-role turns. Anthropic requires
// alternating roles, and the IR routinely produces two user turns in a row —
// a tool-result turn follows a user turn in every agentic loop.
//
// A trailing text-only assistant turn is Anthropic's prefill idiom and is
// normally preserved. It is dropped when thinking is on, which Anthropic
// rejects outright.
func renderMessages(req *ir.Request, cb *cacheBudget, thinking bool) ([]any, []ir.Warning) {
	var (
		out     []any
		warns   []ir.Warning
		curRole string
		content []any
	)
	flush := func() {
		if curRole == "" {
			return
		}
		out = append(out, map[string]any{"role": curRole, "content": content})
		curRole, content = "", nil
	}

	msgs := xlate.NonSystemMessages(req.Messages)
	if thinking && droppedPrefill(req) {
		msgs = msgs[:len(msgs)-1]
	}
	for _, m := range msgs {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "assistant"
		}
		blocks, w := renderBlocks(m.Content, cb)
		warns = append(warns, w...)
		if len(blocks) == 0 {
			continue
		}
		if role != curRole {
			flush()
			curRole = role
		}
		content = append(content, blocks...)
	}
	flush()
	return out, warns
}

// droppedPrefill reports whether the conversation ends in the prefill idiom —
// a trailing assistant turn holding only text. Anthropic rejects a prefill
// while thinking is on, so that combination has to give one of them up.
func droppedPrefill(req *ir.Request) bool {
	msgs := xlate.NonSystemMessages(req.Messages)
	if len(msgs) == 0 {
		return false
	}
	last := msgs[len(msgs)-1]
	if last.Role != ir.RoleAssistant || len(last.Content) == 0 {
		return false
	}
	for _, b := range last.Content {
		if b.Type != ir.BlockText {
			return false
		}
	}
	return true
}

// modelTraits records what one Anthropic model generation accepts.
type modelTraits struct {
	adaptive     bool // accepts thinking: {"type": "adaptive"}
	manualBudget bool // accepts thinking: {"type": "enabled", budget_tokens}
	freeSampling bool // accepts non-default temperature, top_p, or top_k
	known        bool
}

// generations maps a model-name fragment to its traits, most specific first.
//
// Reading the generation off the name is a heuristic, and it is here because
// there is no catalog until Phase 6 — the same technique internal/tokenize uses
// to pick a vocabulary. It is checked longest-first because "opus-4-5" and
// "opus-4" would otherwise both match the same name.
var generations = []struct {
	fragment string
	traits   modelTraits
}{
	{"mythos-preview", modelTraits{adaptive: true, manualBudget: true, known: true}},
	{"opus-4-7", modelTraits{adaptive: true, known: true}},
	{"opus-4-8", modelTraits{adaptive: true, known: true}},
	{"opus-5", modelTraits{adaptive: true, known: true}},
	{"sonnet-5", modelTraits{adaptive: true, known: true}},
	{"fable-5", modelTraits{adaptive: true, known: true}},
	{"mythos-5", modelTraits{adaptive: true, known: true}},
	{"opus-4-6", modelTraits{adaptive: true, manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4-6", modelTraits{adaptive: true, manualBudget: true, freeSampling: true, known: true}},
	{"opus-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"haiku-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"opus-4-1", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"opus-4", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"claude-3", modelTraits{manualBudget: true, freeSampling: true, known: true}},
}

// traitsFor reads a model name. Dots become dashes first, because proxies spell
// the same model "claude-sonnet-4.5" where Anthropic spells it
// "claude-sonnet-4-5-20250929". An unrecognized name gets the permissive set,
// so the request is shaped by what the client asked for rather than by a guess.
func traitsFor(model string) modelTraits {
	name := strings.ReplaceAll(strings.ToLower(model), ".", "-")
	for _, g := range generations {
		if strings.Contains(name, g.fragment) {
			return g.traits
		}
	}
	return modelTraits{adaptive: true, manualBudget: true, freeSampling: true}
}

type thinkingChoice int

const (
	modeNone thinkingChoice = iota
	modeAdaptive
	modeManual
	modeDisabled
)

// thinkingMode picks the wire shape. The client's own spelling decides what it
// wants; the model's traits decide what it can have, and the conversion runs
// through xlate so a request reasons the same depth either way.
func thinkingMode(req *ir.Request, traits modelTraits) thinkingChoice {
	inbound := req.Metadata["anthropic_thinking_type"]
	if inbound == "disabled" {
		return modeDisabled
	}
	if req.Reasoning == nil && inbound == "" {
		return modeNone
	}
	// An explicit token budget is evidence the client targets a budget-taking
	// model; effort or an adaptive config is evidence of the opposite. Where the
	// target cannot honor the client's shape, convert rather than fail.
	wantsManual := inbound == "enabled" || (req.Reasoning != nil && req.Reasoning.Budget > 0)
	if wantsManual && traits.manualBudget && req.Reasoning != nil {
		return modeManual
	}
	if traits.adaptive {
		return modeAdaptive
	}
	// Manual mode needs a budget to state. A client that asked for thinking
	// without one, against a model with no adaptive mode, gets none.
	if traits.manualBudget && req.Reasoning != nil {
		return modeManual
	}
	return modeNone
}

// adaptiveEffort is the depth control adaptive thinking takes in place of a
// budget. A client that sent a budget has it banded back to an effort here.
func adaptiveEffort(req *ir.Request) string {
	if req.Reasoning == nil {
		return ""
	}
	if req.Reasoning.Effort != "" {
		return strings.ToLower(req.Reasoning.Effort)
	}
	return xlate.BudgetEffort(req.Reasoning.Budget)
}

func renderTools(tools []ir.Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		// A tool with no schema still needs one: Anthropic rejects a null
		// input_schema outright.
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": schema,
		})
	}
	return out
}

// renderToolChoice maps the IR's four modes. disable_parallel_tool_use lives
// inside tool_choice rather than at the top level, so a request that only sets
// parallel_tool_calls still needs a choice object to hang it on.
func renderToolChoice(tc *ir.ToolChoice, parallel *bool) map[string]any {
	if tc == nil && parallel == nil {
		return nil
	}
	m := map[string]any{"type": "auto"}
	if tc != nil {
		switch tc.Mode {
		case "none":
			m = map[string]any{"type": "none"}
		case "any":
			m = map[string]any{"type": "any"}
		case "tool":
			m = map[string]any{"type": "tool", "name": tc.Name}
		}
	}
	if parallel != nil && !*parallel {
		m["disable_parallel_tool_use"] = true
	}
	return m
}
