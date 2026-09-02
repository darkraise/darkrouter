package catalog

import (
	_ "embed"
	"testing"
)

//go:embed presets.overrides.yaml
var overridesYAML []byte

// TestOverrideTraitsForCurrentAnthropicFamilies pins the hand-reviewed rows
// the generator merges into presets.yaml. The three newer traits are what
// the Anthropic adapter reads to drop a prefill, skip the thinking off
// switch, and downgrade a forced tool choice; a row missing one of them is a
// 400 on every request of that shape against that family.
func TestOverrideTraitsForCurrentAnthropicFamilies(t *testing.T) {
	presets, err := parsePresets(overridesYAML)
	if err != nil {
		t.Fatal(err)
	}
	rules := presets["anthropic"].ModelTraits
	if len(rules) == 0 {
		t.Fatal("the anthropic override declares no model traits")
	}
	cases := []struct {
		model                                       string
		noPrefill, thinkingAlwaysOn, noForcedChoice bool
	}{
		{"claude-fable-5-1", true, true, true},
		{"claude-fable-5", true, true, true},
		{"claude-mythos-5-1", true, true, true},
		{"claude-mythos-5", true, true, true},
		{"claude-opus-5", true, false, false},
		{"claude-sonnet-5", true, false, false},
		{"claude-opus-4-8", true, false, false},
		{"claude-opus-4-7", true, false, false},
		{"claude-opus-4-6", true, false, false},
		{"claude-sonnet-4-6", true, false, false},
		{"claude-opus-4-5", false, false, false},
		{"claude-sonnet-4-5", false, false, false},
		{"claude-haiku-4-5", false, false, false},
	}
	for _, c := range cases {
		got := traitsFor(Preset{ModelTraits: rules}, c.model)
		if !got.Known {
			t.Errorf("%s: no rule matches", c.model)
			continue
		}
		rule := longestRule(rules, c.model)
		if rule.NoPrefill != c.noPrefill || rule.ThinkingAlwaysOn != c.thinkingAlwaysOn ||
			rule.NoForcedToolChoice != c.noForcedChoice {
			t.Errorf("%s: rule = %+v, want no_prefill=%v thinking_always_on=%v no_forced_tool_choice=%v",
				c.model, rule, c.noPrefill, c.thinkingAlwaysOn, c.noForcedChoice)
		}
	}
}

// longestRule mirrors traitsFor's selection so the test reads the rule
// itself, including fields Traits does not yet carry.
func longestRule(rules []TraitRule, model string) TraitRule {
	var best TraitRule
	for _, r := range rules {
		if len(r.Match) > len(best.Match) && containsRule(model, r.Match) {
			best = r
		}
	}
	return best
}

func containsRule(model, match string) bool {
	return traitsFor(Preset{ModelTraits: []TraitRule{{Match: match}}}, model).Known
}
