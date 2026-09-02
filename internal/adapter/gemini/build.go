package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// MetadataCachedContent is the Metadata key the Gemini edge parks a request's
// cachedContent handle under. Only this adapter reads it back; every other
// target warns about metadata it cannot forward, as it should, because a
// cache handle is meaningless anywhere else.
const MetadataCachedContent = "gemini_cached_content"

func (f *Fetcher) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	body, warns, err := f.renderBody(ctx, t, req)
	if err != nil {
		return nil, warns, err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	method := ":generateContent"
	if req.Stream {
		method = ":streamGenerateContent"
	}
	endpoint := modelEndpoint(t, method)
	if req.Stream {
		endpoint += "?alt=sse"
	}
	hr, err := newRequest(ctx, t, endpoint, buf)
	if err != nil {
		return nil, warns, err
	}
	return hr, warns, nil
}

// modelEndpoint joins the base URL, the model and the operation.
// url.PathEscape on the model keeps a provider/model name from opening extra
// path segments the API would not match.
func modelEndpoint(t *adapter.Target, method string) string {
	return strings.TrimRight(t.BaseURL, "/") + "/models/" + url.PathEscape(t.Model) + method
}

func newRequest(ctx context.Context, t *adapter.Target, endpoint string, body []byte) (*http.Request, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		// The header rather than ?key=: a query parameter lands in access logs
		// and proxy traces.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, nil
}

// renderBody produces the generateContent body. countTokens wraps the same
// body rather than building its own, so tools and the system instruction are
// counted exactly as the model will see them.
func (f *Fetcher) renderBody(ctx context.Context, t *adapter.Target, req *ir.Request) (map[string]any, []ir.Warning, error) {
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

	if tools, w := renderTools(req.Tools); len(tools) > 0 || len(w) > 0 {
		warns = append(warns, w...)
		if len(tools) > 0 {
			body["tools"] = tools
		}
	}
	if cfg := functionCallingConfig(req.ToolChoice); cfg != nil {
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
	}
	if req.ParallelToolCalls != nil {
		warns = append(warns, ir.Warning{
			Field: "parallel_tool_calls", Target: targetName, Reason: "no equivalent setting",
		})
	}
	for k, v := range req.Metadata {
		if k == MetadataCachedContent {
			body["cachedContent"] = v
			continue
		}
		if strings.HasPrefix(k, "anthropic_") {
			continue
		}
		warns = append(warns, ir.Warning{
			Field: "metadata", Target: targetName, Reason: "no request metadata field",
		})
		break
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
	if rf := req.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_schema":
			// A responseSchema without the MIME type is ignored outright.
			cfg["responseMimeType"] = "application/json"
			cfg["responseSchema"] = rf.Schema
		case "json_object":
			cfg["responseMimeType"] = "application/json"
		}
	}
	if tc, w := thinkingConfig(t.Model, req.Reasoning); tc != nil {
		warns = append(warns, w...)
		cfg["thinkingConfig"] = tc
	}
	if len(cfg) > 0 {
		body["generationConfig"] = cfg
	}
	return body, warns, nil
}

// renderTools declares the client's functions in one tools entry and each
// provider built-in tool in its own, which is the only arrangement Gemini
// accepts: every declaration in one entry, and one entry per built-in.
func renderTools(tools []ir.Tool) ([]any, []ir.Warning) {
	var (
		decls []any
		out   []any
		warns []ir.Warning
	)
	for _, tool := range tools {
		if tool.BuiltIn() {
			keys := make([]string, 0, len(tool.Extra))
			for k := range tool.Extra {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out = append(out, map[string]any{k: tool.Extra[k]})
			}
			continue
		}
		for k := range tool.Extra {
			warns = append(warns, ir.Warning{
				Field: "tools[]." + k, Target: targetName,
				Reason: "no equivalent on a function declaration; the field was dropped",
			})
		}
		schema := tool.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		decls = append(decls, map[string]any{
			"name": tool.Name, "description": tool.Description, "parameters": schema,
		})
	}
	if len(decls) > 0 {
		// Separate entries per declaration disable function calling without
		// an error, so every declaration shares one.
		out = append([]any{map[string]any{"functionDeclarations": decls}}, out...)
	}
	return out, warns
}

// Budget ceilings per model family. Gemini rejects a thinkingBudget above the
// family's ceiling rather than clamping it, so a client's Anthropic-sized
// budget has to be clamped here. The pro ceiling doubles as the default for
// an unrecognized id: too high fails loudly, too low silently under-thinks.
const (
	budgetCapFlash = 24576
	budgetCapPro   = 32768
)

func budgetCap(model string) int {
	if strings.Contains(strings.ToLower(model), "flash") {
		return budgetCapFlash
	}
	return budgetCapPro
}

// isGemini3 reports whether the model takes thinkingLevel rather than a
// token budget. Gemini 3 ignores thinkingBudget on some variants and rejects
// it on others; the level is the control the generation documents.
func isGemini3(model string) bool {
	return strings.Contains(strings.ToLower(model), "gemini-3")
}

// thinkingLevel maps the IR effort vocabulary onto Gemini 3's levels.
func thinkingLevel(effort string) string {
	switch strings.ToLower(effort) {
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	}
	return ""
}

// effortBudget extends xlate.EffortBudget to the ends of the vocabulary it
// does not cover: minimal is the smallest budget Gemini accepts for thinking
// that still happens, and the two top efforts are the family ceiling.
func effortBudget(effort string, cap int) int {
	switch strings.ToLower(effort) {
	case "minimal":
		return 1024
	case "xhigh", "max":
		return cap
	}
	return xlate.EffortBudget(effort, cap)
}

func thinkingConfig(model string, r *ir.Reasoning) (map[string]any, []ir.Warning) {
	if r == nil {
		return nil, nil
	}
	if r.Disabled {
		return map[string]any{"thinkingBudget": 0}, nil
	}
	cap := budgetCap(model)
	if isGemini3(model) {
		effort := r.Effort
		if effort == "" && r.Budget > 0 {
			effort = xlate.BudgetEffort(r.Budget)
		}
		if level := thinkingLevel(effort); level != "" {
			warns := []ir.Warning(nil)
			if r.Effort == "" {
				warns = append(warns, ir.Warning{
					Field: "reasoning.budget", Target: targetName,
					Reason: "Gemini 3 takes a thinking level, not a budget; converted to the nearest level",
				})
			}
			return map[string]any{"thinkingLevel": level, "includeThoughts": true}, warns
		}
		return nil, nil
	}
	budget := r.Budget
	if budget == 0 {
		budget = effortBudget(r.Effort, cap)
	}
	if budget <= 0 {
		return nil, nil
	}
	var warns []ir.Warning
	if budget > cap {
		warns = append(warns, ir.Warning{
			Field: "reasoning.budget", Target: targetName,
			Reason: "above the model family's thinking ceiling; clamped to " + strconv.Itoa(cap),
		})
		budget = cap
	}
	return map[string]any{"thinkingBudget": budget, "includeThoughts": true}, warns
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
