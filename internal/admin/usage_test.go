package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

type overviewBody struct {
	Providers []struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Cooling     int    `json:"cooling"`
		Credentials int    `json:"credentials"`
		NeedsAuth   bool   `json:"needs_reauth"`
	} `json:"providers"`
	RequestsPerMin float64 `json:"requests_per_min"`
	ErrorRate      float64 `json:"error_rate"`
	Spend          struct {
		Micros int64 `json:"micros"`
		Priced bool  `json:"priced"`
	} `json:"today_spend"`
}

func getOverview(t *testing.T, s *Server, cookie *http.Cookie, token string) overviewBody {
	t.Helper()
	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body overviewBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheOverviewShowsOneTilePerProvider(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")
	seedProviderWithKey(t, s, cookie, token, "p2", "https://y/v1")

	body := getOverview(t, s, cookie, token)
	if len(body.Providers) != 2 {
		t.Fatalf("tiles = %+v", body.Providers)
	}
	for _, p := range body.Providers {
		if p.Credentials != 1 {
			t.Errorf("provider %s shows %d credentials", p.ID, p.Credentials)
		}
		if p.State != "healthy" {
			t.Errorf("provider %s state = %q, want healthy", p.ID, p.State)
		}
	}
}

func TestAProviderWithNoCredentialReadsAsUnconfigured(t *testing.T) {
	// Distinct from "degraded": nothing is broken, nothing is set up.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	body := getOverview(t, s, cookie, token)
	if len(body.Providers) != 1 || body.Providers[0].State != "unconfigured" {
		t.Errorf("tiles = %+v", body.Providers)
	}
}

func TestManyCoolingTriplesReadAsOneDegradedProvider(t *testing.T) {
	// Spec §6: provider-level signals, not raw triples. Forty models cooling
	// on one dead credential is one dead provider, not forty problems.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")

	for i := 0; i < 40; i++ {
		k := health.Key{ProviderID: "p1", KeyID: keyID, Model: string(rune('a' + i%26))}
		for j := 0; j < 5; j++ {
			s.deps.Breaker.Record(k, health.Signal{
				Outcome: adapter.OutcomeRetryableProvider, StatusCode: 500,
			})
		}
	}

	body := getOverview(t, s, cookie, token)
	if len(body.Providers) != 1 {
		t.Fatalf("tiles = %+v; cooling models produced more than one tile", body.Providers)
	}
	if body.Providers[0].State != "degraded" {
		t.Errorf("state = %q, want degraded", body.Providers[0].State)
	}
	if body.Providers[0].Cooling == 0 {
		t.Error("cooling count = 0; the tile does not say how much is down")
	}
}

func TestTodaysSpendSaysPricingIsNotWired(t *testing.T) {
	// CostMicros is nil on every row: nothing computes cost, and phase 5
	// recorded why. A confident zero would read as "today was free".
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedLog(t, db, 3)

	body := getOverview(t, s, cookie, token)
	if body.Spend.Priced {
		t.Error("priced = true; nothing computes cost yet and the UI must not claim a total")
	}
}

func TestTheOverviewReportsAnErrorRate(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	now := time.Now()
	db.WriteBatchForTest(t, []*store.RequestRecord{
		{ID: "01A", TS: now, Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success"},
		{ID: "01B", TS: now, Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success"},
		{ID: "01C", TS: now, Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "error"},
		{ID: "01D", TS: now, Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "error"},
	})

	body := getOverview(t, s, cookie, token)
	if body.ErrorRate <= 0 || body.ErrorRate >= 1 {
		t.Errorf("error rate = %v; the fixture is half errors", body.ErrorRate)
	}
	if body.RequestsPerMin <= 0 {
		t.Errorf("requests per min = %v", body.RequestsPerMin)
	}
}

func TestAnEmptyLogReportsZeroRatherThanFailing(t *testing.T) {
	// SUM over no rows is NULL, which fails a scan into int64 rather than
	// yielding zero. A fresh install must not 500 on its first page load.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	body := getOverview(t, s, cookie, token)
	if body.ErrorRate != 0 || body.RequestsPerMin != 0 {
		t.Errorf("overview = %+v", body)
	}
}

func TestUsageRollsUpByDay(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, requests, tokens_in, tokens_out)
		 VALUES ('2026-08-21','a','m',5,10,20), ('2026-08-22','a','m',7,14,28)`); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, cookie, token, "GET", "/api/usage", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Days []struct {
			Day       string `json:"day"`
			Requests  int64  `json:"requests"`
			TokensIn  int64  `json:"tokens_in"`
			TokensOut int64  `json:"tokens_out"`
		} `json:"days"`
		Priced bool `json:"priced"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Days) != 2 {
		t.Fatalf("days = %+v", body.Days)
	}
	// Oldest first: a chart reads left to right.
	if body.Days[0].Day > body.Days[1].Day {
		t.Errorf("days are newest-first: %+v", body.Days)
	}
	if body.Days[0].Requests != 5 || body.Days[1].TokensOut != 28 {
		t.Errorf("rollup = %+v", body.Days)
	}
	if body.Priced {
		t.Error("priced = true with no cost recorded")
	}
}

func TestTodaySpendAgreesWithTheUsageChartAcrossAFailover(t *testing.T) {
	// A failed attempt's cost lands in usage_daily via the rollup but, before
	// this, never reached today_spend -- the tile and the chart answered a
	// different question about the same day.
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	now := time.Now()
	failedCost := int64(500)
	servedCost := int64(1200)
	db.WriteBatchForTest(t, []*store.RequestRecord{{
		ID: "01SPEND", TS: now, Dialect: "openai", Surface: "llm", RequestedModel: "m",
		FinalProviderID: "b", FinalModel: "m", Status: "success",
		CostMicros: &servedCost,
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "a", Model: "m", Outcome: "retryable_provider", CostMicros: &failedCost},
			{Seq: 2, ProviderID: "b", Model: "m", Outcome: "success", CostMicros: &servedCost},
		},
	}})
	if err := db.Rollup(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	body := getOverview(t, s, cookie, token)
	want := failedCost + servedCost
	if !body.Spend.Priced || body.Spend.Micros != want {
		t.Fatalf("today_spend = %+v, want %d priced", body.Spend, want)
	}

	rr := do(t, s, cookie, token, "GET", "/api/usage", "")
	var usage struct {
		Days []struct {
			CostMicros *int64 `json:"cost_micros"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	var chartTotal int64
	for _, d := range usage.Days {
		if d.CostMicros != nil {
			chartTotal += *d.CostMicros
		}
	}
	if chartTotal != body.Spend.Micros {
		t.Fatalf("chart total = %d, today_spend = %d; the two surfaces disagree",
			chartTotal, body.Spend.Micros)
	}
}

func TestTodaySpendIsNotTheFiveMinuteWindow(t *testing.T) {
	// The tile is labelled as the day's spend. Sourcing it from the live
	// window makes it report a few minutes and read as "today was nearly
	// free".
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	c := int64(4200)
	db.WriteBatchForTest(t, []*store.RequestRecord{{
		ID: "old", TS: startOfDay.Add(time.Hour), ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m", CostMicros: &c,
		Attempts: []store.AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m",
			Outcome: "success", CostMicros: &c,
		}},
	}})

	rr := do(t, s, cookie, token, "GET", "/api/overview", "")

	// The handler computes its own start-of-today a moment after this test
	// did; a UTC midnight landing between the two would put the seeded row
	// in what the handler now considers yesterday. That is a clock race, not
	// a bug the assertion below should absorb, so detect it and skip.
	if end := time.Now().UTC(); end.Year() != now.Year() || end.YearDay() != now.YearDay() {
		t.Skip("UTC day boundary crossed during the test")
	}

	var got struct {
		TodaySpend struct {
			Micros *int64 `json:"micros"`
		} `json:"today_spend"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TodaySpend.Micros == nil || *got.TodaySpend.Micros != 4200 {
		val := "nil"
		if got.TodaySpend.Micros != nil {
			val = strconv.FormatInt(*got.TodaySpend.Micros, 10)
		}
		t.Fatalf("today_spend = %s, want 4200 from an hour into the day", val)
	}
}

func TestOverviewCarriesLatencySeriesAndFailovers(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	rr := do(t, s, cookie, token, "GET", "/api/overview", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"latency", "series", "failovers"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("overview is missing %q: %v", k, got)
		}
	}
	// An empty database still answers with the keys present and empty, so the
	// console never has to distinguish "no data" from "old server".
	if got["failovers"] == nil {
		t.Fatal("failovers must be [] rather than null")
	}
	if got["series"] == nil {
		t.Fatal("series must be [] rather than null")
	}
}

func TestOverviewSeriesAndFailoversUseSnakeCaseKeys(t *testing.T) {
	// The console builds against these shapes next; a capitalised Go field
	// name slipping into the same payload as "requests_per_min" would
	// fossilize an inconsistency no consumer asked for.
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, requests, attempts, tokens_in, tokens_out)
		 VALUES ('2026-08-25','groq','m',5,7,10,20)`); err != nil {
		t.Fatal(err)
	}
	db.WriteBatchForTest(t, []*store.RequestRecord{{
		ID: "01F", TS: time.Now(), RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m",
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "other", Model: "m", Outcome: "error"},
			{Seq: 2, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}})

	body := getOverview(t, s, cookie, token)
	_ = body // sanity that the typed decode above still succeeds

	rr := do(t, s, cookie, token, "GET", "/api/overview", "")
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	series, ok := raw["series"].([]any)
	if !ok || len(series) == 0 {
		t.Fatalf("series = %v", raw["series"])
	}
	seriesRow, ok := series[0].(map[string]any)
	if !ok {
		t.Fatalf("series[0] = %v", series[0])
	}
	for _, k := range []string{"day", "requests", "attempts", "tokens_in", "tokens_out", "cost_micros"} {
		if _, ok := seriesRow[k]; !ok {
			t.Errorf("series row missing %q: %v", k, seriesRow)
		}
	}
	for _, k := range []string{"Day", "Requests", "Attempts", "TokensIn", "TokensOut", "CostMicros", "Key"} {
		if _, ok := seriesRow[k]; ok {
			t.Errorf("series row carries capitalised key %q: %v", k, seriesRow)
		}
	}

	failovers, ok := raw["failovers"].([]any)
	if !ok || len(failovers) == 0 {
		t.Fatalf("failovers = %v", raw["failovers"])
	}
	failoverRow, ok := failovers[0].(map[string]any)
	if !ok {
		t.Fatalf("failovers[0] = %v", failovers[0])
	}
	for _, k := range []string{"id", "ts", "alias", "final_provider_id", "final_model", "attempts", "total_ms"} {
		if _, ok := failoverRow[k]; !ok {
			t.Errorf("failover row missing %q: %v", k, failoverRow)
		}
	}
	for _, k := range []string{"ID", "TS", "Alias", "FinalProviderID", "FinalModel", "Attempts", "TotalMs"} {
		if _, ok := failoverRow[k]; ok {
			t.Errorf("failover row carries capitalised key %q: %v", k, failoverRow)
		}
	}
}

func TestUsageGroupByAlias(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','fast-coder',7)`); err != nil {
		t.Fatal(err)
	}

	rr := do(t, s, cookie, token, "GET", "/api/usage?group_by=alias", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		GroupBy string `json:"group_by"`
		Days    []struct {
			Key      string `json:"key"`
			Requests int64  `json:"requests"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GroupBy != "alias" {
		t.Fatalf("group_by echo: want alias, got %q", got.GroupBy)
	}
	if len(got.Days) != 1 || got.Days[0].Key != "fast-coder" || got.Days[0].Requests != 7 {
		t.Fatalf("want one fast-coder row of 7, got %+v", got.Days)
	}
}

func TestUsageGroupByCarriesAttemptsAlongsideRequests(t *testing.T) {
	// requests counts only the attempt that served, so a provider that failed
	// every time reads as requests: 0. Without attempts in the payload it
	// looks like the provider did nothing rather than having burned tokens on
	// every failed try.
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, requests, attempts, tokens_in, tokens_out)
		 VALUES ('2026-08-25','flaky','m',0,7,140,0)`); err != nil {
		t.Fatal(err)
	}

	rr := do(t, s, cookie, token, "GET", "/api/usage?group_by=provider", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Days []struct {
			Key      string `json:"key"`
			Requests int64  `json:"requests"`
			Attempts int64  `json:"attempts"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 1 || got.Days[0].Key != "flaky" {
		t.Fatalf("want one flaky row, got %+v", got.Days)
	}
	if got.Days[0].Requests != 0 || got.Days[0].Attempts != 7 {
		t.Errorf("want requests=0 attempts=7, got %+v", got.Days[0])
	}
}

func TestUsageRejectsAnUnknownGroupBy(t *testing.T) {
	// A typo must not silently fall back to the day-only rollup: the caller
	// would render a chart with one series and no way to tell it asked wrong.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	rr := do(t, s, cookie, token, "GET", "/api/usage?group_by=providr", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown dimension, got %d", rr.Code)
	}
}

func TestUsageWithoutGroupByIsUnchanged(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','fast-coder',7),
		        ('2026-08-25','groq','m','cheap',3)`); err != nil {
		t.Fatal(err)
	}
	rr := do(t, s, cookie, token, "GET", "/api/usage", "")
	var got struct {
		Days []struct {
			Requests int64 `json:"requests"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 1 || got.Days[0].Requests != 10 {
		t.Fatalf("ungrouped: want one row of 10, got %+v", got.Days)
	}
}
