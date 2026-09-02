package catalog

// Tokens is one request's billable counts.
//
// CacheWrite is the total the provider reported; CacheWrite5m and
// CacheWrite1h are the part of it whose TTL is known and priced on its own.
// Reasoning is thinking the provider reports separately from Output, as
// Gemini does; a provider that folds it into Output reports 0 here.
type Tokens struct {
	Input, Output, Reasoning   int64
	CacheRead, CacheWrite      int64
	CacheWrite5m, CacheWrite1h int64
}

// Anthropic's cache-write multipliers over the input rate. The catalog's
// single cache-write rate is the 5-minute one where models.dev knows it, and
// a write whose TTL the response broke out is priced from the input rate
// rather than that catalog figure, which cannot tell the two TTLs apart.
const (
	cacheWrite5mPerMille = 1250
	cacheWrite1hPerMille = 2000
)

// Cost is the micro-dollar cost of one request at this price, or nil when
// the model has no price.
//
// A method on Pricing, in package catalog, because the arithmetic needs the
// prices and internal/store cannot import internal/catalog: catalog already
// imports store directly, so store->catalog would close a cycle.
//
// Unpriced and free are both zero rates and only Pricing.Known separates
// them. Returning nil for unpriced is what lets the trace and the spend tile
// render an em-dash: reporting 0 would state that a request cost nothing,
// which is a different and usually false claim.
func (p Pricing) Cost(t Tokens) *int64 {
	if !p.Known {
		return nil
	}
	plain := t.CacheWrite - t.CacheWrite5m - t.CacheWrite1h
	total := rateMicros(p.InputMicrosPerMTok, t.Input) +
		rateMicros(p.OutputMicrosPerMTok, t.Output) +
		rateMicros(p.OutputMicrosPerMTok, t.Reasoning) +
		rateMicros(p.CacheReadMicrosPerMTok, t.CacheRead) +
		rateMicros(p.CacheWriteMicrosPerMTok, plain) +
		rateMicros(p.InputMicrosPerMTok*cacheWrite5mPerMille/1000, t.CacheWrite5m) +
		rateMicros(p.InputMicrosPerMTok*cacheWrite1hPerMille/1000, t.CacheWrite1h)
	return &total
}

// CostMicros is Cost for a caller holding only the four classic counts.
func (p Pricing) CostMicros(in, out, cacheRead, cacheWrite int64) *int64 {
	return p.Cost(Tokens{Input: in, Output: out, CacheRead: cacheRead, CacheWrite: cacheWrite})
}

// rateMicros applies one per-million-token rate to one token count, rounding
// half-up. Truncating would report every request under a million tokens as
// costing nothing, which is most of them.
func rateMicros(perMTok, tokens int64) int64 {
	if tokens <= 0 || perMTok <= 0 {
		return 0
	}
	const perM = 1_000_000
	return (perMTok*tokens + perM/2) / perM
}
