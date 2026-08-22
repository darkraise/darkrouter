package xlate

import (
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// DefaultMaxTokens is the cap substituted when a target requires one and
// neither the request nor the catalog supplies it. Phase 6 replaces the
// catalog half; until then every substitution carries a warning.
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
func EffortBudget(effort string, maxOut int) int {
	var b int
	switch strings.ToLower(effort) {
	case "low":
		b = budgetLow
	case "medium":
		b = budgetMedium
	case "high":
		b = budgetHigh
	default:
		return 0
	}
	if maxOut > 0 && b > maxOut {
		return maxOut
	}
	return b
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

// RequiredMaxTokens supplies the cap a target demands. A request that carries
// none gets DefaultMaxTokens and a warning, so a truncated answer is traceable
// to the substitution rather than looking like the model stopping early.
func RequiredMaxTokens(req *ir.Request, target string) (int, []ir.Warning) {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens, nil
	}
	return DefaultMaxTokens, []ir.Warning{{
		Field:  "max_tokens",
		Target: target,
		Reason: "required by the target and absent from the request; substituted " +
			strconv.Itoa(DefaultMaxTokens),
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
