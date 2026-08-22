// Package openai implements the OpenAI chat-completions inbound dialect.
package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireRequest struct {
	Model       string          `json:"model"`
	Messages    []wireMessage   `json:"messages"`
	Tools       []wireTool      `json:"tools"`
	ToolChoice  json.RawMessage `json:"tool_choice"`
	MaxTokens   *int            `json:"max_tokens"`
	Temperature *float64        `json:"temperature"`
	TopP        *float64        `json:"top_p"`
	Stop        []string        `json:"stop"`
	Stream      bool            `json:"stream"`
	Reasoning   *string         `json:"reasoning_effort"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type wireTool struct {
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// ParseRequest reads the body fully — retrying requires replaying it — and
// converts it to the canonical IR.
func ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > maxBody {
		return nil, nil, fmt.Errorf("request body exceeds %d bytes", maxBody)
	}
	var w wireRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON body: %w", err)
	}

	req := &ir.Request{
		Model:         w.Model,
		MaxTokens:     w.MaxTokens,
		Temperature:   w.Temperature,
		TopP:          w.TopP,
		StopSequences: w.Stop,
		Stream:        w.Stream,
	}
	if w.Reasoning != nil {
		req.Reasoning = &ir.Reasoning{Effort: *w.Reasoning}
	}
	for _, m := range w.Messages {
		msg := ir.Message{Role: mapRole(m.Role)}
		blocks, err := parseContent(m.Content)
		if err != nil {
			return nil, nil, err
		}
		msg.Content = blocks
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Function.Name, Description: t.Function.Description, Schema: t.Function.Parameters,
		})
	}
	req.ToolChoice = parseToolChoice(w.ToolChoice)

	return req, &edge.Passthrough{Body: body, ModelField: "model", Surface: "llm"}, nil
}

func mapRole(role string) ir.Role {
	switch role {
	case "system", "developer": // newer clients send "developer" for system
		return ir.RoleSystem
	case "assistant":
		return ir.RoleAssistant
	case "tool", "function":
		return ir.RoleTool
	default:
		return ir.RoleUser
	}
}

// parseContent accepts both the plain-string and multi-part forms.
func parseContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var parts []wirePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported message content: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: p.ImageURL.URL}})
		}
	}
	return out, nil
}

func parseToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto":
			return &ir.ToolChoice{Mode: "auto"}
		case "none":
			return &ir.ToolChoice{Mode: "none"}
		case "required":
			return &ir.ToolChoice{Mode: "any"} // OpenAI "required" == Anthropic "any"
		}
		return nil
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err == nil && named.Function.Name != "" {
		return &ir.ToolChoice{Mode: "tool", Name: named.Function.Name}
	}
	return nil
}
