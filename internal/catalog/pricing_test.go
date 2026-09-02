package catalog

import "testing"

func TestCostMicrosIsNilWhenPriceIsUnknown(t *testing.T) {
	// Unpriced and free are both zero prices. Only Known separates them, and
	// a nil result is what makes the UI render an em-dash instead of $0.00.
	if got := (Pricing{Known: false}).CostMicros(1_000_000, 1_000_000, 0, 0); got != nil {
		t.Fatalf("unpriced model: want nil, got %d", *got)
	}
}

func TestCostMicrosIsZeroForAKnownFreeModel(t *testing.T) {
	got := (Pricing{Known: true}).CostMicros(1_000_000, 1_000_000, 0, 0)
	if got == nil {
		t.Fatal("known free model: want 0, got nil")
	}
	if *got != 0 {
		t.Fatalf("known free model: want 0, got %d", *got)
	}
}

func TestCostMicrosSumsTheThreeRates(t *testing.T) {
	p := Pricing{
		InputMicrosPerMTok:     3_000_000,
		OutputMicrosPerMTok:    15_000_000,
		CacheReadMicrosPerMTok: 300_000,
		Known:                  true,
	}
	// 2M in, 1M out, 4M cache-read = 6_000_000 + 15_000_000 + 1_200_000
	got := p.CostMicros(2_000_000, 1_000_000, 4_000_000, 0)
	if got == nil || *got != 22_200_000 {
		t.Fatalf("want 22200000, got %v", got)
	}
}

func TestCostMicrosRoundsHalfUp(t *testing.T) {
	// 1 token at 1 micro/MTok is 0.000001 micros. Truncation would report
	// every small request as free; half-up keeps a sub-micro request at 0
	// but a half-micro one at 1.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(1, 0, 0, 0); got == nil || *got != 1 {
		t.Fatalf("1 token at 1 micro/token: want 1, got %v", got)
	}
	half := Pricing{InputMicrosPerMTok: 1, Known: true}
	if got := half.CostMicros(500_000, 0, 0, 0); got == nil || *got != 1 {
		t.Fatalf("half a micro: want 1 (half-up), got %v", got)
	}
	if got := half.CostMicros(499_999, 0, 0, 0); got == nil || *got != 0 {
		t.Fatalf("just under half a micro: want 0, got %v", got)
	}
}

func TestCostMicrosIgnoresNegativeCounts(t *testing.T) {
	// A provider that reports a negative usage count is malformed, not a
	// refund. Clamping keeps one bad response from making a day's spend
	// smaller than it was.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(-5, 0, 0, 0); got == nil || *got != 0 {
		t.Fatalf("negative tokens: want 0, got %v", got)
	}
}

func TestCacheWritesAreBilled(t *testing.T) {
	p := Pricing{
		Known:                   true,
		InputMicrosPerMTok:      1_000_000,
		OutputMicrosPerMTok:     2_000_000,
		CacheReadMicrosPerMTok:  100_000,
		CacheWriteMicrosPerMTok: 1_250_000,
	}
	got := p.CostMicros(2000, 500, 8000, 4000)
	// 2000*1 + 500*2 + 8000*0.1 + 4000*1.25
	want := int64(2000 + 1000 + 800 + 5000)
	if got == nil || *got != want {
		t.Fatalf("cost = %v, want %d", got, want)
	}
}

func TestAnUnknownCacheWriteRateCostsNothingRatherThanGuessing(t *testing.T) {
	// A model whose payload omits cache_write must not have the input rate
	// substituted: a wrong number is worse than a missing component.
	p := Pricing{Known: true, InputMicrosPerMTok: 1_000_000}
	got := p.CostMicros(1000, 0, 0, 5000)
	if got == nil || *got != 1000 {
		t.Fatalf("cost = %v, want 1000 with no cache-write rate", got)
	}
}

func TestReasoningTokensAreBilledAtTheOutputRate(t *testing.T) {
	// Gemini reports thoughts separately from candidates and bills them as
	// output. Leaving them out under-reports every reasoning request.
	p := Pricing{Known: true, InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 2_000_000}
	got := p.Cost(Tokens{Input: 1000, Output: 500, Reasoning: 250})
	if got == nil || *got != 1000+1000+500 {
		t.Fatalf("cost = %v, want 2500", got)
	}
}

func TestCacheWritesAreBilledByTTL(t *testing.T) {
	// Anthropic prices a 5-minute write at 1.25x input and a 1-hour write
	// at 2x. The catalog's single cache-write rate covers only the writes
	// whose TTL the response did not break out.
	p := Pricing{
		Known: true, InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 2_000_000,
		CacheWriteMicrosPerMTok: 1_250_000,
	}
	got := p.Cost(Tokens{Input: 1000, CacheWrite: 3000, CacheWrite5m: 1000, CacheWrite1h: 1000})
	// 1000*1 + 1000*1.25 + 1000*2 + the remaining 1000 at the catalog rate 1.25
	want := int64(1000 + 1250 + 2000 + 1250)
	if got == nil || *got != want {
		t.Fatalf("cost = %v, want %d", got, want)
	}
}

func TestCostMicrosIsTheFourFieldForm(t *testing.T) {
	p := Pricing{Known: true, InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 2_000_000,
		CacheReadMicrosPerMTok: 100_000, CacheWriteMicrosPerMTok: 1_250_000}
	a := p.CostMicros(2000, 500, 8000, 4000)
	b := p.Cost(Tokens{Input: 2000, Output: 500, CacheRead: 8000, CacheWrite: 4000})
	if a == nil || b == nil || *a != *b {
		t.Fatalf("CostMicros = %v, Cost = %v; the two forms must agree", a, b)
	}
}
