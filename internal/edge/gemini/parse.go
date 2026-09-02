// Package gemini implements the Google Gemini inbound dialect.
package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	// Tools is read as raw objects because an entry is either a set of
	// function declarations or one provider built-in — googleSearch,
	// codeExecution, urlContext — keyed by its own name.
	Tools      []map[string]json.RawMessage `json:"tools"`
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
		Temperature        *float64        `json:"temperature"`
		TopP               *float64        `json:"topP"`
		TopK               *int            `json:"topK"`
		MaxOutputTokens    *int            `json:"maxOutputTokens"`
		StopSequences      []string        `json:"stopSequences"`
		ResponseMimeType   string          `json:"responseMimeType"`
		ResponseSchema     json.RawMessage `json:"responseSchema"`
		ResponseJSONSchema json.RawMessage `json:"responseJsonSchema"`
		ThinkingConfig     *struct {
			// ThinkingBudget is a pointer because zero is an explicit
			// request to turn thinking off, and absent means unset.
			ThinkingBudget  *int   `json:"thinkingBudget"`
			ThinkingLevel   string `json:"thinkingLevel"`
			IncludeThoughts bool   `json:"includeThoughts"`
		} `json:"thinkingConfig"`
	} `json:"generationConfig"`
	CachedContent string `json:"cachedContent"`
}

type wireFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
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

	for _, entry := range w.Tools {
		tools, err := parseToolEntry(entry)
		if err != nil {
			return nil, nil, err
		}
		req.Tools = append(req.Tools, tools...)
	}
	if w.CachedContent != "" {
		// Metadata is the one IR slot a provider-specific handle fits in.
		// Only the Gemini adapter reads this key back; every other target
		// warns about metadata it cannot forward.
		req.Metadata = map[string]string{"gemini_cached_content": w.CachedContent}
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
		switch {
		case len(g.ResponseJSONSchema) > 0:
			req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: g.ResponseJSONSchema}
		case len(g.ResponseSchema) > 0:
			req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: g.ResponseSchema}
		case g.ResponseMimeType == "application/json":
			req.ResponseFormat = &ir.ResponseFormat{Type: "json_object"}
		}
		if tc := g.ThinkingConfig; tc != nil {
			switch {
			case tc.ThinkingBudget != nil && *tc.ThinkingBudget == 0:
				req.Reasoning = &ir.Reasoning{Disabled: true}
			case tc.ThinkingBudget != nil && *tc.ThinkingBudget > 0:
				req.Reasoning = &ir.Reasoning{Budget: *tc.ThinkingBudget}
			case tc.ThinkingLevel != "":
				req.Reasoning = &ir.Reasoning{Effort: strings.ToLower(tc.ThinkingLevel)}
			}
		}
	}

	// ModelField is empty: the Gemini model lives in the URL, which is why
	// phase 9's passthrough rewrites the path rather than the body. The
	// credential parameter is dropped here rather than overridden upstream —
	// replaying it would send Darkrouter's own proxy token to Google.
	q := r.URL.Query()
	q.Del("key")
	return req, &edge.Passthrough{
		Body: body, ModelField: "", Surface: ir.SurfaceLLM,
		Method: method, Query: q, Stream: req.Stream,
	}, nil
}

// parseToolEntry reads one tools entry: its function declarations become
// named tools, and any other key is a provider built-in carried whole.
func parseToolEntry(entry map[string]json.RawMessage) ([]ir.Tool, error) {
	var out []ir.Tool
	keys := make([]string, 0, len(entry))
	for k := range entry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		raw := entry[k]
		if k == "functionDeclarations" || k == "function_declarations" {
			var decls []wireFunctionDeclaration
			if err := json.Unmarshal(raw, &decls); err != nil {
				return nil, fmt.Errorf("invalid functionDeclarations: %w", err)
			}
			for _, d := range decls {
				out = append(out, ir.Tool{Name: d.Name, Description: d.Description, Schema: d.Parameters})
			}
			continue
		}
		if string(raw) == "null" {
			continue
		}
		out = append(out, ir.Tool{Extra: map[string]json.RawMessage{k: raw}})
	}
	return out, nil
}

// parseParts converts one content entry, returning its blocks and the ids of
// any function calls it made.
func parseParts(turn int, parts []wirePart, pending []string) ([]ir.ContentBlock, []string) {
	var (
		out   []ir.ContentBlock
		calls []string
	)
	// Responses naming their call claim it up front, so a positional
	// response in the same turn answers the next call nobody named.
	named := map[string]bool{}
	for _, p := range parts {
		if p.FunctionResponse != nil && p.FunctionResponse.ID != "" {
			named[p.FunctionResponse.ID] = true
		}
	}
	next := 0
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
				ID: id, Name: p.FunctionCall.Name, Input: args, Signature: p.ThoughtSignature,
			}})

		case p.FunctionResponse != nil:
			id := p.FunctionResponse.ID
			if id == "" {
				for next < len(pending) && named[pending[next]] {
					next++
				}
				if next < len(pending) {
					id = pending[next]
				}
				next++
			}
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

		case p.Text != "" || p.ThoughtSignature != "":
			blk := ir.ContentBlock{Type: ir.BlockText, Text: p.Text}
			if p.ThoughtSignature != "" {
				blk.SetExtraString(ir.ExtraThoughtSignature, p.ThoughtSignature)
			}
			out = append(out, blk)
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
