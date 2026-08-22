// Package openai implements the OpenAI chat-completions inbound dialect.
package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireRequest struct {
	Model               string              `json:"model"`
	Messages            []wireMessage       `json:"messages"`
	Tools               []wireTool          `json:"tools"`
	Functions           []wireFunction      `json:"functions"`
	ToolChoice          json.RawMessage     `json:"tool_choice"`
	FunctionCall        json.RawMessage     `json:"function_call"`
	MaxTokens           *int                `json:"max_tokens"`
	MaxCompletionTokens *int                `json:"max_completion_tokens"`
	Temperature         *float64            `json:"temperature"`
	TopP                *float64            `json:"top_p"`
	Stop                json.RawMessage     `json:"stop"`
	Stream              bool                `json:"stream"`
	Reasoning           *string             `json:"reasoning_effort"`
	ResponseFormat      *wireResponseFormat `json:"response_format"`
	ParallelToolCalls   *bool               `json:"parallel_tool_calls"`
	Metadata            map[string]string   `json:"metadata"`
}

type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []wireToolCall  `json:"tool_calls"`
	// FunctionCall is the pre-2023 single-call form. Some SDK versions still
	// replay it from stored history.
	FunctionCall *wireFunctionCall `json:"function_call"`
}

type wireToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireTool struct {
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireResponseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
	File *struct {
		FileID   string `json:"file_id"`
		FileData string `json:"file_data"`
		Filename string `json:"filename"`
	} `json:"file"`
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
		Model:             w.Model,
		MaxTokens:         w.MaxTokens,
		Temperature:       w.Temperature,
		TopP:              w.TopP,
		StopSequences:     parseStop(w.Stop),
		Stream:            w.Stream,
		ParallelToolCalls: w.ParallelToolCalls,
		Metadata:          w.Metadata,
	}
	// max_completion_tokens is the current spelling and wins where a client
	// sends both, which SDKs do while they migrate.
	if w.MaxCompletionTokens != nil {
		req.MaxTokens = w.MaxCompletionTokens
	}
	if w.Reasoning != nil {
		req.Reasoning = &ir.Reasoning{Effort: *w.Reasoning}
	}
	if w.ResponseFormat != nil && w.ResponseFormat.Type == "json_schema" && w.ResponseFormat.JSONSchema != nil {
		req.ResponseFormat = &ir.ResponseFormat{
			Type: "json_schema", Schema: w.ResponseFormat.JSONSchema.Schema,
		}
	}
	for i, m := range w.Messages {
		msg, err := parseMessage(i, m)
		if err != nil {
			return nil, nil, err
		}
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Function.Name, Description: t.Function.Description, Schema: t.Function.Parameters,
		})
	}
	// Legacy declarations are translated rather than rejected: nothing
	// downstream should have to know the old spelling existed.
	for _, f := range w.Functions {
		req.Tools = append(req.Tools, ir.Tool{
			Name: f.Name, Description: f.Description, Schema: f.Parameters,
		})
	}
	req.ToolChoice = parseToolChoice(w.ToolChoice)
	if req.ToolChoice == nil {
		req.ToolChoice = parseToolChoice(w.FunctionCall)
	}

	return req, &edge.Passthrough{Body: body, ModelField: "model", Surface: ir.SurfaceLLM}, nil
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
			// A data URI is inline content, not an address. Leaving it in URL
			// makes Anthropic emit source.type "url" for base64 and makes
			// Gemini try to fetch "data:", dropping the image entirely.
			if mime, data := splitDataURI(p.ImageURL.URL); data != p.ImageURL.URL {
				out = append(out, ir.ContentBlock{Type: ir.BlockImage,
					Media: &ir.Media{MIME: mime, Data: data}})
				continue
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: p.ImageURL.URL}})
		case "input_audio":
			if p.InputAudio == nil {
				continue
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockAudio, Media: &ir.Media{
				MIME: "audio/" + p.InputAudio.Format, Data: p.InputAudio.Data,
			}})
		case "file":
			if p.File == nil {
				continue
			}
			mime, data := splitDataURI(p.File.FileData)
			out = append(out, ir.ContentBlock{Type: ir.BlockDocument, Media: &ir.Media{
				MIME: mime, Data: data, FileID: p.File.FileID,
			}})
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
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err == nil {
		if named.Function.Name != "" {
			return &ir.ToolChoice{Mode: "tool", Name: named.Function.Name}
		}
		if named.Name != "" {
			return &ir.ToolChoice{Mode: "tool", Name: named.Name}
		}
	}
	return nil
}

// parseMessage converts one wire message. The turn index is needed because a
// legacy function message carries no call id and one has to be synthesized
// from its position.
func parseMessage(turn int, m wireMessage) (ir.Message, error) {
	role := mapRole(m.Role)
	blocks, err := parseContent(m.Content)
	if err != nil {
		return ir.Message{}, err
	}

	if role == ir.RoleTool {
		id := m.ToolCallID
		if id == "" {
			id = xlate.SyntheticToolCallID(turn, 0)
		}
		return ir.Message{Role: role, Content: []ir.ContentBlock{{
			Type:       ir.BlockToolResult,
			ToolResult: &ir.ToolResult{ToolUseID: id, Content: blocks},
		}}}, nil
	}

	for i, tc := range m.ToolCalls {
		id := tc.ID
		if id == "" {
			id = xlate.SyntheticToolCallID(turn, i)
		}
		blocks = append(blocks, ir.ContentBlock{
			Type: ir.BlockToolUse,
			ToolUse: &ir.ToolUse{
				ID: id, Name: tc.Function.Name,
				Input: json.RawMessage(argumentsOrEmpty(tc.Function.Arguments)),
			},
		})
	}
	if m.FunctionCall != nil {
		blocks = append(blocks, ir.ContentBlock{
			Type: ir.BlockToolUse,
			ToolUse: &ir.ToolUse{
				ID: xlate.SyntheticToolCallID(turn, len(m.ToolCalls)), Name: m.FunctionCall.Name,
				Input: json.RawMessage(argumentsOrEmpty(m.FunctionCall.Arguments)),
			},
		})
	}
	return ir.Message{Role: role, Content: blocks}, nil
}

// argumentsOrEmpty guards the IR against an empty Input, which would serialize
// as bare nothing rather than an empty object and be rejected by every target.
func argumentsOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// parseStop accepts both shapes OpenAI documents. A bare string is common
// enough that rejecting it fails real requests with a JSON error.
func parseStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

// splitDataURI pulls the MIME type and payload out of a data URI. A value that
// is not one is returned as opaque data, since some clients send bare base64.
func splitDataURI(s string) (mime, data string) {
	if !strings.HasPrefix(s, "data:") {
		return "", s
	}
	rest := strings.TrimPrefix(s, "data:")
	head, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", s
	}
	return strings.TrimSuffix(head, ";base64"), payload
}
