package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

const targetName = "bedrock"

// EndpointFor derives the runtime host from the region.
//
// The preset declares base_url: "" because there is no single host — spec §3.3
// makes region an endpoint property rather than part of the model identifier,
// and this is the one place that turns it into a URL.
func EndpointFor(region string) string {
	return "https://bedrock-runtime." + region + ".amazonaws.com"
}

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		if t.Region == "" {
			return nil, nil, fmt.Errorf("bedrock target has neither a base url nor a region")
		}
		base = EndpointFor(t.Region)
	}

	var warns []ir.Warning
	body := map[string]any{}

	// Converse takes system content in a field of its own, and its messages
	// array admits only user and assistant. A system turn left in the
	// conversation would be rendered as a user turn and silently lose its
	// status, so it is collected first — the shared helper is what makes an
	// inbound `developer` role and a `system` message behave the same here as
	// they do for gemini and anthropic.
	sysBlocks, sysWarns := xlate.CollectSystemBlocks(req, targetName)
	warns = append(warns, sysWarns...)
	if sys := renderSystem(sysBlocks); len(sys) > 0 {
		body["system"] = sys
	}

	messages, mw := renderMessages(xlate.NonSystemMessages(req.Messages))
	warns = append(warns, mw...)
	body["messages"] = messages
	if cfg := inferenceConfig(req); len(cfg) > 0 {
		body["inferenceConfig"] = cfg
	}
	if extra, w := additionalFields(t, req); len(extra) > 0 || len(w) > 0 {
		warns = append(warns, w...)
		if len(extra) > 0 {
			body["additionalModelRequestFields"] = extra
		}
	}
	if tc, w := toolConfig(req); tc != nil || len(w) > 0 {
		warns = append(warns, w...)
		if tc != nil {
			body["toolConfig"] = tc
		}
	}
	if req.TopK != nil {
		// topK lives in additionalModelRequestFields, which is per-family and
		// therefore exactly the branching Converse was chosen to avoid.
		warns = append(warns, ir.Warning{
			Field: "top_k", Target: targetName,
			Reason: "Converse has no top_k; it is a per-family additional field",
		})
	}
	if req.ResponseFormat != nil {
		warns = append(warns, ir.Warning{
			Field: "response_format", Target: targetName,
			Reason: "Converse has no structured-output field",
		})
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	route := "converse"
	if req.Stream {
		route = "converse-stream"
	}
	// The colon in a model or inference-profile id is part of the canonical URI
	// the signature covers, so it is escaped here rather than left raw.
	u := base + "/model/" + adapter.EscapePathSegment(t.Model) + "/" + route

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	// No credential header. internal/auth signs this request, and a key written
	// here would travel in a header the signature does not cover.
	return hr, warns, nil
}

func renderSystem(blocks []ir.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == ir.BlockText && b.Text != "" {
			out = append(out, map[string]any{"text": b.Text})
			if b.CacheControl != nil {
				out = append(out, cachePoint())
			}
		}
	}
	return out
}

// cachePoint is Converse's spelling of a cache breakpoint. It is a block of
// its own placed after the content it closes, rather than an attribute on
// that content, and it takes no TTL.
func cachePoint() map[string]any {
	return map[string]any{"cachePoint": map[string]any{"type": "default"}}
}

// isAnthropicModel reports whether a Bedrock model id names a Claude model.
// Every Anthropic id on Bedrock carries the "anthropic." vendor segment,
// whether bare, behind a geo prefix, or inside an inference-profile ARN.
func isAnthropicModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "anthropic.")
}

// additionalFields renders what Converse only accepts per model family.
// reasoning_config is Anthropic's; other publishers spell thinking
// differently or not at all, and sending it to them is a ValidationException.
func additionalFields(t *adapter.Target, req *ir.Request) (map[string]any, []ir.Warning) {
	r := req.Reasoning
	if r == nil || r.Disabled {
		return nil, nil
	}
	if !isAnthropicModel(t.Model) {
		return nil, []ir.Warning{{
			Field: "reasoning", Target: targetName,
			Reason: "reasoning_config is an Anthropic-only additional field; dropped for this publisher",
		}}
	}
	var warns []ir.Warning
	budget := r.Budget
	if budget == 0 {
		budget = xlate.EffortBudget(r.Effort, t.Info.MaxOutputTokens)
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 && budget >= *req.MaxTokens {
		budget = *req.MaxTokens - 1
		warns = append(warns, ir.Warning{
			Field: "reasoning.budget", Target: targetName,
			Reason: "clamped below max_tokens, which Anthropic requires to be larger",
		})
	}
	if budget < 1024 {
		return nil, append(warns, ir.Warning{
			Field: "reasoning", Target: targetName,
			Reason: "budget below Anthropic's 1024-token minimum; thinking disabled",
		})
	}
	return map[string]any{
		"reasoning_config": map[string]any{"type": "enabled", "budget_tokens": budget},
	}, warns
}

func inferenceConfig(req *ir.Request) map[string]any {
	cfg := map[string]any{}
	if req.MaxTokens != nil {
		cfg["maxTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		cfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		cfg["topP"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		cfg["stopSequences"] = req.StopSequences
	}
	return cfg
}

func toolConfig(req *ir.Request) (map[string]any, []ir.Warning) {
	if len(req.Tools) == 0 {
		return nil, nil
	}
	var warns []ir.Warning
	tools := make([]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		// A typed tool runs on its own provider's side. Rendering it as a
		// toolSpec would have the model call a function nobody implements.
		if _, typed := t.Extra["type"]; typed {
			warns = append(warns, ir.Warning{
				Field: "tools[]." + t.Name, Target: targetName,
				Reason: "provider-run tool has no Converse equivalent; dropped",
			})
			continue
		}
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, map[string]any{
			"toolSpec": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				// The schema is wrapped in a json key rather than sent bare.
				"inputSchema": map[string]any{"json": schema},
			},
		})
	}
	if len(tools) == 0 {
		return nil, warns
	}
	cfg := map[string]any{"tools": tools}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case "any":
			cfg["toolChoice"] = map[string]any{"any": map[string]any{}}
		case "tool":
			cfg["toolChoice"] = map[string]any{"tool": map[string]any{"name": tc.Name}}
		case "auto":
			cfg["toolChoice"] = map[string]any{"auto": map[string]any{}}
		}
		// "none" has no Converse spelling. Omitting toolChoice is the closest
		// honest rendering; the model may still call a tool.
	}
	return cfg, warns
}

// renderMessages maps IR turns to Converse turns, merging consecutive
// same-role turns into one.
//
// Converse has no tool role: a tool result is user content carrying a toolResult
// block. Getting that wrong is a 400 on every tool loop, which is why it is the
// first thing the tests assert. It also requires strictly alternating roles,
// and the IR routinely produces two user turns in a row — a tool-result turn
// follows a user turn in every agentic loop.
func renderMessages(msgs []ir.Message) ([]any, []ir.Warning) {
	var (
		warns   []ir.Warning
		out     = make([]any, 0, len(msgs))
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
	for _, m := range msgs {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "assistant"
		}
		blocks, w := renderBlocks(m.Content)
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

func renderBlocks(blocks []ir.ContentBlock) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		before := len(out)
		switch b.Type {
		case ir.BlockText:
			if b.Text != "" {
				out = append(out, map[string]any{"text": b.Text})
			}
		case ir.BlockImage:
			blk, w := imageBlock(b.Media)
			warns = append(warns, w...)
			if blk != nil {
				out = append(out, blk)
			}
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			// Converse takes the arguments as an object and rejects null;
			// a call made with no arguments is {}.
			input := b.ToolUse.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, map[string]any{"toolUse": map[string]any{
				"toolUseId": b.ToolUse.ID,
				"name":      b.ToolUse.Name,
				"input":     input,
			}})
		case ir.BlockToolResult:
			if b.ToolResult == nil {
				continue
			}
			inner, w := renderBlocks(b.ToolResult.Content)
			warns = append(warns, w...)
			res := map[string]any{
				"toolUseId": b.ToolResult.ToolUseID,
				"content":   inner,
			}
			if b.ToolResult.IsError {
				res["status"] = "error"
			}
			out = append(out, map[string]any{"toolResult": res})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			// Replayed byte-identical: an Anthropic model with thinking on
			// verifies the signature chain across a tool loop, and a turn
			// missing its reasoning invalidates the next call.
			out = append(out, map[string]any{"reasoningContent": map[string]any{
				"reasoningText": map[string]any{
					"text": b.Thinking.Text, "signature": b.Thinking.Signature,
				},
			}})
		case ir.BlockRedactedThinking:
			if b.Thinking == nil {
				continue
			}
			out = append(out, map[string]any{"reasoningContent": map[string]any{
				"redactedContent": b.Thinking.Data,
			}})
		default:
			warns = append(warns, ir.Warning{
				Field: string(b.Type), Target: targetName,
				Reason: "no Converse content block for this type",
			})
		}
		// A breakpoint closes the block that carried it, so it follows only a
		// block that was actually rendered.
		if b.CacheControl != nil && len(out) > before {
			out = append(out, cachePoint())
		}
	}
	return out, warns
}

// imageBlock renders inline bytes. Converse takes a bare format word rather
// than a mime type, and takes no URL at all.
func imageBlock(m *ir.Media) (map[string]any, []ir.Warning) {
	if m == nil {
		return nil, nil
	}
	if m.Data == "" {
		return nil, []ir.Warning{{
			Field: "image", Target: targetName,
			Reason: "Converse takes image bytes only; a URL cannot be sent",
		}}
	}
	format, ok := imageFormat(m.MIME)
	if !ok {
		return nil, []ir.Warning{{
			Field: "image", Target: targetName,
			Reason: "Converse accepts png, jpeg, gif and webp only; " + m.MIME + " was dropped",
		}}
	}
	return map[string]any{"image": map[string]any{
		"format": format,
		// The IR carries base64; Converse's bytes member is base64 on the wire.
		"source": map[string]any{"bytes": m.Data},
	}}, nil
}

func imageFormat(mime string) (string, bool) {
	switch mime {
	case "image/png":
		return "png", true
	case "image/jpeg", "image/jpg":
		return "jpeg", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	}
	return "", false
}
