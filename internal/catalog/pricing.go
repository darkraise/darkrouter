package catalog

// CostMicros is the micro-dollar cost of one request at this price, or nil
// when the model has no price.
//
// A method on Pricing, in package catalog, because the arithmetic needs the
// prices and internal/store cannot import internal/catalog: catalog already
// imports store directly, so store->catalog would close a cycle.
//
// Unpriced and free are both zero rates and only Pricing.Known separates
// them. Returning nil for unpriced is what lets the trace and the spend tile
// render an em-dash: reporting 0 would state that a request cost nothing,
// which is a different and usually false claim.
func (p Pricing) CostMicros(in, out, cacheRead, cacheWrite int64) *int64 {
	if !p.Known {
		return nil
	}
	total := rateMicros(p.InputMicrosPerMTok, in) +
		rateMicros(p.OutputMicrosPerMTok, out) +
		rateMicros(p.CacheReadMicrosPerMTok, cacheRead) +
		rateMicros(p.CacheWriteMicrosPerMTok, cacheWrite)
	return &total
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
