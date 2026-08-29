package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

const liveSample = `{
  "anthropic": {
    "id": "anthropic",
    "models": {
      "claude-opus-4-5": {
        "id": "claude-opus-4-5",
        "tool_call": true,
        "reasoning": true,
        "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000},
        "cost": {"input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25}
      },
      "cheap-model": {
        "id": "cheap-model",
        "tool_call": false,
        "reasoning": false,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 8192, "output": 4096},
        "cost": {"input": 0.14, "output": 0.28}
      },
      "no-price": {
        "id": "no-price",
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 4096, "output": 0}
      }
    }
  }
}`

func TestCentPriceSurvivesAsMicroDollars(t *testing.T) {
	// $0.14 per million is the case that fails if prices are stored per token:
	// it truncates to integer zero and the model reads as free.
	doc, err := ParseModelsDev([]byte(liveSample))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := doc.Metadata("anthropic", "cheap-model")
	if !ok {
		t.Fatal("cheap-model not found")
	}
	if m.InputMicrosPerMTok != 140_000 {
		t.Errorf("input = %d, want 140000", m.InputMicrosPerMTok)
	}
	if m.OutputMicrosPerMTok != 280_000 {
		t.Errorf("output = %d, want 280000", m.OutputMicrosPerMTok)
	}
	if !m.PriceKnown {
		t.Error("PriceKnown is false for a priced model")
	}
}

func TestVisionComesFromModalitiesNotAFlag(t *testing.T) {
	// models.dev has no vision field. Reading one would leave every
	// vision-capable model marked text-only.
	doc, _ := ParseModelsDev([]byte(liveSample))
	m, _ := doc.Metadata("anthropic", "claude-opus-4-5")
	if !m.Capabilities.Vision {
		t.Error("vision not derived from modalities.input")
	}
	if !m.Capabilities.Tools || !m.Capabilities.Reasoning {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	if !m.Capabilities.Known {
		t.Error("capabilities from models.dev are not marked known")
	}
	cheap, _ := doc.Metadata("anthropic", "cheap-model")
	if cheap.Capabilities.Vision {
		t.Error("text-only model reported vision")
	}
	if !cheap.Capabilities.Known {
		t.Error("a models.dev negative is still knowledge and must be marked known")
	}
}

func TestLimitsAndUnknownPrice(t *testing.T) {
	doc, _ := ParseModelsDev([]byte(liveSample))
	m, _ := doc.Metadata("anthropic", "claude-opus-4-5")
	if m.ContextWindow != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("limits = (%d, %d)", m.ContextWindow, m.MaxOutputTokens)
	}
	if m.CacheReadMicrosPerMTok != 500_000 {
		t.Errorf("cache read = %d, want 500000", m.CacheReadMicrosPerMTok)
	}
	np, _ := doc.Metadata("anthropic", "no-price")
	if np.PriceKnown {
		t.Error("an unpriced model reports a known price")
	}
	if np.MaxOutputTokens != 0 {
		t.Errorf("max output = %d, want 0 for an absent limit", np.MaxOutputTokens)
	}
}

func TestCacheWriteRateIsParsed(t *testing.T) {
	// models.dev reports cache creation at a premium over input. Discarding
	// it prices the first request of every cached session as if writing the
	// context were free.
	doc, err := ParseModelsDev([]byte(liveSample))
	if err != nil {
		t.Fatal(err)
	}
	models := Merge(MergeInput{
		Providers: []provider.Provider{{ID: "anthropic", Kind: "anthropic", Preset: "anthropic"}},
		Presets: Presets{"anthropic": {
			Name: "Anthropic", Kind: "anthropic", ModelsDevID: "anthropic",
		}},
		Doc: doc,
		Rows: []store.ModelRow{
			{ProviderID: "anthropic", ModelID: "claude-opus-4-5", State: "live", CapabilitiesSource: "inferred"},
		},
	})
	snap := NewSnapshot(models, []string{"anthropic"})
	m, ok := snap.Lookup("anthropic", "claude-opus-4-5")
	if !ok {
		t.Fatal("claude-opus-4-5 missing from the snapshot")
	}
	if m.Pricing.CacheWriteMicrosPerMTok != 6_250_000 {
		t.Fatalf("cache-write rate = %d, want 6250000 (6.25 per Mtok)",
			m.Pricing.CacheWriteMicrosPerMTok)
	}
}

func TestMissRatherThanZeroValue(t *testing.T) {
	doc, _ := ParseModelsDev([]byte(liveSample))
	if _, ok := doc.Metadata("anthropic", "not-a-model"); ok {
		t.Error("an unknown model reported a hit")
	}
	if _, ok := doc.Metadata("not-a-provider", "claude-opus-4-5"); ok {
		t.Error("an unknown provider reported a hit")
	}
}

func TestMalformedDocumentIsAnError(t *testing.T) {
	// A truncated CDN response must not read as an empty catalog, which would
	// wipe every price on the next sync.
	for _, bad := range []string{"", "not json", "[]", "{}"} {
		if _, err := ParseModelsDev([]byte(bad)); err == nil {
			t.Errorf("%q parsed cleanly", bad)
		}
	}
}

func TestEmbeddedFallbackAgreesWithTheLiveShape(t *testing.T) {
	// The two documents have different shapes and must produce identical
	// numbers. A divergence means the gateway's prices change the first time
	// it reaches the network, which nobody would attribute to a parser.
	fb := FallbackDoc()
	got, ok := fb.Metadata("anthropic", "claude-opus-4-5")
	if !ok {
		t.Fatal("claude-opus-4-5 missing from the embedded snapshot")
	}
	live, _ := ParseModelsDev([]byte(liveSample))
	want, _ := live.Metadata("anthropic", "claude-opus-4-5")
	if got != want {
		t.Errorf("fallback = %+v\nlive     = %+v", got, want)
	}
}

func TestEmbeddedFallbackIsPopulated(t *testing.T) {
	fb := FallbackDoc()
	if len(fb) < 100 {
		t.Fatalf("embedded snapshot has %d providers; regenerate with tools/presetgen", len(fb))
	}
}

func TestEmbeddedFallbackKeepsAZeroPriceKnown(t *testing.T) {
	// The bug this flag exists for. Every price field in the snapshot is
	// omitempty, so a model priced at zero and one nobody has priced both
	// arrive with no numbers at all -- and inferring PriceKnown from the
	// numbers read every free model as unpriced, which is the one verdict
	// that keeps it out of a free-only import.
	fb := FallbackDoc()
	free, ok := fb.Metadata("groq", "allam-2-7b")
	if !ok {
		t.Fatal("allam-2-7b missing from the embedded snapshot")
	}
	if !free.PriceKnown || free.InputMicrosPerMTok != 0 || free.OutputMicrosPerMTok != 0 {
		t.Errorf("allam-2-7b = %+v; want a known price of zero", free)
	}
	if !IsFreeModel("allam-2-7b", free, ok, nil) {
		t.Error("a model models.dev prices at zero must pass the free filter")
	}

	// The other half of the distinction: models.dev publishes no price for
	// whisper at all, and an unpriced model is not free.
	unpriced, ok := fb.Metadata("groq", "whisper-large-v3")
	if !ok {
		t.Fatal("whisper-large-v3 missing from the embedded snapshot")
	}
	if unpriced.PriceKnown {
		t.Errorf("whisper-large-v3 = %+v; want no price known", unpriced)
	}
	if IsFreeModel("whisper-large-v3", unpriced, ok, nil) {
		t.Error("an unpriced model must not pass the free filter")
	}
}
