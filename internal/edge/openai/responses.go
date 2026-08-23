package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// responsesEcho is what the response object must repeat back to the client.
//
// A Responses body echoes the request's own parameters, and the client's SDK
// reads them back. They are held between the parse and the writer rather than
// re-derived from the IR, which cannot reproduce what it does not model.
type responsesEcho struct {
	Instructions      string
	Tools             json.RawMessage
	ToolChoice        json.RawMessage
	ParallelToolCalls *bool
	Temperature       *float64
	TopP              *float64
	MaxOutputTokens   *int
	Metadata          map[string]string
}

type wireResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions"`
	Tools        []wireRespTool  `json:"tools"`
	// RawTools is the same array undecoded, echoed verbatim into the response
	// object. Re-rendering from the decoded form would drop whatever the IR
	// does not model and quietly change what the client is told it sent.
	RawTools          json.RawMessage `json:"-"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	MaxOutputTokens   *int            `json:"max_output_tokens"`
	Temperature       *float64        `json:"temperature"`
	TopP              *float64        `json:"top_p"`
	Stream            bool            `json:"stream"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	Reasoning         *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Text *struct {
		Format *wireRespFormat `json:"format"`
	} `json:"text"`
	Metadata map[string]string `json:"metadata"`

	// The two stateful fields. Their presence is the rejection, so both are
	// raw: a conversation may be an id string or an object.
	PreviousResponseID string          `json:"previous_response_id"`
	Conversation       json.RawMessage `json:"conversation"`
	Background         bool            `json:"background"`
}

type wireRespTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// wireRespFormat is flat, and that is the whole reason it exists rather than
// reusing chat's wireResponseFormat. Chat nests the schema under a json_schema
// key; Responses puts name, schema and strict at the top level of text.format.
// Decoding the flat shape into the nested struct leaves the schema nil and
// ships a structured-output request with no schema at all — a silent drop the
// client cannot see.
type wireRespFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict *bool           `json:"strict"`
}

// wireRespItem is one element of the input array. The vocabulary is a union
// discriminated by type, except that a plain message may omit type entirely.
type wireRespItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`

	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Output    string `json:"output"`
}

type wireRespPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Refusal  string `json:"refusal"`
	ImageURL string `json:"image_url"`
	FileURL  string `json:"file_url"`
	FileData string `json:"file_data"`
	Filename string `json:"filename"`
	FileID   string `json:"file_id"`
}

// ParseResponses returns the echo alongside the request. The echo is what the
// response object must repeat back, and the dialect is constructed per request
// so it can hold it between ParseRequest and the writer.
func ParseResponses(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, *responsesEcho, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, nil, nil, err
	}
	var w wireResponsesRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	// Decoded twice on purpose: once into the typed shape the IR needs, once
	// raw so the response can echo the tools array exactly as it arrived.
	var rawTop struct {
		Tools json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &rawTop)
	w.RawTools = rawTop.Tools

	// Refused before anything else is read: an answer built from a body that
	// carries only the newest turn is fluent, confident and amnesic, and the
	// client cannot tell it apart from a correct one.
	if w.PreviousResponseID != "" {
		return nil, nil, nil, errors.New(
			"previous_response_id is not supported: Darkrouter stores no conversations, " +
				"so send the full input each turn and use stateless requests")
	}
	if len(w.Conversation) > 0 && string(w.Conversation) != "null" {
		return nil, nil, nil, errors.New(
			"conversation is not supported: Darkrouter stores no conversations, " +
				"so send the full input each turn and use stateless requests")
	}
	if w.Background {
		// Answering with a finished response would leave the client polling an
		// id that will never exist.
		return nil, nil, nil, errors.New(
			"background is not supported: Darkrouter has no queue and mints no " +
				"resolvable ids, so use a stateless foreground request")
	}

	req := &ir.Request{
		Model:             w.Model,
		MaxTokens:         w.MaxOutputTokens,
		Temperature:       w.Temperature,
		TopP:              w.TopP,
		Stream:            w.Stream,
		ParallelToolCalls: w.ParallelToolCalls,
		Metadata:          w.Metadata,
	}
	if w.Instructions != "" {
		req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: w.Instructions}}
	}
	if w.Reasoning != nil && w.Reasoning.Effort != "" {
		req.Reasoning = &ir.Reasoning{Effort: w.Reasoning.Effort}
	}
	if f := w.Text; f != nil && f.Format != nil && f.Format.Type == "json_schema" {
		req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: f.Format.Schema}
	}
	if err := applyResponsesTools(req, w.Tools, w.ToolChoice); err != nil {
		return nil, nil, nil, err
	}
	if err := applyResponsesInput(req, w.Input); err != nil {
		return nil, nil, nil, err
	}
	echo := &responsesEcho{
		Instructions: w.Instructions, Tools: rawArray(w.RawTools),
		ToolChoice: w.ToolChoice, ParallelToolCalls: w.ParallelToolCalls,
		Temperature: w.Temperature, TopP: w.TopP,
		MaxOutputTokens: w.MaxOutputTokens, Metadata: w.Metadata,
	}
	return req, &edge.Passthrough{
		Body: body, ModelField: "model", Surface: ir.SurfaceLLM,
	}, echo, nil
}

// rawArray keeps a nil tools array out of the response body, where the field is
// required and null would fail the SDK's model.
func rawArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// applyResponsesTools reads the function tools and refuses every built-in.
//
// Silently answering without a requested web search is the same class of lie as
// silently answering without the stored conversation: the response looks
// entirely successful and nothing in it says the tool never ran.
func applyResponsesTools(req *ir.Request, tools []wireRespTool, choice json.RawMessage) error {
	var warns []ir.Warning
	for _, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return fmt.Errorf(
				"tool type %q is not supported: Darkrouter cannot execute built-in tools, "+
					"and answering without one would look like success", t.Type)
		}
		if t.Name == "" {
			return errors.New("a function tool has no name")
		}
		if t.Strict != nil && *t.Strict {
			// The IR has no strict flag and no adapter renders one, so the
			// model may return arguments that do not validate against the
			// schema. The client asked for a guarantee it will not get.
			warns = append(warns, ir.Warning{
				Field: "tools." + t.Name + ".strict", Target: "responses",
				Reason: "strict schema adherence is not forwarded; arguments may not validate",
			})
		}
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Name, Description: t.Description, Schema: t.Parameters,
		})
	}
	req.Warnings = append(req.Warnings, warns...)
	req.ToolChoice = parseResponsesToolChoice(choice)
	return nil
}

// parseResponsesToolChoice maps the Responses spellings onto the IR's. The
// Responses "required" is the IR's "any", which is Anthropic's spelling and the
// one the IR settled on in phase 1.
func parseResponsesToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || string(raw) == "null" {
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
			return &ir.ToolChoice{Mode: "any"}
		default:
			return nil
		}
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if obj.Type == "function" && obj.Name != "" {
		return &ir.ToolChoice{Mode: "tool", Name: obj.Name}
	}
	return nil
}

func applyResponsesInput(req *ir.Request, raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return errors.New("input is required")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("input: %w", err)
		}
		req.Messages = append(req.Messages, ir.Message{
			Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: s}},
		})
		return nil
	}

	var items []wireRespItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("input must be text or an array of items: %w", err)
	}
	if len(items) == 0 {
		return errors.New("input is empty")
	}
	for i, it := range items {
		switch {
		case it.Type == "function_call":
			args := it.Arguments
			if args == "" {
				args = "{}"
			}
			req.Messages = append(req.Messages, ir.Message{
				Role: ir.RoleAssistant,
				Content: []ir.ContentBlock{{
					Type:    ir.BlockToolUse,
					ToolUse: &ir.ToolUse{ID: it.CallID, Name: it.Name, Input: json.RawMessage(args)},
				}},
			})
		case it.Type == "function_call_output":
			req.Messages = append(req.Messages, ir.Message{
				Role: ir.RoleTool,
				Content: []ir.ContentBlock{{
					Type: ir.BlockToolResult,
					ToolResult: &ir.ToolResult{
						ToolUseID: it.CallID,
						Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: it.Output}},
					},
				}},
			})
		case it.Type == "reasoning":
			// Dropped, not replayed: an encrypted reasoning item is meaningful
			// only to the provider that minted it, and this turn may be routed
			// somewhere else entirely.
			req.Warnings = append(req.Warnings, ir.Warning{
				Field:  fmt.Sprintf("input[%d]", i),
				Target: "responses",
				Reason: "reasoning item dropped; it is only meaningful to the provider that produced it",
			})
		case it.Type == "" || it.Type == "message":
			blocks, err := responsesContent(it.Content)
			if err != nil {
				return fmt.Errorf("input[%d]: %w", i, err)
			}
			req.Messages = append(req.Messages, ir.Message{
				Role: responsesRole(it.Role), Content: blocks,
			})
		default:
			return fmt.Errorf("input[%d]: item type %q is not supported", i, it.Type)
		}
	}
	if len(req.Messages) == 0 {
		return errors.New("input carried no messages")
	}
	return nil
}

func responsesRole(s string) ir.Role {
	switch s {
	case "assistant":
		return ir.RoleAssistant
	case "system", "developer":
		return ir.RoleSystem
	default:
		return ir.RoleUser
	}
}

// responsesContent reads a message's content, which is a string or an array of
// typed parts. input_text and output_text differ only in which side wrote them,
// so both become text blocks.
func responsesContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, errors.New("content is required")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var parts []wireRespPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("content must be text or an array of parts: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text", "summary_text":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		case "refusal":
			// A replayed assistant turn that refused carries one of these. It
			// is history, not a new refusal, so it is carried as text rather
			// than rejected — 400ing a legitimate agent-loop replay would be
			// the worse failure.
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Refusal})
		case "input_image", "image":
			blk, err := responsesImageBlock(p.ImageURL, p.FileID)
			if err != nil {
				return nil, err
			}
			out = append(out, blk)
		case "input_file", "file":
			// A file part carries file_url or inline file_data, not image_url.
			// Reading only the latter would drop the document silently.
			m := &ir.Media{FileID: p.FileID, URL: p.FileURL, Data: p.FileData}
			out = append(out, ir.ContentBlock{Type: ir.BlockDocument, Media: m})
		default:
			return nil, fmt.Errorf("content part type %q is not supported", p.Type)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("content carried no parts")
	}
	return out, nil
}

// responsesImageBlock splits a data URL into its MIME type and payload, and
// passes a plain URL or a provider file handle through. FileID is not
// interchangeable with URL: a target accepting its own handle will reject a
// public address.
func responsesImageBlock(imageURL, fileID string) (ir.ContentBlock, error) {
	if fileID != "" {
		return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{FileID: fileID}}, nil
	}
	if imageURL == "" {
		return ir.ContentBlock{}, errors.New("image part has neither image_url nor file_id")
	}
	if rest, ok := strings.CutPrefix(imageURL, "data:"); ok {
		meta, payload, found := strings.Cut(rest, ",")
		if !found {
			return ir.ContentBlock{}, errors.New("malformed data URL in an image part")
		}
		mime, _, _ := strings.Cut(meta, ";")
		return ir.ContentBlock{
			Type: ir.BlockImage, Media: &ir.Media{MIME: mime, Data: payload},
		}, nil
	}
	return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: imageURL}}, nil
}
