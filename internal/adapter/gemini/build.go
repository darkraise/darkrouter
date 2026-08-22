package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (f *Fetcher) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	var warns []ir.Warning
	body := map[string]any{}

	contents, w := f.renderContents(ctx, req)
	warns = append(warns, w...)
	body["contents"] = contents

	sys, w := xlate.CollectSystem(req, targetName)
	warns = append(warns, w...)
	// systemInstruction is prose, so a cache marker on a system block has
	// nowhere to go. A cached system prompt is the most valuable thing a client
	// caches, and losing it silently is the failure spec §5 exists to prevent.
	for _, b := range req.System {
		if b.CacheControl != nil {
			warns = append(warns, ir.Warning{
				Field: "system[].cache_control", Target: targetName,
				Reason: "Gemini caches explicitly through cachedContent, not per block",
			})
		}
	}
	if sys != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": sys}}}
	}

	if len(req.Tools) > 0 {
		decls := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := tool.Schema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			decls = append(decls, map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": schema,
			})
		}
		// One entry holding every declaration. Separate entries are reserved
		// for built-in tools, and splitting them here disables function calling
		// without an error.
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	if cfg := functionCallingConfig(req.ToolChoice); cfg != nil {
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
	}
	if req.ParallelToolCalls != nil {
		warns = append(warns, ir.Warning{
			Field: "parallel_tool_calls", Target: targetName, Reason: "no equivalent setting",
		})
	}
	if len(req.Metadata) > 0 {
		for k := range req.Metadata {
			if strings.HasPrefix(k, "anthropic_") {
				continue
			}
			warns = append(warns, ir.Warning{
				Field: "metadata", Target: targetName, Reason: "no request metadata field",
			})
			break
		}
	}
	if len(req.Safety) > 0 {
		settings := make([]any, 0, len(req.Safety))
		for _, s := range req.Safety {
			settings = append(settings, map[string]any{
				"category": s.Category, "threshold": s.Threshold,
			})
		}
		body["safetySettings"] = settings
	}

	cfg := map[string]any{}
	if req.Temperature != nil {
		cfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		cfg["topP"] = *req.TopP
	}
	if req.TopK != nil {
		cfg["topK"] = *req.TopK
	}
	if req.MaxTokens != nil {
		cfg["maxOutputTokens"] = *req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		cfg["stopSequences"] = req.StopSequences
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		// A responseSchema without the MIME type is ignored outright.
		cfg["responseMimeType"] = "application/json"
		cfg["responseSchema"] = req.ResponseFormat.Schema
	}
	if req.Reasoning != nil {
		budget := req.Reasoning.Budget
		if budget == 0 {
			budget = xlate.EffortBudget(req.Reasoning.Effort, 0)
		}
		if budget > 0 {
			cfg["thinkingConfig"] = map[string]any{
				"thinkingBudget": budget, "includeThoughts": true,
			}
		}
	}
	if len(cfg) > 0 {
		body["generationConfig"] = cfg
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	method := ":generateContent"
	if req.Stream {
		method = ":streamGenerateContent"
	}
	// url.PathEscape on the model keeps a provider/model name from opening
	// extra path segments the API would not match.
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" + url.PathEscape(t.Model) + method
	if req.Stream {
		endpoint += "?alt=sse"
	}

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		// The header rather than ?key=: a query parameter lands in access logs
		// and proxy traces.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, warns, nil
}

// functionCallingConfig maps the IR's tool choice. Forcing one tool is ANY plus
// an allow list, since Gemini has no single-tool mode.
func functionCallingConfig(tc *ir.ToolChoice) map[string]any {
	if tc == nil {
		return nil
	}
	switch tc.Mode {
	case "none":
		return map[string]any{"mode": "NONE"}
	case "any":
		return map[string]any{"mode": "ANY"}
	case "tool":
		return map[string]any{"mode": "ANY", "allowedFunctionNames": []string{tc.Name}}
	default:
		return map[string]any{"mode": "AUTO"}
	}
}
