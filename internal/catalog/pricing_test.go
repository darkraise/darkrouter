package catalog

import "testing"

func TestCostMicrosIsNilWhenPriceIsUnknown(t *testing.T) {
	// Unpriced and free are both zero prices. Only Known separates them, and
	// a nil result is what makes the UI render an em-dash instead of $0.00.
	if got := (Pricing{Known: false}).CostMicros(1_000_000, 1_000_000, 0); got != nil {
		t.Fatalf("unpriced model: want nil, got %d", *got)
	}
}

func TestCostMicrosIsZeroForAKnownFreeModel(t *testing.T) {
	got := (Pricing{Known: true}).CostMicros(1_000_000, 1_000_000, 0)
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
	got := p.CostMicros(2_000_000, 1_000_000, 4_000_000)
	if got == nil || *got != 22_200_000 {
		t.Fatalf("want 22200000, got %v", got)
	}
}

func TestCostMicrosRoundsHalfUp(t *testing.T) {
	// 1 token at 1 micro/MTok is 0.000001 micros. Truncation would report
	// every small request as free; half-up keeps a sub-micro request at 0
	// but a half-micro one at 1.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(1, 0, 0); got == nil || *got != 1 {
		t.Fatalf("1 token at 1 micro/token: want 1, got %v", got)
	}
	half := Pricing{InputMicrosPerMTok: 1, Known: true}
	if got := half.CostMicros(500_000, 0, 0); got == nil || *got != 1 {
		t.Fatalf("half a micro: want 1 (half-up), got %v", got)
	}
	if got := half.CostMicros(499_999, 0, 0); got == nil || *got != 0 {
		t.Fatalf("just under half a micro: want 0, got %v", got)
	}
}

func TestCostMicrosIgnoresNegativeCounts(t *testing.T) {
	// A provider that reports a negative usage count is malformed, not a
	// refund. Clamping keeps one bad response from making a day's spend
	// smaller than it was.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(-5, 0, 0); got == nil || *got != 0 {
		t.Fatalf("negative tokens: want 0, got %v", got)
	}
}
