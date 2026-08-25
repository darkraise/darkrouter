package admin

import (
	"encoding/json"
	"net/http"
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
}
