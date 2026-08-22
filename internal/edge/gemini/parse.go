// Package gemini implements the Google Gemini inbound dialect.
package gemini

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

// ExtractModel splits the single path segment Gemini puts the model in.
//
// net/http's {model} wildcard already percent-decodes, and an encoded slash
// does not split the segment — so `openrouter%2Fanthropic%2Fclaude-sonnet-4.5`
// arrives here with real slashes and needs no further decoding. Decoding again
// would corrupt a name containing a literal percent sign.
//
// The split is on the LAST colon: an alias may contain one, the method suffix
// is always final.
func ExtractModel(segment string) (model, method string) {
	model = strings.TrimPrefix(segment, "models/")
	if i := strings.LastIndexByte(model, ':'); i >= 0 {
		return model[:i], model[i+1:]
	}
	return model, ""
}

type wirePart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	InlineData       *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FileData *struct {
		MimeType string `json:"mimeType"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

type wireContent struct {
	Role  string     `json:"role"`
	Parts []wirePart `json:"parts"`
}

type wireRequest struct {
	Contents          []wireContent `json:"contents"`
	SystemInstruction *wireContent  `json:"systemInstruction"`
	Tools             []struct {
		FunctionDeclarations []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
	ToolConfig *struct {
		FunctionCallingConfig *struct {
			Mode                 string   `json:"mode"`
			AllowedFunctionNames []string `json:"allowedFunctionNames"`
		} `json:"functionCallingConfig"`
	} `json:"toolConfig"`
	SafetySettings []struct {
		Category  string `json:"category"`
		Threshold string `json:"threshold"`
	} `json:"safetySettings"`
	GenerationConfig *struct {
		Temperature      *float64        `json:"temperature"`
		TopP             *float64        `json:"topP"`
		TopK             *int            `json:"topK"`
		MaxOutputTokens  *int            `json:"maxOutputTokens"`
		StopSequences    []string        `json:"stopSequences"`
		ResponseMimeType string          `json:"responseMimeType"`
		ResponseSchema   json.RawMessage `json:"responseSchema"`
		ThinkingConfig   *struct {
			ThinkingBudget  int  `json:"thinkingBudget"`
			IncludeThoughts bool `json:"includeThoughts"`
		} `json:"thinkingConfig"`
	} `json:"generationConfig"`
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

	model, method := ExtractModel(r.PathValue("model"))
	req := &ir.Request{Model: model, Stream: method == "streamGenerateContent"}

	if w.SystemInstruction != nil {
		for _, p := range w.SystemInstruction.Parts {
			if p.Text != "" {
				req.System = append(req.System, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
			}
		}
	}

	// The ids of the previous model turn's calls, in order. A functionResponse
	// is matched to one of them by position, never by name: parallel calls to
	// one function are otherwise indistinguishable.
	var pending []string
	for turn, c := range w.Contents {
		role := ir.RoleUser
		if c.Role == "model" {
			role = ir.RoleAssistant
		}
		blocks, calls := parseParts(turn, c.Parts, pending)
		if len(calls) > 0 {
			pending = calls
		}
		req.Messages = append(req.Messages, ir.Message{Role: role, Content: blocks})
	}

	for _, t := range w.Tools {
		for _, d := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, ir.Tool{
				Name: d.Name, Description: d.Description, Schema: d.Parameters,
			})
		}
	}
	if w.ToolConfig != nil && w.ToolConfig.FunctionCallingConfig != nil {
		cfg := w.ToolConfig.FunctionCallingConfig
		switch cfg.Mode {
		case "NONE":
			req.ToolChoice = &ir.ToolChoice{Mode: "none"}
		case "ANY":
			// ANY plus exactly one allowed name is how Gemini spells "use this
			// tool", which the IR models as the distinct mode "tool".
			if len(cfg.AllowedFunctionNames) == 1 {
				req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: cfg.AllowedFunctionNames[0]}
			} else {
				req.ToolChoice = &ir.ToolChoice{Mode: "any"}
			}
		default:
			req.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		}
	}
	for _, s := range w.SafetySettings {
		req.Safety = append(req.Safety, ir.SafetySetting{Category: s.Category, Threshold: s.Threshold})
	}

	if g := w.GenerationConfig; g != nil {
		req.Temperature = g.Temperature
		req.TopP = g.TopP
		req.TopK = g.TopK
		req.MaxTokens = g.MaxOutputTokens
		req.StopSequences = g.StopSequences
		if len(g.ResponseSchema) > 0 {
			req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: g.ResponseSchema}
		}
		if g.ThinkingConfig != nil && g.ThinkingConfig.ThinkingBudget > 0 {
			req.Reasoning = &ir.Reasoning{Budget: g.ThinkingConfig.ThinkingBudget}
		}
	}

	// ModelField is empty: the Gemini model lives in the URL, which is why
	// Phase 9's passthrough rewrites the path rather than the body.
	return req, &edge.Passthrough{Body: body, ModelField: "", Surface: ir.SurfaceLLM}, nil
}

// parseParts converts one content entry, returning its blocks and the ids of
// any function calls it made.
func parseParts(turn int, parts []wirePart, pending []string) ([]ir.ContentBlock, []string) {
	var (
		out     []ir.ContentBlock
		calls   []string
		results int
	)
	for _, p := range parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = xlate.SyntheticToolCallID(turn, len(calls))
			}
			calls = append(calls, id)
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: id, Name: p.FunctionCall.Name, Input: args,
			}})

		case p.FunctionResponse != nil:
			id := p.FunctionResponse.ID
			if id == "" && results < len(pending) {
				id = pending[results]
			}
			results++
			out = append(out, ir.ContentBlock{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: id,
				Content: []ir.ContentBlock{{
					Type: ir.BlockText, Text: string(p.FunctionResponse.Response),
				}},
			}})

		case p.Thought:
			out = append(out, ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
				Text: p.Text, Signature: p.ThoughtSignature,
			}})

		case p.InlineData != nil:
			out = append(out, ir.ContentBlock{Type: mediaKind(p.InlineData.MimeType),
				Media: &ir.Media{MIME: p.InlineData.MimeType, Data: p.InlineData.Data}})

		case p.FileData != nil:
			out = append(out, ir.ContentBlock{Type: mediaKind(p.FileData.MimeType),
				Media: &ir.Media{MIME: p.FileData.MimeType, URL: p.FileData.FileURI}})

		case p.Text != "":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		}
	}
	return out, calls
}

// mediaKind picks the IR block type from a MIME type. Gemini has one part shape
// for every medium, so the type is the only signal, and the router's vision
// capability check reads it.
func mediaKind(mime string) ir.BlockType {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return ir.BlockImage
	case strings.HasPrefix(mime, "audio/"):
		return ir.BlockAudio
	default:
		return ir.BlockDocument
	}
}
