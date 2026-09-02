package xlate

import (
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// DefaultMaxTokens is the cap substituted when a target requires one and
// neither the request nor the catalog supplies it. Every substitution carries a
// warning, so a truncated answer is traceable to it rather than looking like
// the model stopping early.
const DefaultMaxTokens = 4096

// MaxCacheBreakpoints is Anthropic's hard limit. A fifth marker is a 400 the
// client cannot diagnose, because the error does not say which one was surplus.
const MaxCacheBreakpoints = 4

// Effort bands, fixed by spec §4.6 so the same request reasons identically
// against every target.
const (
	budgetLow    = 4096
	budgetMedium = 16384
	budgetHigh   = 32768
)

// EffortBudget converts a reasoning effort to a token budget, clamped to the
// model's maximum output tokens. A maxOut of 0 means the catalog does not know
// it, which disables the clamp rather than clamping to nothing.
//
// The outer efforts collapse onto the nearest band: a budget-taking model has
// no depth below low or above high, and returning 0 for them would turn
// reasoning off for a client that asked for the most of it.
func EffortBudget(effort string, maxOut int) int {
	var b int
	switch strings.ToLower(effort) {
	case "minimal", "low":
		b = budgetLow
	case "medium":
		b = budgetMedium
	case "high", "xhigh", "max":
		b = budgetHigh
	default:
		return 0
	}
	if maxOut > 0 && b > maxOut {
		return maxOut
	}
	return b
}

// AnthropicEffort maps the IR vocabulary onto output_config.effort. Anthropic
// has no minimal; low is its floor. The rest is Anthropic's own vocabulary and
// passes through lowercased.
func AnthropicEffort(effort string) string {
	e := strings.ToLower(effort)
	if e == "minimal" {
		return "low"
	}
	return e
}

// GeminiMinBudget is the smallest thinkingBudget every Gemini 2.5 model
// accepts as "think, but barely": Flash allows 0, Pro 128, Flash-Lite 512,
// and 0 means off rather than minimal.
const GeminiMinBudget = 512

// GeminiBudgetCap is the largest thinkingBudget a Gemini model accepts. The
// Flash line caps lower than Pro; an unrecognized name gets Pro's cap, which
// is also Gemini's documented default ceiling.
func GeminiBudgetCap(model string) int {
	if strings.Contains(strings.ToLower(model), "flash") {
		return 24576
	}
	return 32768
}

// GeminiEffortBudget bands an effort onto a thinkingBudget for one model:
// minimal is the floor, xhigh and max are the model's cap, and the three
// middle bands come from the shared table clamped to that cap.
func GeminiEffortBudget(effort, model string) int {
	cap := GeminiBudgetCap(model)
	switch strings.ToLower(effort) {
	case "minimal":
		return GeminiMinBudget
	case "xhigh", "max":
		return cap
	default:
		return EffortBudget(effort, cap)
	}
}

// BudgetEffort is the inverse banding, for targets that take an effort rather
// than a budget. The boundaries sit midway between the table's values so a
// budget written by EffortBudget maps back to the effort it came from.
func BudgetEffort(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget < (budgetLow+budgetMedium)/2:
		return "low"
	case budget < (budgetMedium+budgetHigh)/2:
		return "medium"
	default:
		return "high"
	}
}

// RequiredMaxTokens supplies the cap a target demands.
//
// catalogMax is the model's real maximum output, or 0 when the catalog does not
// know it. Three cases, each warned about when it changes what the client sent:
//
//   - No cap in the request: the catalog's maximum, or DefaultMaxTokens.
//   - A cap above the model's maximum: clamped. Forwarding it is a 400 the
//     client cannot diagnose, and keeping a servable request servable is the
//     same choice phase 4 made for a thinking budget that exceeded max_tokens.
//   - A cap the model can honor: passed through untouched and unwarned.
func RequiredMaxTokens(req *ir.Request, target string, catalogMax int) (int, []ir.Warning) {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		asked := *req.MaxTokens
		if catalogMax > 0 && asked > catalogMax {
			return catalogMax, []ir.Warning{{
				Field:  "max_tokens",
				Target: target,
				Reason: "above the model's maximum output; clamped to " + strconv.Itoa(catalogMax),
			}}
		}
		return asked, nil
	}
	if catalogMax > 0 {
		return catalogMax, []ir.Warning{{
			Field:  "max_tokens",
			Target: target,
			Reason: "required by the target and absent from the request; substituted " +
				"the model's maximum, " + strconv.Itoa(catalogMax),
		}}
	}
	return DefaultMaxTokens, []ir.Warning{{
		Field:  "max_tokens",
		Target: target,
		Reason: "required by the target and absent from the request, and the catalog " +
			"does not know the model's maximum; substituted " + strconv.Itoa(DefaultMaxTokens),
	}}
}

// SyntheticToolCallID names a tool call the upstream left unidentified.
//
// Gemini's functionCall and functionResponse ids are optional and most models
// omit them, so correlation is positional within the turn. Deriving the id from
// the same two positions keeps it stable across a re-render, which is what makes
// a retried attempt produce the same conversation.
func SyntheticToolCallID(turn, call int) string {
	return "call_" + strconv.Itoa(turn) + "_" + strconv.Itoa(call)
}
