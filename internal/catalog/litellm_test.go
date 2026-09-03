package catalog

import (
	"os"
	"reflect"
	"testing"
)

// Decoded through the production parser from verbatim upstream records, so a
// schema change upstream fails here rather than silently producing zero prices.
func TestParseLiteLLMReadsTheRealSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/litellm-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseLiteLLM(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) == 0 {
		t.Fatal("no providers parsed")
	}
	var priced int
	for _, models := range doc {
		for _, p := range models {
			if p.Known && p.Source != SourceLiteLLM {
				t.Errorf("price stamped %q, want litellm", p.Source)
			}
			if p.Known {
				priced++
			}
		}
	}
	if priced == 0 {
		t.Error("every sampled entry parsed as unpriced")
	}
	if got := doc["openai"]["gpt-4o"].InputMicrosPerMTok; got != 2_500_000 {
		t.Errorf("gpt-4o input = %d, want 2500000", got)
	}
}

// An entry with no cost fields is unpriced, not zero-priced: contributing a
// zeroed candidate would let a silent record outrank a real price.
func TestParseLiteLLMLeavesACostlessEntryUnknown(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{"x":{"litellm_provider":"acme"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc["acme"]["x"].Known {
		t.Error("an entry with no cost fields parsed as priced")
	}
}

// Two upstream keys can reduce to the same model id under the same provider.
// The least-qualified key is the vendor's canonical listing, so its rate is the
// one an operator is billed against; the qualified variants are regional.
func TestParseLiteLLMPrefersTheLeastQualifiedKey(t *testing.T) {
	raw := []byte(`{
		"bedrock/us-gov-east-1/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":9e-06},
		"bedrock/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":3e-06},
		"bedrock/invoke/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":7e-06}
	}`)
	doc, err := ParseLiteLLM(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc["bedrock"]["anthropic.claude-x"].InputMicrosPerMTok; got != 3_000_000 {
		t.Errorf("input = %d, want 3000000 from the least-qualified key", got)
	}
}

// Map iteration order varies per run, so one comparison can agree by accident.
// Repeating it proves the parsed document is a function of its input.
func TestParseLiteLLMIsStableAcrossRepeatedParses(t *testing.T) {
	raw, err := os.ReadFile("testdata/litellm-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	collide := []byte(`{
		"bedrock/us-gov-east-1/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":9e-06},
		"bedrock/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":3e-06},
		"bedrock/invoke/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":7e-06},
		"eu/anthropic.claude-x": {"litellm_provider":"bedrock","input_cost_per_token":5e-06}
	}`)
	for _, input := range [][]byte{raw, collide} {
		first, err := ParseLiteLLM(input)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 20; i++ {
			again, err := ParseLiteLLM(input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, again) {
				t.Fatalf("parse %d differs from the first parse of the same bytes", i)
			}
		}
	}
}

// The real Bedrock shapes: a region qualifier marks a variant listing whose
// rate differs from the vendor's canonical one, so it must lose on shape
// rather than on where its name happens to fall alphabetically.
func TestParseLiteLLMPrefersAnUnqualifiedKeyOverARegionalOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    int64
		wantKey string
	}{{
		name:    "bare key beats a regional one",
		wantKey: "bedrock/deepseek.v3.2",
		want:    3_000_000,
		raw: `{
			"bedrock/ap-northeast-1/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":9e-06},
			"bedrock/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":3e-06}
		}`,
	}, {
		// "ap-northeast-1" sorts before "invoke", so lexicographic order alone
		// picks the regional key here; only the shape rule gets this right.
		name:    "a non-region segment beats a region at equal depth",
		wantKey: "bedrock/invoke/deepseek.v3.2",
		want:    3_000_000,
		raw: `{
			"bedrock/ap-northeast-1/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":9e-06},
			"bedrock/invoke/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":3e-06}
		}`,
	}, {
		name:    "us-gov is region-shaped too",
		wantKey: "bedrock/invoke/amazon.nova-pro-v1:0",
		want:    3_000_000,
		raw: `{
			"bedrock/us-gov-east-1/amazon.nova-pro-v1:0": {"litellm_provider":"bedrock","input_cost_per_token":9e-06},
			"bedrock/invoke/amazon.nova-pro-v1:0": {"litellm_provider":"bedrock","input_cost_per_token":3e-06}
		}`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseLiteLLM([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			var got int64
			for _, p := range doc["bedrock"] {
				got = p.InputMicrosPerMTok
			}
			if got != tc.want {
				t.Errorf("input = %d, want %d from %s", got, tc.want, tc.wantKey)
			}
		})
	}
}

// Equally-qualified candidates need not be regional: two endpoint variants can
// disagree just as materially, and are refused on the same grounds.
func TestParseLiteLLMRefusesDisagreeingNonRegionalCandidates(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{
		"bedrock/invoke/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":3e-06},
		"bedrock/converse/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":5e-06}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p := doc["bedrock"]["deepseek.v3.2"]; p.Known {
		t.Errorf("input = %d, want no price from disagreeing endpoint variants", p.InputMicrosPerMTok)
	}
}

// When the surviving candidates disagree on price there is no canonical key to
// prefer, so the source has nothing to say and must contribute no candidate.
func TestParseLiteLLMRefusesToPriceDisagreeingCandidates(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{
		"bedrock/us-east-1/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":6.2e-07,"output_cost_per_token":1.85e-06},
		"bedrock/ap-south-1/deepseek.v3.2": {"litellm_provider":"bedrock","input_cost_per_token":7.4e-07,"output_cost_per_token":2.22e-06}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	models, ok := doc["bedrock"]
	if !ok {
		t.Fatal("provider missing")
	}
	p, ok := models["deepseek.v3.2"]
	if !ok {
		t.Fatal("the model must stay present even when it cannot be priced")
	}
	if p.Known {
		t.Error("disagreeing candidates produced a price")
	}
	if p.InputMicrosPerMTok != 0 || p.OutputMicrosPerMTok != 0 {
		t.Errorf("rates = %d/%d, want zero", p.InputMicrosPerMTok, p.OutputMicrosPerMTok)
	}
}

// Agreement is the common case among regional listings; refusing there would
// cost coverage for nothing.
func TestParseLiteLLMPricesAgreeingTiedCandidates(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{
		"bedrock/us-east-1/zai.glm-5": {"litellm_provider":"bedrock","input_cost_per_token":5e-07,"output_cost_per_token":1.5e-06},
		"bedrock/ap-south-1/zai.glm-5": {"litellm_provider":"bedrock","input_cost_per_token":5e-07,"output_cost_per_token":1.5e-06}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	p := doc["bedrock"]["zai.glm-5"]
	if !p.Known || p.InputMicrosPerMTok != 500_000 || p.OutputMicrosPerMTok != 1_500_000 {
		t.Errorf("got known=%v %d/%d, want true 500000/1500000", p.Known, p.InputMicrosPerMTok, p.OutputMicrosPerMTok)
	}
}

// The disagreement check must not fire on the single-candidate path, which is
// almost every entry in the index.
func TestParseLiteLLMPricesASingleCandidate(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{
		"bedrock/us-east-1/zai.glm-5": {"litellm_provider":"bedrock","input_cost_per_token":5e-07,"output_cost_per_token":1.5e-06}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p := doc["bedrock"]["zai.glm-5"]; !p.Known || p.InputMicrosPerMTok != 500_000 {
		t.Errorf("got known=%v %d, want true 500000", p.Known, p.InputMicrosPerMTok)
	}
}
