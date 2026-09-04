package xlate

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEffortBudgetUsesTheFixedTable(t *testing.T) {
	cases := []struct {
		effort string
		maxOut int
		want   int
	}{
		{"low", 0, 4096},
		{"medium", 0, 16384},
		{"high", 0, 32768},
		{"LOW", 0, 4096},
		{"", 0, 0},
		{"minimal", 0, 4096},
		{"high", 8192, 8192},
		{"low", 65536, 4096},
	}
	for _, tc := range cases {
		if got := EffortBudget(tc.effort, tc.maxOut); got != tc.want {
			t.Errorf("EffortBudget(%q, %d) = %d, want %d", tc.effort, tc.maxOut, got, tc.want)
		}
	}
}

func TestBudgetEffortIsTheInverseBanding(t *testing.T) {
	cases := []struct {
		budget int
		want   string
	}{
		{0, ""},
		{1024, "low"},
		{4096, "low"},
		{10239, "low"},
		{10240, "medium"},
		{16384, "medium"},
		{24575, "medium"},
		{24576, "high"},
		{100000, "high"},
	}
	for _, tc := range cases {
		if got := BudgetEffort(tc.budget); got != tc.want {
			t.Errorf("BudgetEffort(%d) = %q, want %q", tc.budget, got, tc.want)
		}
	}
}

func TestEffortBudgetRoundTrips(t *testing.T) {
	for _, e := range []string{"low", "medium", "high"} {
		if got := BudgetEffort(EffortBudget(e, 0)); got != e {
			t.Errorf("round trip of %q = %q", e, got)
		}
	}
}

func TestRequiredMaxTokensPassesAnExplicitCapThrough(t *testing.T) {
	n := 512
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 0)
	if got != 512 {
		t.Errorf("max_tokens = %d, want 512", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestRequiredMaxTokensSubstitutesAndWarns(t *testing.T) {
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic", 0)
	if got != DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", got, DefaultMaxTokens)
	}
	if len(warns) != 1 || warns[0].Field != "max_tokens" || warns[0].Target != "anthropic" {
		t.Fatalf("warnings = %+v", warns)
	}
}

func TestRequiredMaxTokensTreatsZeroAsAbsent(t *testing.T) {
	n := 0
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 0)
	if got != DefaultMaxTokens || len(warns) != 1 {
		t.Fatalf("max_tokens = %d, warnings = %+v", got, warns)
	}
}

func TestSyntheticToolCallIDIsStableAndPositional(t *testing.T) {
	if got := SyntheticToolCallID(2, 1); got != "call_2_1" {
		t.Errorf("SyntheticToolCallID(2, 1) = %q", got)
	}
	if SyntheticToolCallID(0, 0) == SyntheticToolCallID(0, 1) {
		t.Error("ids for two calls in one turn must differ")
	}
}

func TestRequiredMaxTokensUsesTheCatalogMaximum(t *testing.T) {
	// The carried-forward debt: 4096 was a constant because nothing knew the
	// model's real cap.
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic", 64_000)
	if got != 64_000 {
		t.Errorf("max tokens = %d, want the catalog's 64000", got)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if warns[0].Field != "max_tokens" {
		t.Errorf("warning field = %q", warns[0].Field)
	}
	// The substitution has to be visible, or a truncated answer looks like the
	// model stopping early.
	if !strings.Contains(warns[0].Reason, "64000") {
		t.Errorf("warning does not name the substituted value: %q", warns[0].Reason)
	}
}

func TestRequiredMaxTokensClampsAnImpossibleAsk(t *testing.T) {
	// Forwarding this is a 400 the client cannot diagnose. Clamping keeps a
	// servable request servable, and the warning is what makes the shorter
	// answer traceable to the substitution.
	n := 200_000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 64_000)
	if got != 64_000 {
		t.Errorf("max tokens = %d, want the clamp to 64000", got)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Reason, "64000") {
		t.Errorf("warning does not name the clamp: %q", warns[0].Reason)
	}
}

func TestRequiredMaxTokensDoesNotClampAgainstAnUnknownMaximum(t *testing.T) {
	n := 200_000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 0)
	if got != 200_000 || len(warns) != 0 {
		t.Errorf("got (%d, %v); an unknown maximum must not clamp", got, warns)
	}
}

func TestRequiredMaxTokensKeepsAServableClientValue(t *testing.T) {
	n := 1000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 64_000)
	if got != 1000 {
		t.Errorf("max tokens = %d, want the client's 1000", got)
	}
	if len(warns) != 0 {
		t.Errorf("warned about a request that needed no substitution: %v", warns)
	}
}

func TestEffortBudgetClampIsLive(t *testing.T) {
	// The parameter once had every caller passing 0, which disabled it.
	if got := EffortBudget("high", 8192); got != 8192 {
		t.Errorf("EffortBudget(high, 8192) = %d, want the clamp to 8192", got)
	}
	if got := EffortBudget("high", 0); got != 32768 {
		t.Errorf("EffortBudget(high, 0) = %d, want the unclamped 32768", got)
	}
	if got := EffortBudget("low", 65536); got != 4096 {
		t.Errorf("EffortBudget(low, 65536) = %d, want 4096", got)
	}
}

func TestEffortBudgetBandsTheOuterEfforts(t *testing.T) {
	// minimal has no band of its own on a budget-taking model, and neither
	// do xhigh and max: they collapse onto the nearest band rather than
	// being dropped, which would silently turn reasoning off.
	for effort, want := range map[string]int{"minimal": 4096, "xhigh": 32768, "max": 32768, "MAX": 32768} {
		if got := EffortBudget(effort, 0); got != want {
			t.Errorf("EffortBudget(%q) = %d, want %d", effort, got, want)
		}
	}
}

func TestAnthropicEffortMapsMinimalOntoLow(t *testing.T) {
	for in, want := range map[string]string{
		"minimal": "low", "low": "low", "medium": "medium", "high": "high",
		"xhigh": "xhigh", "max": "max", "High": "high", "": "",
	} {
		if got := AnthropicEffort(in); got != want {
			t.Errorf("AnthropicEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
