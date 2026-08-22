package xlate

import (
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
		{"minimal", 0, 0},
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
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic")
	if got != 512 {
		t.Errorf("max_tokens = %d, want 512", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestRequiredMaxTokensSubstitutesAndWarns(t *testing.T) {
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic")
	if got != DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", got, DefaultMaxTokens)
	}
	if len(warns) != 1 || warns[0].Field != "max_tokens" || warns[0].Target != "anthropic" {
		t.Fatalf("warnings = %+v", warns)
	}
}

func TestRequiredMaxTokensTreatsZeroAsAbsent(t *testing.T) {
	n := 0
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic")
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
