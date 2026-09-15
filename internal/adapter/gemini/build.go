package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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

	contents, w, err := f.renderContents(ctx, req)
	warns = append(warns, w...)
	if err != nil {
		return nil, warns, err
	}
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
			// responseSchema takes Gemini's OpenAPI subset, which rejects
			// $defs, $ref and additionalProperties; responseJsonSchema takes
			// JSON Schema, whose type names are lowercase. Each schema goes to
			// the field its own form belongs in. Either is ignored outright
			// without the MIME type.
			cfg["responseMimeType"] = "application/json"
			cfg[schemaField(rf.SchemaDialect, "responseSchema", "responseJsonSchema")] = rf.Schema
		case "json_object":
			cfg["responseMimeType"] = "application/json"
		}
	}
	tc, tw := thinkingConfig(t.Model, req.Reasoning)
	warns = append(warns, tw...)
	if tc != nil {
		cfg["thinkingConfig"] = tc
	}
	if len(cfg) > 0 {
		body["generationConfig"] = cfg
	}
	return body, warns, nil
}

func schemaField(dialect, openAPIField, jsonSchemaField string) string {
	if dialect == ir.SchemaOpenAPI {
		return openAPIField
	}
	return jsonSchemaField
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
		if _, typed := tool.Extra["type"]; typed {
			// A typed tool is another provider's server-side capability;
			// declaring it as a function would invite calls nobody answers.
			warns = append(warns, ir.Warning{
				Field: "tools[].type", Target: targetName,
				Reason: "typed server tool has no equivalent; the tool was dropped",
			})
			continue
		}
		for k := range tool.Extra {
			warns = append(warns, ir.Warning{
				Field: "tools[]." + k, Target: targetName,
				Reason: "no equivalent on a function declaration; the field was dropped",
			})
		}
		schema, dialect := tool.Schema, tool.SchemaDialect
		if len(schema) == 0 {
			schema, dialect = json.RawMessage(`{"type":"object"}`), ""
		}
		// parameters or parametersJsonSchema by the schema's form, for the
		// same reason as responseSchema and responseJsonSchema.
		decls = append(decls, map[string]any{
			"name": tool.Name, "description": tool.Description,
			schemaField(dialect, "parameters", "parametersJsonSchema"): schema,
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

// Budget floors, also per family and also rejected rather than clamped. Pro
// has no off switch at all: its floor is the closest it comes to off. An
// unrecognized id gets no floor, so a client's own budget goes through. Only
// a thinking generation reaches this: 1.x and 2.0 Pro take no budget at all.
const (
	budgetFloorPro       = 128
	budgetFloorFlashLite = 512
)

func budgetFloor(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "flash-lite"):
		return budgetFloorFlashLite
	case isPro(m):
		return budgetFloorPro
	}
	return 0
}

// isPro reports a model that cannot turn thinking off: 2.5 Pro takes no zero
// budget.
func isPro(model string) bool {
	return strings.Contains(strings.ToLower(model), "-pro")
}

// isGemini3 reports whether the model takes thinkingLevel rather than a
// token budget. Gemini 3 ignores thinkingBudget on some variants and rejects
// it on others; the level is the control the generation documents.
func isGemini3(model string) bool {
	return strings.Contains(strings.ToLower(model), "gemini-3")
}

// nonThinking reports a generation from before thinking existed. It takes no
// thinkingConfig, so a request to turn thinking off is already honored.
func nonThinking(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "gemini-1.") || strings.Contains(m, "gemini-2.0-")
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

var levelOrder = []string{"minimal", "low", "medium", "high"}

var gemini3Model = regexp.MustCompile(`gemini-(3(?:\.\d+)?)-([a-z-]+)`)

// gemini3Levels is the set of thinking levels a Gemini 3 model accepts, in
// levelOrder, per Google's thinking guides for the Gemini API and Vertex.
// Every other level is a 400.
//
// minimal is granted only to the models documented with it: the two newest
// Flash models dropped it, so an unrecognized one is assumed not to take it.
// low, medium and high are what every other text model takes, except 3 Pro,
// which has no medium.
func gemini3Levels(model string) []string {
	all := levelOrder
	noMinimal := levelOrder[1:]
	match := gemini3Model.FindStringSubmatch(strings.ToLower(model))
	if match == nil {
		return noMinimal
	}
	version, variant := match[1], match[2]
	switch {
	case strings.Contains(variant, "image") && strings.HasPrefix(variant, "pro"):
		return []string{"high"}
	case strings.Contains(variant, "image"):
		return []string{"minimal", "high"}
	case strings.HasPrefix(variant, "pro"):
		if version == "3" {
			return []string{"low", "high"}
		}
		return noMinimal
	case strings.HasPrefix(variant, "flash-lite"):
		if version == "3.1" || version == "3.5" {
			return all
		}
	case strings.HasPrefix(variant, "flash"):
		if version == "3" || version == "3.5" || version == "3.6" {
			return all
		}
	}
	return noMinimal
}

// nearestLevel picks the supported level closest to the one asked for,
// rounding up on a tie: too much thinking costs tokens, too little silently
// degrades the answer.
func nearestLevel(level string, supported []string) string {
	want := slices.Index(levelOrder, level)
	best, bestDist := supported[0], len(levelOrder)
	for _, s := range supported {
		d := slices.Index(levelOrder, s) - want
		if d < 0 {
			d = -d
		}
		if d <= bestDist {
			best, bestDist = s, d
		}
	}
	return best
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
		cannotDisable := []ir.Warning{{
			Field: "reasoning", Target: targetName,
			Reason: "this model cannot turn thinking off; sent with the least thinking it accepts",
		}}
		switch {
		// No Gemini 3 model turns thinking fully off, and a zero budget is not
		// its control; its lowest level is the documented nearest thing.
		case isGemini3(model):
			return map[string]any{"thinkingLevel": gemini3Levels(model)[0]}, cannotDisable
		case nonThinking(model):
			return nil, nil
		case isPro(model):
			return map[string]any{"thinkingBudget": budgetFloorPro}, cannotDisable
		}
		return map[string]any{"thinkingBudget": 0}, nil
	}
	if nonThinking(model) {
		return nil, []ir.Warning{{
			Field: "reasoning", Target: targetName,
			Reason: "this model has no thinking; reasoning dropped",
		}}
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
			if sent := nearestLevel(level, gemini3Levels(model)); sent != level {
				warns = append(warns, ir.Warning{
					Field: "reasoning.effort", Target: targetName,
					Reason: "this model has no " + level + " thinking level; sent " + sent,
				})
				level = sent
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
	if floor := budgetFloor(model); budget < floor {
		warns = append(warns, ir.Warning{
			Field: "reasoning.budget", Target: targetName,
			Reason: "below the model family's thinking floor; raised to " + strconv.Itoa(floor),
		})
		budget = floor
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
