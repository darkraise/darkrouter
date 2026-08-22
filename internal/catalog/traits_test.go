package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// TestShippedAnthropicTraits closes the loop between the preset data and the
// wire shape the adapter builds from it.
//
// internal/adapter/anthropic's tests now state their traits explicitly, which
// is correct — the adapter's job is to honor them, not to derive them. This is
// where the derivation itself is checked, against the real model names phase 4
// verified live on 2026-08-22:
//
//   - thinking:{type:"enabled", budget_tokens} is a 400 on Claude 4.7 and later
//   - thinking:{type:"adaptive"} is a 400 on Claude 4.5 and earlier
//   - the newest generation rejects any non-default sampling value outright
//
// Getting one of these wrong is a 400 on every reasoning request against that
// generation, and nothing else in the suite would catch it.
func TestShippedAnthropicTraits(t *testing.T) {
	cases := []struct {
		model                                string
		adaptive, manualBudget, freeSampling bool
	}{
		// The sealed generation: adaptive only, no sampling at all.
		{"claude-opus-4-7", true, false, false},
		{"claude-opus-4-8", true, false, false},
		{"claude-opus-5", true, false, false},
		{"claude-sonnet-5", true, false, false},
		{"claude-fable-5", true, false, false},
		// Both shapes, sampling allowed.
		{"claude-opus-4-6", true, true, true},
		{"claude-sonnet-4-6", true, true, true},
		// Budget only: adaptive is a 400 here, which models.dev cannot express.
		{"claude-opus-4-5", false, true, true},
		{"claude-opus-4-5-20251101", false, true, true},
		{"claude-sonnet-4-5", false, true, true},
		{"claude-sonnet-4-5-20250929", false, true, true},
		{"claude-haiku-4-5", false, true, true},
		{"claude-3-5-sonnet-20241022", false, true, true},
		// Proxies spell the same model with dots.
		{"claude-sonnet-4.5", false, true, true},
	}

	presets, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := Merge(MergeInput{
			Providers: []provider.Provider{{ID: "p", Kind: "anthropic", Preset: "anthropic"}},
			Presets:   presets,
			Rows:      []store.ModelRow{{ProviderID: "p", ModelID: c.model, State: "live"}},
		})
		if len(got) != 1 {
			t.Fatalf("%s: merged to %d models", c.model, len(got))
		}
		tr := got[0].Traits
		if !tr.Known {
			t.Errorf("%s: traits unknown; the shipped preset has no rule for it", c.model)
			continue
		}
		if tr.Adaptive != c.adaptive || tr.ManualBudget != c.manualBudget || tr.FreeSampling != c.freeSampling {
			t.Errorf("%s: traits = %+v, want adaptive=%v manual=%v sampling=%v",
				c.model, tr, c.adaptive, c.manualBudget, c.freeSampling)
		}
	}
}

func TestUnknownAnthropicModelHasNoTraits(t *testing.T) {
	// A proxied model whose name says nothing about its generation is the case
	// the deleted name heuristic got wrong. Unknown is the honest answer: the
	// adapter then shapes the request the way the client asked and warns.
	presets, _ := LoadPresets()
	got := Merge(MergeInput{
		Providers: []provider.Provider{{ID: "p", Kind: "anthropic", Preset: "anthropic"}},
		Presets:   presets,
		Rows:      []store.ModelRow{{ProviderID: "p", ModelID: "default", State: "live"}},
	})
	if got[0].Traits.Known {
		t.Errorf("traits invented for an unrecognized name: %+v", got[0].Traits)
	}
}
