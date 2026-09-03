package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestEmbeddingSuffixIsTrimmed(t *testing.T) {
	e := entry{id: "voyage", baseURL: "https://api.voyageai.com/v1/embeddings"}
	if got := e.toPreset(displayEntry{}).BaseURL; got != "https://api.voyageai.com/v1" {
		t.Errorf("BaseURL = %q, want the API root", got)
	}
}

// trimAPISuffix returns on the first match, so a suffix that is itself the tail
// of a longer one has to come after it or the longer path is trimmed in half.
// No current pair is nested, which is exactly why this asserts the ordering
// rule over the live list rather than trusting one example to notice: adding
// "/completions" beside "/chat/completions" must fail here.
func TestSuffixOrderPutsNestedSuffixesLast(t *testing.T) {
	for i, short := range chatSuffixes {
		for j, long := range chatSuffixes {
			if i < j && strings.HasSuffix(long, short) {
				t.Errorf("chatSuffixes[%d] %q is the tail of [%d] %q; the shorter one "+
					"matches first and trims %q to %q", i, short, j, long, long,
					strings.TrimSuffix(long, short))
			}
		}
	}
}

// The case the rule exists for: /v1/chat/completions is an API root of /v1.
func TestLongerSuffixWinsOverShorter(t *testing.T) {
	e := entry{id: "x", baseURL: "https://api.example.com/v1/chat/completions"}
	if got := e.toPreset(displayEntry{}).BaseURL; got != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q", got)
	}
}

// carryQuirks and applyOverrides run after the merge, so a hand-reviewed
// correction still beats both upstreams.
func TestOverridesStillWinOverBothUpstreams(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://omni.example/v1"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://nine.example/v1"}}}
	m := mergeSources(omni, map[string]displayEntry{}, nine)

	dir := t.TempDir()
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(overrides, []byte("p:\n  base_url: https://override.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOverrides(m.Presets, overrides); err != nil {
		t.Fatal(err)
	}
	if got := m.Presets["p"].BaseURL; got != "https://override.example/v1" {
		t.Errorf("BaseURL = %q, want the override", got)
	}
}

func TestOverriddenFieldIsAttributedToTheOverride(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://omni.example/v1"}}
	m := mergeSources(omni, map[string]displayEntry{}, nil)

	dir := t.TempDir()
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(overrides, []byte("p:\n  base_url: https://override.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOverrides(m.Presets, overrides); err != nil {
		t.Fatal(err)
	}
	markOverridden(&m, overrides)

	if !hasOrigin(m, "p", "base_url", "override") {
		t.Errorf("origins = %v, want base_url attributed to override", m.Origins["p"])
	}
}

// The exemption is what makes a forgotten join key visible, so the generator
// may only write it where the index genuinely has no entry for the id. Blanket
// exemption leaves the preset test unable to fail.
func TestJoinPriceIndexExemptsOnlyAGenuineMiss(t *testing.T) {
	presets := catalog.Presets{
		"groq":      {Name: "Groq"},
		"fireworks": {Name: "Fireworks"}, // spelled fireworks_ai in the index
	}
	joined := joinPriceIndex(presets, map[string]bool{"groq": true, "fireworks_ai": true})
	if joined != 1 {
		t.Errorf("joined = %d, want 1", joined)
	}
	if p := presets["groq"]; p.LiteLLMID != "groq" || p.NoLiteLLM {
		t.Errorf("groq: litellm_id = %q no_litellm = %v, want the key and no exemption",
			p.LiteLLMID, p.NoLiteLLM)
	}
	if p := presets["fireworks"]; p.LiteLLMID != "" || !p.NoLiteLLM {
		t.Errorf("fireworks: litellm_id = %q no_litellm = %v, want an exemption the "+
			"overrides file can correct", p.LiteLLMID, p.NoLiteLLM)
	}
}

func TestReadLiteLLMProvidersCollectsTheIndexsOwnNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "litellm.json")
	if err := os.WriteFile(path, []byte(`{
		"sample_spec": {"litellm_provider": ""},
		"groq/llama-3.3-70b": {"litellm_provider": "groq"},
		"llama-3.3-70b": {"litellm_provider": "groq"},
		"accounts/fireworks/models/x": {"litellm_provider": "fireworks_ai"}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLiteLLMProviders(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["groq"] || !got["fireworks_ai"] {
		t.Errorf("providers = %v, want groq and fireworks_ai", got)
	}
}

func TestReadLiteLLMProvidersRejectsAnEmptyIndex(t *testing.T) {
	// A snapshot that parsed to nothing would exempt every preset at once,
	// which is the state this generator must never write.
	path := filepath.Join(t.TempDir(), "litellm.json")
	if err := os.WriteFile(path, []byte(`{"sample_spec": {"mode": "chat"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readLiteLLMProviders(path); err == nil {
		t.Error("an index naming no providers was accepted")
	}
}
