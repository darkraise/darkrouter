package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
)

func TestCarryQuirksKeepsWhatTheGeneratorCannotKnow(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(existing, []byte(`
mistral:
  name: Mistral
  kind: openaicompat
  base_url: https://api.mistral.ai/v1
  auth: {style: bearer}
  surfaces: [llm]
  quirks: [strict-unknown-fields, temperature-top-p-exclusive]
retired:
  name: Retired
  kind: openaicompat
  base_url: https://retired.example/v1
  auth: {style: bearer}
  surfaces: [llm]
  quirks: [requires-max-tokens]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	presets := catalog.Presets{
		"mistral":  {Name: "Mistral", Kind: "openaicompat"},
		"deepseek": {Name: "DeepSeek", Kind: "openaicompat", Quirks: []string{"echo-reasoning-content"}},
	}
	carried, err := carryQuirks(presets, existing)
	if err != nil {
		t.Fatal(err)
	}
	if carried != 1 {
		t.Fatalf("carried = %d", carried)
	}
	if got := presets["mistral"].Quirks; len(got) != 2 || got[0] != "strict-unknown-fields" {
		t.Fatalf("mistral quirks = %v", got)
	}
	if got := presets["deepseek"].Quirks; len(got) != 1 {
		t.Fatalf("deepseek quirks = %v; a freshly generated quirk is not overwritten", got)
	}
	if _, ok := presets["retired"]; ok {
		t.Fatal("a preset the generator no longer produces is not resurrected")
	}
}

func TestCarryQuirksToleratesAMissingFile(t *testing.T) {
	presets := catalog.Presets{"x": {Name: "X"}}
	if n, err := carryQuirks(presets, filepath.Join(t.TempDir(), "absent.yaml")); err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
}

func TestOverridesWinOverCarriedQuirks(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "presets.yaml")
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(existing, []byte(`
groq:
  name: Groq
  kind: openaicompat
  base_url: https://api.groq.com/openai/v1
  auth: {style: bearer}
  surfaces: [llm]
  quirks: [requires-max-tokens]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overrides, []byte("groq:\n  quirks: [no-tool-streaming]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	presets := catalog.Presets{"groq": {Name: "Groq", Kind: "openaicompat"}}
	if _, err := carryQuirks(presets, existing); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOverrides(presets, overrides); err != nil {
		t.Fatal(err)
	}
	if got := presets["groq"].Quirks; len(got) != 1 || got[0] != "no-tool-streaming" {
		t.Fatalf("groq quirks = %v", got)
	}
}
