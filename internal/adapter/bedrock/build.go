package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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

// regionShape is one hostname label: us-east-1, us-gov-west-1. A dot, slash
// or @ would send the signed request to another host.
var regionShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// CheckRegion refuses a region that would not form the documented endpoint.
func CheckRegion(region string) error {
	if !regionShape.MatchString(region) {
		return fmt.Errorf("bedrock region %q is not a region name such as us-east-1", region)
	}
	return nil
}

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		if t.Region == "" {
			return nil, nil, fmt.Errorf("bedrock target has neither a base url nor a region")
		}
		if err := CheckRegion(t.Region); err != nil {
			return nil, nil, err
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
	marks := &cacheMarks{hourTTL: hourCacheModel(t.Model), toolPoints: toolCacheModel(t.Model)}

	// Converse forwards all of this to Claude's native request, so the shapes
	// a generation refuses, and the controls Anthropic rejects while thinking
	// is on, are refused here exactly as on the direct API. Thinking is
	// settled first because the other rules depend on it.
	shape := claudeShapeOf(t)
	// Tools come before thinking, which depends on the tool choice actually
	// sent: none when every tool was dropped, auto when the model refused a
	// forced one. They also come before system, the order cache markers are
	// placed in.
	tc, toolWarns := toolConfig(req, shape, marks)
	sys, sw := renderSystem(sysBlocks, marks)
	warns = append(warns, sw...)
	if len(sys) > 0 {
		body["system"] = sys
	}
	extra, extraWarns := additionalFields(t, req, sendsForcedChoice(tc))
	thinking := shape.thinkingAlwaysOn || thinkingEnabled(extra)

	msgs := xlate.NonSystemMessages(req.Messages)
	dropPrefill := (thinking || shape.noPrefill) && endsInPrefill(msgs)
	if dropPrefill {
		msgs = msgs[:len(msgs)-1]
	}
	messages, mw := renderMessages(msgs, marks)
	warns = append(warns, mw...)
	if dropPrefill {
		reason := "response prefill is rejected while thinking is on; the turn was dropped"
		if shape.noPrefill {
			reason = "this model rejects a response prefill; the turn was dropped"
		}
		warns = append(warns, ir.Warning{
			Field: "messages[last].assistant_prefill", Target: targetName, Reason: reason,
		})
	}
	body["messages"] = messages
	cfg, cw := inferenceConfig(req, shape, thinking)
	warns = append(warns, cw...)
	if len(cfg) > 0 {
		body["inferenceConfig"] = cfg
	}
	warns = append(warns, extraWarns...)
	if len(extra) > 0 {
		body["additionalModelRequestFields"] = extra
	}
	warns = append(warns, toolWarns...)
	if tc != nil {
		body["toolConfig"] = tc
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

func renderSystem(blocks []ir.ContentBlock, marks *cacheMarks) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == ir.BlockText && b.Text != "" {
			out = append(out, map[string]any{"text": b.Text})
			if b.CacheControl != nil {
				cp, w := marks.point(b.CacheControl)
				warns = append(warns, w...)
				if cp != nil {
					out = append(out, cp)
				}
			}
		}
	}
	return out, warns
}

// cacheMarks tracks the four-breakpoint limit across a whole request. Converse
// forwards the markers to the model, which rejects a fifth with a message that
// does not name the surplus one, so it is dropped here and named.
//
// It also tracks the TTL rules. Only the models hourCacheModel names take a
// ttl at all, and AWS requires every one-hour entry to precede every
// five-minute one, in the order Converse processes them: tools, system,
// messages. Markers are placed here in that same order.
type cacheMarks struct {
	used     int
	hourTTL  bool
	sentFive bool
	// toolPoints is whether the model takes a cachePoint in tools. AWS lists
	// tools among the checkpoint fields for the Claude models in its caching
	// table only.
	toolPoints bool
}

// point is Converse's spelling of a cache breakpoint: a block of its own placed
// after the content it closes, rather than an attribute on that content.
//
// A five-minute marker is sent without a ttl. Five minutes is the default, and
// a model without the ttl field in its cache-point contract, such as Nova,
// accepts the omitted form.
func (c *cacheMarks) point(cc *ir.CacheControl) (map[string]any, []ir.Warning) {
	if c.used >= xlate.MaxCacheBreakpoints {
		return nil, []ir.Warning{{
			Field: "cache_control", Target: targetName,
			Reason: "more than four breakpoints; the surplus marker was dropped",
		}}
	}
	c.used++
	cp := map[string]any{"type": "default"}
	var warns []ir.Warning
	switch {
	case cc.TTL != "1h":
		c.sentFive = true
	case !c.hourTTL:
		c.sentFive = true
		warns = append(warns, ir.Warning{
			Field: "cache_control", Target: targetName,
			Reason: "this model has no one-hour cache TTL on Bedrock; cached for the default five minutes",
		})
	case c.sentFive:
		warns = append(warns, ir.Warning{
			Field: "cache_control", Target: targetName,
			Reason: "a one-hour cache entry must precede every five-minute one; cached for five minutes",
		})
	default:
		cp["ttl"] = "1h"
	}
	return map[string]any{"cachePoint": cp}, warns
}

// hourCacheModels are the Claude families AWS documents the one-hour cache TTL
// for (Bedrock user guide, "Supported models, Regions, and explicit caching
// limits"), keyed by what follows "anthropic.claude-" in a model id. Claude
// 3.7 Sonnet and 3.5 Sonnet v2 are listed with five minutes only; every other
// publisher documents no ttl.
var hourCacheModels = []string{
	"fable-5", "mythos-",
	"opus-5", "opus-4-8", "opus-4-7", "opus-4-6", "opus-4-5",
	"sonnet-5", "sonnet-4-6", "sonnet-4-5",
	"haiku-4-5",
}

// toolCacheModels are the Claude families in the same AWS table, every one of
// which lists tools among its checkpoint fields. Claude models missing from it,
// such as Sonnet 4, Opus 4 and 4.1, and the 3.x Haiku models, are sent no tool
// cachePoint.
var toolCacheModels = append([]string{
	"3-7-sonnet", "3-5-sonnet-20241022-v2",
}, hourCacheModels...)

// hourCacheModel reports whether a Bedrock model id names a model that takes a
// one-hour cache TTL. An id that does not name its model, such as an
// application inference profile ARN, is not assumed to: the one-hour marker
// degrades to five minutes rather than failing the request.
func hourCacheModel(model string) bool {
	return claudeFamilyIn(model, hourCacheModels)
}

// toolCacheModel reports whether a Bedrock model id names a model that takes a
// cachePoint in tools. As with hourCacheModel, an id that does not name its
// model is not assumed to.
func toolCacheModel(model string) bool {
	return claudeFamilyIn(model, toolCacheModels)
}

func claudeFamilyIn(model string, families []string) bool {
	m := strings.ToLower(model)
	i := strings.Index(m, "anthropic.claude-")
	if i < 0 {
		return false
	}
	family := m[i+len("anthropic.claude-"):]
	for _, p := range families {
		if strings.HasPrefix(family, p) {
			return true
		}
	}
	return false
}

// isAnthropicModel reports whether a Bedrock model id names a Claude model.
// Every Anthropic id on Bedrock carries the "anthropic." vendor segment,
// whether bare, behind a geo prefix, or inside an inference-profile ARN.
func isAnthropicModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "anthropic.")
}

// additionalFields renders what Converse only accepts per model family.
// Converse hands these fields to the model's native request, so Claude's are
// Anthropic's own thinking and output_config. Other publishers spell thinking
// differently or not at all, and sending it to them is a ValidationException.
func additionalFields(t *adapter.Target, req *ir.Request, forcedChoice bool) (map[string]any, []ir.Warning) {
	r := req.Reasoning
	if (r != nil && r.Disabled) || req.Metadata["anthropic_thinking_type"] == "disabled" {
		return disabledThinking(t)
	}
	if r == nil {
		return nil, nil
	}
	if !isAnthropicModel(t.Model) {
		return nil, []ir.Warning{{
			Field: "reasoning", Target: targetName,
			Reason: "thinking is an Anthropic-only additional field; dropped for this publisher",
		}}
	}
	// The catalog, not the model id, knows which shape a generation takes. A
	// generation that dropped the manual budget refuses one and takes the
	// adaptive shape instead. TraitsKnown false keeps the permissive
	// fallback an unrecognized or proxied model has always had.
	if t.Info.TraitsKnown && !t.Info.ManualBudget {
		if !t.Info.Adaptive {
			return nil, []ir.Warning{{
				Field: "reasoning", Target: targetName,
				Reason: "this model takes neither a thinking budget nor adaptive thinking; reasoning dropped",
			}}
		}
		fields := map[string]any{"thinking": map[string]any{"type": "adaptive"}}
		effort := xlate.AnthropicEffort(r.Effort)
		if r.Effort == "" {
			effort = xlate.BudgetEffort(r.Budget)
		}
		if effort != "" {
			fields["output_config"] = map[string]any{"effort": effort}
		}
		return fields, nil
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
	// Forced tool use is incompatible with manual thinking, though not with
	// adaptive. The forced tool is the client's explicit instruction and an
	// agentic loop depends on it; the reasoning depth is the softer ask.
	if forcedChoice {
		return nil, append(warns, ir.Warning{
			Field: "reasoning", Target: targetName,
			Reason: "manual thinking is incompatible with a forced tool choice; thinking disabled",
		})
	}
	return map[string]any{
		"thinking": map[string]any{"type": "enabled", "budget_tokens": budget},
	}, warns
}

// disabledThinking renders a client's request for no thinking. A Claude model
// with adaptive thinking may think when the field is absent — Opus 5 and
// Sonnet 5 do by default — so only the explicit off switch turns it off. A
// model that always thinks rejects that switch, and one from before adaptive
// thinking has none; omitting the field is the only request either accepts.
func disabledThinking(t *adapter.Target) (map[string]any, []ir.Warning) {
	if !isAnthropicModel(t.Model) || !t.Info.TraitsKnown {
		return nil, nil
	}
	if t.Info.ThinkingAlwaysOn {
		return nil, []ir.Warning{{
			Field: "reasoning", Target: targetName,
			Reason: "this model cannot turn thinking off; the request was sent with thinking on",
		}}
	}
	if !t.Info.Adaptive {
		return nil, nil
	}
	return map[string]any{"thinking": map[string]any{"type": "disabled"}}, nil
}

// claudeShape is what a Claude generation refuses. The zero restrictions —
// free sampling and nothing refused — belong to another publisher's model and
// to a Claude model the catalog does not know, which is sent as the client
// asked.
type claudeShape struct {
	freeSampling       bool
	noPrefill          bool
	thinkingAlwaysOn   bool
	noForcedToolChoice bool
}

func claudeShapeOf(t *adapter.Target) claudeShape {
	if !isAnthropicModel(t.Model) || !t.Info.TraitsKnown {
		return claudeShape{freeSampling: true}
	}
	return claudeShape{
		freeSampling:       t.Info.FreeSampling,
		noPrefill:          t.Info.NoPrefill,
		thinkingAlwaysOn:   t.Info.ThinkingAlwaysOn,
		noForcedToolChoice: t.Info.NoForcedToolChoice,
	}
}

func thinkingEnabled(extra map[string]any) bool {
	th, _ := extra["thinking"].(map[string]any)
	return th != nil && th["type"] != "disabled"
}

func forcedToolChoice(tc *ir.ToolChoice) bool {
	return tc != nil && (tc.Mode == "any" || tc.Mode == "tool")
}

// sendsForcedChoice reports whether a rendered toolConfig forces a tool.
func sendsForcedChoice(toolConfig map[string]any) bool {
	choice, _ := toolConfig["toolChoice"].(map[string]any)
	_, anyTool := choice["any"]
	_, oneTool := choice["tool"]
	return anyTool || oneTool
}

// endsInPrefill reports whether the conversation ends in the prefill idiom: a
// trailing assistant turn holding only text.
func endsInPrefill(msgs []ir.Message) bool {
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

func inferenceConfig(req *ir.Request, shape claudeShape, thinking bool) (map[string]any, []ir.Warning) {
	var warns []ir.Warning
	drop := func(field, reason string) {
		warns = append(warns, ir.Warning{Field: field, Target: targetName, Reason: reason})
	}
	const sealed = "this model rejects any non-default sampling parameter"
	cfg := map[string]any{}
	if req.MaxTokens != nil {
		cfg["maxTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		switch {
		case !shape.freeSampling:
			drop("temperature", sealed)
		case thinking:
			drop("temperature", "rejected by Anthropic alongside thinking")
		default:
			cfg["temperature"] = *req.Temperature
		}
	}
	if req.TopP != nil {
		switch {
		case !shape.freeSampling:
			drop("top_p", sealed)
		case thinking && (*req.TopP < 0.95 || *req.TopP > 1):
			drop("top_p", "with thinking on, Anthropic accepts top_p only between 0.95 and 1")
		default:
			cfg["topP"] = *req.TopP
		}
	}
	if len(req.StopSequences) > 0 {
		cfg["stopSequences"] = req.StopSequences
	}
	return cfg, warns
}

func toolConfig(req *ir.Request, shape claudeShape, marks *cacheMarks) (map[string]any, []ir.Warning) {
	if len(req.Tools) == 0 {
		return nil, nil
	}
	var warns []ir.Warning
	tools := make([]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		// Another provider's built-in, such as Gemini's googleSearch, has no
		// name, and a toolSpec without one fails validation.
		if t.BuiltIn() {
			for k := range t.Extra {
				warns = append(warns, ir.Warning{
					Field: "tools[]." + k, Target: targetName,
					Reason: "another provider's built-in tool has no Converse equivalent; dropped",
				})
			}
			continue
		}
		// A typed tool runs on its own provider's side. Rendering it as a
		// toolSpec would have the model call a function nobody implements.
		if _, typed := t.Extra["type"]; typed {
			field := "tools[]." + t.Name
			if t.Name == "" {
				field = "tools[].type"
			}
			warns = append(warns, ir.Warning{
				Field: field, Target: targetName,
				Reason: "provider-run tool has no Converse equivalent; dropped",
			})
			continue
		}
		schema := xlate.JSONSchema(t.Schema, t.SchemaDialect)
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
		if raw, ok := t.Extra["cache_control"]; ok {
			cp, w := toolCachePoint(raw, marks)
			warns = append(warns, w...)
			if cp != nil {
				tools = append(tools, cp)
			}
		}
	}
	if len(tools) == 0 {
		if forcedToolChoice(req.ToolChoice) {
			warns = append(warns, ir.Warning{
				Field: "tool_choice", Target: targetName,
				Reason: "no tool was left to declare; the forced tool choice was dropped",
			})
		}
		return nil, warns
	}
	cfg := map[string]any{"tools": tools}
	tc := req.ToolChoice
	if shape.noForcedToolChoice && forcedToolChoice(tc) {
		warns = append(warns, ir.Warning{
			Field: "tool_choice", Target: targetName,
			Reason: "this model rejects a forced tool choice; downgraded to auto",
		})
		tc = &ir.ToolChoice{Mode: "auto"}
	}
	if tc != nil {
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

// toolCachePoint renders a tool's cache_control as the cachePoint that follows
// it in tools, closing every tool declared before it.
func toolCachePoint(raw json.RawMessage, marks *cacheMarks) (map[string]any, []ir.Warning) {
	reason := "this model takes no cache checkpoint in tools on Bedrock; the marker was dropped"
	var cc ir.CacheControl
	if marks.toolPoints {
		if json.Unmarshal(raw, &cc) == nil {
			return marks.point(&cc)
		}
		reason = "the marker is not a cache_control object; it was dropped"
	}
	return nil, []ir.Warning{{Field: "tools[].cache_control", Target: targetName, Reason: reason}}
}

// renderMessages maps IR turns to Converse turns, merging consecutive
// same-role turns into one.
//
// Converse has no tool role: a tool result is user content carrying a toolResult
// block. Getting that wrong is a 400 on every tool loop, which is why it is the
// first thing the tests assert. It also requires strictly alternating roles,
// and the IR routinely produces two user turns in a row — a tool-result turn
// follows a user turn in every agentic loop.
func renderMessages(msgs []ir.Message, marks *cacheMarks) ([]any, []ir.Warning) {
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
		blocks, w := renderBlocks(m.Content, marks)
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

// renderBlocks renders content, placing cache breakpoints only when marks is
// non-nil: tool-result content is rendered without them, because
// ToolResultContentBlock has no cachePoint member.
func renderBlocks(blocks []ir.ContentBlock, marks *cacheMarks) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		before := len(out)
		mark := b.CacheControl
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
			inner, w := renderBlocks(b.ToolResult.Content, nil)
			warns = append(warns, w...)
			// A marker inside the result closes the result as a whole, which
			// is the nearest place Converse admits one.
			for _, c := range b.ToolResult.Content {
				if mark == nil && c.CacheControl != nil {
					mark = c.CacheControl
				}
			}
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
		if marks != nil && mark != nil && len(out) > before {
			cp, w := marks.point(mark)
			warns = append(warns, w...)
			if cp != nil {
				out = append(out, cp)
			}
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
