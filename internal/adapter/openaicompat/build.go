// Package openaicompat speaks the OpenAI wire format to any compatible upstream.
package openaicompat

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

// targetName labels the warnings this kind produces.
const targetName = "openaicompat"

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return buildRequest(ctx, t, req, quirksForTarget(t))
}

func buildRequest(ctx context.Context, t *adapter.Target, req *ir.Request, q quirkSet) (*http.Request, []ir.Warning, error) {
	var warns []ir.Warning
	msgs, mwarns := renderMessages(req, targetName, q.has("no-system-role"), q.has("echo-reasoning-content"))
	warns = append(warns, mwarns...)
	body := map[string]any{
		"model":    t.Model,
		"messages": msgs,
	}
	maxTokensKey := "max_tokens"
	if q.has("max-completion-tokens-name") {
		maxTokensKey = "max_completion_tokens"
	}
	switch {
	case q.has("requires-max-tokens"):
		cap, w := xlate.RequiredMaxTokens(req, targetName, t.Info.MaxOutputTokens)
		warns = append(warns, w...)
		body[maxTokensKey] = cap
	case req.MaxTokens != nil:
		body[maxTokensKey] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		if q.has("temperature-top-p-exclusive") && req.Temperature != nil {
			warns = append(warns, ir.Warning{
				Field: "top_p", Target: targetName,
				Reason: "the target accepts temperature or top_p, not both; top_p was dropped",
			})
		} else {
			body["top_p"] = *req.TopP
		}
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	if req.TopK != nil {
		warns = append(warns, ir.Warning{
			Field: "top_k", Target: targetName, Reason: "no equivalent parameter",
		})
	}
	if len(req.Safety) > 0 {
		warns = append(warns, ir.Warning{
			Field: "safety", Target: targetName, Reason: "safety settings are Gemini-only",
		})
	}
	strict := q.has("strict-unknown-fields")
	if req.Reasoning != nil {
		switch {
		case req.Reasoning.Disabled:
			warns = append(warns, ir.Warning{
				Field: "reasoning.disabled", Target: targetName,
				Reason: "no switch to turn thinking off; the request was sent without a reasoning_effort",
			})
		case strict && (req.Reasoning.Effort != "" || req.Reasoning.Budget > 0):
			warns = append(warns, ir.Warning{
				Field: "reasoning.effort", Target: targetName,
				Reason: "the target rejects unknown fields and reasoning_effort is not one it knows",
			})
		case req.Reasoning.Effort != "":
			body["reasoning_effort"] = req.Reasoning.Effort
		case req.Reasoning.Budget > 0:
			// A budget is finer-grained than an effort, so the conversion is
			// lossy in one direction only and the loss is worth recording.
			body["reasoning_effort"] = xlate.BudgetEffort(req.Reasoning.Budget)
			warns = append(warns, ir.Warning{
				Field: "reasoning.budget", Target: targetName,
				Reason: "converted to the nearest reasoning_effort band",
			})
		}
	}
	if rf := renderResponseFormat(req.ResponseFormat); rf != nil {
		body["response_format"] = rf
	}
	if req.ParallelToolCalls != nil {
		switch {
		case q.has("no-parallel-tool-calls"):
			warns = append(warns, ir.Warning{
				Field: "parallel_tool_calls", Target: targetName,
				Reason: "the target does not accept parallel_tool_calls; the field was dropped",
			})
		case strict:
			warns = append(warns, ir.Warning{
				Field: "parallel_tool_calls", Target: targetName,
				Reason: "the target rejects unknown fields; parallel_tool_calls was dropped",
			})
		default:
			body["parallel_tool_calls"] = *req.ParallelToolCalls
		}
	}
	if md := forwardableMetadata(req.Metadata); len(md) > 0 {
		if strict {
			warns = append(warns, ir.Warning{
				Field: "metadata", Target: targetName,
				Reason: "the target rejects unknown fields; metadata was dropped",
			})
		} else {
			body["metadata"] = md
		}
	}
	if len(req.Tools) > 0 {
		tools, w := renderTools(req.Tools)
		warns = append(warns, w...)
		if len(tools) > 0 {
			body["tools"] = tools
		}
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = renderToolChoice(req.ToolChoice)
	}
	// Extra holds the OpenAI-only fields the inbound dialect could not place
	// in the IR. A key the IR already rendered is not overwritten: the IR is
	// the translated truth, and Extra only fills the gaps around it.
	for k, v := range req.Extra {
		if _, taken := body[k]; !taken {
			body[k] = v
		}
	}
	stream := req.Stream
	if stream && len(req.Tools) > 0 && q.has("no-tool-streaming") {
		// The stream parser recognizes a unary body and replays it as
		// events, so the client still receives its stream.
		stream = false
		warns = append(warns, ir.Warning{
			Field: "stream", Target: targetName,
			Reason: "the target cannot stream tool calls; the request was sent unary",
		})
	}
	if stream {
		body["stream"] = true
		// Compatible providers report no stream usage unless asked.
		if !strict {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, warns, nil
}

// renderResponseFormat emits OpenAI's response_format. strict is sent only
// when the client asked for it: strict mode rejects schemas that are fine
// otherwise (every property required, additionalProperties false), so
// switching it on unasked turns a working request into a 400.
func renderResponseFormat(rf *ir.ResponseFormat) map[string]any {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_object":
		return map[string]any{"type": "json_object"}
	case "json_schema":
		name := rf.Name
		if name == "" {
			name = "response"
		}
		schema := map[string]any{"name": name, "schema": rf.Schema}
		if rf.Strict != nil && *rf.Strict {
			schema["strict"] = true
		}
		return map[string]any{"type": "json_schema", "json_schema": schema}
	}
	return nil
}

func renderTools(tools []ir.Tool) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		if t.BuiltIn() {
			for k := range t.Extra {
				warns = append(warns, ir.Warning{
					Field: "tools[]." + k, Target: targetName,
					Reason: "provider built-in tool has no equivalent; the tool was dropped",
				})
			}
			continue
		}
		fn := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Schema,
		}
		for k, v := range t.Extra {
			if _, taken := fn[k]; !taken {
				fn[k] = v
			}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out, warns
}

func renderToolChoice(tc *ir.ToolChoice) any {
	switch tc.Mode {
	case "auto", "none":
		return tc.Mode
	case "any":
		return "required"
	case "tool":
		return map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
	}
	return "auto"
}

// forwardableMetadata strips the keys Darkrouter uses internally. The
// anthropic_ prefix is transport state the Anthropic edge parks in Metadata so
// the Anthropic adapter can act on it — the version header and the thinking
// mode; forwarding either to an OpenAI upstream would be nonsense at best and a
// rejected request at worst.
func forwardableMetadata(md map[string]string) map[string]string {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md))
	for k, v := range md {
		if strings.HasPrefix(k, "anthropic_") {
			continue
		}
		out[k] = v
	}
	return out
}
