package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
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

	messages, mw := renderMessages(req)
	warns = append(warns, mw...)
	body["messages"] = messages

	if sys := renderSystem(req.System); len(sys) > 0 {
		// Converse takes system separately. A system turn folded into messages
		// is a validation error, not a degraded answer.
		body["system"] = sys
	}
	if cfg := inferenceConfig(req); len(cfg) > 0 {
		body["inferenceConfig"] = cfg
	}
	if tc := toolConfig(req); tc != nil {
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
	u := base + "/model/" + escapePathSegment(t.Model) + "/" + route

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	// No credential header. internal/auth signs this request, and a key written
	// here would travel in a header the signature does not cover.
	return hr, warns, nil
}

// escapePathSegment percent-encodes everything outside RFC 3986's unreserved
// set.
//
// url.PathEscape is not equivalent and not usable here: it leaves ':' alone,
// which is legal in a path segment but is not what AWS signs. smithy-go's own
// EscapePath says it outright — "AWS expects every character except these to be
// escaped" — and since every Bedrock inference-profile id contains a colon,
// getting this wrong is a 403 on every request with no cause attached.
func escapePathSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func renderSystem(blocks []ir.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == ir.BlockText && b.Text != "" {
			out = append(out, map[string]any{"text": b.Text})
		}
	}
	return out
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

func toolConfig(req *ir.Request) map[string]any {
	if len(req.Tools) == 0 {
		return nil
	}
	tools := make([]any, 0, len(req.Tools))
	for _, t := range req.Tools {
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
	return cfg
}

// renderMessages maps IR turns to Converse turns.
//
// Converse has no tool role: a tool result is user content carrying a toolResult
// block. Getting that wrong is a 400 on every tool loop, which is why it is the
// first thing the tests assert.
func renderMessages(req *ir.Request) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "assistant"
		}
		content, w := renderBlocks(m.Content)
		warns = append(warns, w...)
		if len(content) == 0 {
			continue
		}
		out = append(out, map[string]any{"role": role, "content": content})
	}
	return out, warns
}

func renderBlocks(blocks []ir.ContentBlock) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
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
			out = append(out, map[string]any{"toolUse": map[string]any{
				"toolUseId": b.ToolUse.ID,
				"name":      b.ToolUse.Name,
				"input":     b.ToolUse.Input,
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
		case ir.BlockThinking, ir.BlockRedactedThinking:
			// reasoningContent is emitted by the model, not accepted from the
			// client on Converse. Replaying it would be a 400.
			warns = append(warns, ir.Warning{
				Field: "thinking", Target: targetName,
				Reason: "Converse does not accept reasoning blocks as input",
			})
		default:
			warns = append(warns, ir.Warning{
				Field: string(b.Type), Target: targetName,
				Reason: "no Converse content block for this type",
			})
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
