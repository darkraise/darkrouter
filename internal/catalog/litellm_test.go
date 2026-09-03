package catalog

import (
	"os"
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
