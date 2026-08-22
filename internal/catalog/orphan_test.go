package catalog

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

func TestOrphanedPresetsNamesTheReference(t *testing.T) {
	got := OrphanedPresets([]provider.Provider{
		{ID: "p1", Preset: "groq", Kind: "openaicompat", BaseURL: "https://a"},
		{ID: "p2", Preset: "retired-vendor", Kind: "openaicompat", BaseURL: "https://b"},
	}, Presets{"groq": {Name: "Groq"}})

	if len(got) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(got), got)
	}
	// The operator has to be able to find both ends of the break.
	if !strings.Contains(got[0], "p2") || !strings.Contains(got[0], "retired-vendor") {
		t.Errorf("warning names neither the provider nor the preset: %q", got[0])
	}
	// And to know it is still serving.
	if !strings.Contains(got[0], "openaicompat") || !strings.Contains(got[0], "https://b") {
		t.Errorf("warning does not say what it degraded to: %q", got[0])
	}
}

func TestUncataloguedProvidersAreNotOrphans(t *testing.T) {
	// An uncatalogued provider is a base URL and a key with no preset at all.
	// Warning about it every startup would be noise on a supported setup.
	got := OrphanedPresets([]provider.Provider{
		{ID: "p", Preset: "", Kind: "openaicompat", BaseURL: "https://x"},
	}, Presets{})
	if len(got) != 0 {
		t.Errorf("warned about a provider with no preset: %v", got)
	}
}

func TestOrphanWarningsAreDeterministic(t *testing.T) {
	// Two startups on the same database must produce the same /healthz text,
	// or the warnings look like they are changing when nothing is.
	ps := []provider.Provider{
		{ID: "b", Preset: "gone", Kind: "openaicompat", BaseURL: "https://b"},
		{ID: "a", Preset: "gone", Kind: "openaicompat", BaseURL: "https://a"},
	}
	first := OrphanedPresets(ps, Presets{})
	second := OrphanedPresets(ps, Presets{})
	if len(first) != 2 {
		t.Fatalf("got %d warnings, want 2", len(first))
	}
	if !strings.Contains(first[0], "\"a\"") {
		t.Errorf("warnings are not sorted by provider id: %v", first)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("warning %d differs between runs: %q and %q", i, first[i], second[i])
		}
	}
}
