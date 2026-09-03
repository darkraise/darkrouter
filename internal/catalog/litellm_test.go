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
