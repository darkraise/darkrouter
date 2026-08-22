// Package openaicompat speaks the OpenAI wire format to any compatible upstream.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	var warns []ir.Warning
	body := map[string]any{
		"model":    t.Model,
		"messages": renderMessages(req),
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		body["reasoning_effort"] = req.Reasoning.Effort
	}
	if len(req.Tools) > 0 {
		body["tools"] = renderTools(req.Tools)
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = renderToolChoice(req.ToolChoice)
	}
	if req.Stream {
		body["stream"] = true
		// Compatible providers report no stream usage unless asked.
		body["stream_options"] = map[string]any{"include_usage": true}
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

func renderMessages(req *ir.Request) []any {
	out := make([]any, 0, len(req.Messages)+1)
	if len(req.System) > 0 {
		var b strings.Builder
		for _, blk := range req.System {
			b.WriteString(blk.Text)
		}
		out = append(out, map[string]any{"role": "system", "content": b.String()})
	}
	for _, m := range req.Messages {
		out = append(out, map[string]any{"role": string(m.Role), "content": renderContent(m.Content)})
	}
	return out
}

// renderContent emits a plain string when every block is text, and the
// multi-part form otherwise. Some compatible providers reject the multi-part
// form for text-only messages.
func renderContent(blocks []ir.ContentBlock) any {
	onlyText := true
	for _, b := range blocks {
		if b.Type != ir.BlockText {
			onlyText = false
			break
		}
	}
	if onlyText {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	parts := make([]any, 0, len(blocks))
	for _, blk := range blocks {
		switch blk.Type {
		case ir.BlockText:
			parts = append(parts, map[string]any{"type": "text", "text": blk.Text})
		case ir.BlockImage:
			if blk.Media == nil {
				continue
			}
			url := blk.Media.URL
			if url == "" && blk.Media.Data != "" {
				url = "data:" + blk.Media.MIME + ";base64," + blk.Media.Data
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
	}
	return parts
}

func renderTools(tools []ir.Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
			},
		})
	}
	return out
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
