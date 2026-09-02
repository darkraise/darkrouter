package store

import "time"

// FailoverTraceFixture is the two-attempt trace the admin handler tests and
// this package's own tests read. One definition rather than one per package,
// so both assert against a single fixture shape that cannot drift.
func FailoverTraceFixture(id string) []*RequestRecord {
	cost := int64(1234)
	ttft := int64(56)
	attempt1Cost := int64(200)
	attempt2Cost := int64(1234)
	return []*RequestRecord{{
		ID: id, TS: time.UnixMilli(1700000000000),
		Dialect: "openai", Surface: "llm", RequestedModel: "fast",
		ResolvedAlias: "fast", FinalProviderID: "b", FinalModel: "m2",
		Status: "success", TokensIn: 10, TokensOut: 20, CacheReadTokens: 4,
		ReasoningTokens: 12,
		CostMicros:      &cost, TTFTMs: &ttft,
		Candidates:  []string{"a/m1", "b/m2", "c/m3"},
		Skips:       []string{"c/m3:cooling", "d/m4:no_credential"},
		Warnings:    []string{"top_k -> openai: not expressible"},
		SurfaceMeta: map[string]any{"input_count": 3},
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "a", KeyID: "k1", Model: "m1",
				Outcome: "retryable_provider", StatusCode: 500, LatencyMs: 120,
				Error: "upstream 500", TokensIn: 15, TokensOut: 5, CostMicros: &attempt1Cost},
			{Seq: 2, ProviderID: "b", KeyID: "k2", Model: "m2",
				Outcome: "success", StatusCode: 200, LatencyMs: 340,
				TokensIn: 10, TokensOut: 20, CostMicros: &attempt2Cost},
		},
	}}
}
