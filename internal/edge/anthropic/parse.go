// Package anthropic implements the Anthropic Messages inbound dialect.
package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireRequest struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools"`
	ToolChoice    *wireToolChoice `json:"tool_choice"`
	MaxTokens     *int            `json:"max_tokens"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Thinking      *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
	Metadata map[string]string `json:"metadata"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
}

type wireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
	FileID    string `json:"file_id"`
}

type wireBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Thinking     string          `json:"thinking"`
	Signature    string          `json:"signature"`
	Data         string          `json:"data"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Input        json.RawMessage `json:"input"`
	ToolUseID    string          `json:"tool_use_id"`
	IsError      bool            `json:"is_error"`
	Content      json.RawMessage `json:"content"`
	Source       *wireSource     `json:"source"`
	CacheControl *struct {
		Type string `json:"type"`
		TTL  string `json:"ttl"`
	} `json:"cache_control"`
}

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
		TopK:          w.TopK,
		StopSequences: w.StopSequences,
		Stream:        w.Stream,
		Metadata:      w.Metadata,
	}
	// The inbound version is transport state the Anthropic adapter echoes
	// upstream. Every other adapter strips it.
	if v := r.Header.Get("anthropic-version"); v != "" {
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		req.Metadata["anthropic_version"] = v
	}
	if w.Thinking != nil {
		if w.Thinking.BudgetTokens > 0 {
			req.Reasoning = &ir.Reasoning{Budget: w.Thinking.BudgetTokens}
		}
		// The mode itself is transport state, like the version header: the
		// Anthropic adapter needs it to choose between the manual and adaptive
		// shapes, and a client that explicitly disabled thinking must not have
		// that instruction silently discarded.
		if w.Thinking.Type != "" {
			if req.Metadata == nil {
				req.Metadata = map[string]string{}
			}
			req.Metadata["anthropic_thinking_type"] = w.Thinking.Type
		}
	}

	sys, err := parseContent(w.System)
	if err != nil {
		return nil, nil, err
	}
	req.System = sys

	for _, m := range w.Messages {
		blocks, err := parseContent(m.Content)
		if err != nil {
			return nil, nil, err
		}
		role := ir.RoleUser
		if m.Role == "assistant" {
			role = ir.RoleAssistant
		}
		req.Messages = append(req.Messages, ir.Message{Role: role, Content: blocks})
	}

	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Name, Description: t.Description, Schema: t.InputSchema,
		})
	}
	if w.ToolChoice != nil {
		switch w.ToolChoice.Type {
		case "none":
			req.ToolChoice = &ir.ToolChoice{Mode: "none"}
		case "any":
			req.ToolChoice = &ir.ToolChoice{Mode: "any"}
		case "tool":
			req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: w.ToolChoice.Name}
		default:
			req.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		}
		if d := w.ToolChoice.DisableParallelToolUse; d != nil {
			parallel := !*d
			req.ParallelToolCalls = &parallel
		}
	}

	return req, &edge.Passthrough{
		Body: body, ModelField: "model", Surface: ir.SurfaceLLM, Stream: req.Stream,
	}, nil
}

// parseContent accepts both the plain-string and block-array forms. Anthropic
// permits the string form for system and for any message.
func parseContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var blocks []wireBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("unsupported content: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		blk, err := parseBlock(b)
		if err != nil {
			return nil, err
		}
		if blk.Type == "" {
			continue
		}
		if b.CacheControl != nil {
			blk.CacheControl = &ir.CacheControl{Type: b.CacheControl.Type, TTL: b.CacheControl.TTL}
		}
		out = append(out, blk)
	}
	return out, nil
}

func parseBlock(b wireBlock) (ir.ContentBlock, error) {
	switch b.Type {
	case "text":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, nil
	case "thinking":
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: b.Thinking, Signature: b.Signature,
		}}, nil
	case "redacted_thinking":
		return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: b.Data}}, nil
	case "image":
		return ir.ContentBlock{Type: ir.BlockImage, Media: sourceToMedia(b.Source)}, nil
	case "document":
		return ir.ContentBlock{Type: ir.BlockDocument, Media: sourceToMedia(b.Source)}, nil
	case "tool_use":
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ID, Name: b.Name, Input: b.Input,
		}}, nil
	case "tool_result":
		inner, err := parseContent(b.Content)
		if err != nil {
			return ir.ContentBlock{}, err
		}
		return ir.ContentBlock{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
			ToolUseID: b.ToolUseID, IsError: b.IsError, Content: inner,
		}}, nil
	default:
		// An unrecognized block is skipped rather than rejected: Anthropic adds
		// block types, and a gateway that 400s on a new one is worse than one
		// that forwards what it understands.
		return ir.ContentBlock{}, nil
	}
}

func sourceToMedia(s *wireSource) *ir.Media {
	if s == nil {
		return nil
	}
	return &ir.Media{MIME: s.MediaType, Data: s.Data, URL: s.URL, FileID: s.FileID}
}
