package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// The live window's request count rides along with its rate, so an empty
// window reads as unmeasured rather than as a 0 ms, 0% window.
func TestTheOverviewCountsTheRequestsBehindItsReadings(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	read := func() int64 {
		t.Helper()
		w := do(t, s, cookie, token, "GET", "/api/overview", "")
		var got struct {
			Requests *int64 `json:"requests"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Requests == nil {
			t.Fatalf("no requests count: %s", w.Body.String())
		}
		return *got.Requests
	}
	if n := read(); n != 0 {
		t.Fatalf("empty window counted %d", n)
	}
	seedFailover(t, db)
	if n := read(); n != 1 {
		t.Fatalf("window counted %d, want 1", n)
	}
}

// Usage by provider counts today's requests before any rollup has run, and
// carries the failover pairs over the same days, so the routing graph never
// draws a return between two rows that read "no traffic".
func TestUsageByProviderIsLiveAndCarriesItsFailovers(t *testing.T) {
	s, db := testServerFull(t)
	pinClock(s)
	cookie, token := login(t, s)
	// Ten days ago: outside the 7-day window, inside the 30-day one.
	old := usageClock.AddDate(0, 0, -10)
	failover := func(id string, ts time.Time) *store.RequestRecord {
		return &store.RequestRecord{
			ID: id, TS: ts, Dialect: "openai", Surface: "llm",
			RequestedModel: "m", FinalProviderID: "together", FinalModel: "m", Status: "success",
			Attempts: []store.AttemptRecord{
				{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "retryable_provider"},
				{Seq: 2, ProviderID: "together", Model: "m", Outcome: "success"},
			},
		}
	}
	// The old day was rolled up while it was current; today's request lands
	// after the last rollup, so only the requests table has it.
	storetest.WriteBatch(t, db, []*store.RequestRecord{failover("01OLD", old)})
	for _, at := range []time.Time{old.Add(time.Hour), usageClock.Add(-2 * time.Hour)} {
		if err := db.Rollup(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	storetest.WriteBatch(t, db, []*store.RequestRecord{failover("01TODAY", usageClock.Add(-time.Hour))})

	type body struct {
		Days []struct {
			Key      string `json:"key"`
			Requests int64  `json:"requests"`
		} `json:"days"`
		Edges []struct {
			From     string `json:"from_provider_id"`
			To       string `json:"to_provider_id"`
			Requests int64  `json:"requests"`
		} `json:"failover_edges"`
	}
	get := func(days int) body {
		t.Helper()
		w := do(t, s, cookie, token, "GET",
			"/api/usage?group_by=provider&days="+strconv.Itoa(days), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var b body
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}

	month := get(30)
	served := int64(0)
	for _, d := range month.Days {
		if d.Key == "together" {
			served += d.Requests
		}
	}
	if served != 2 {
		t.Errorf("together served %d over 30 days before any rollup, want 2: %+v", served, month.Days)
	}
	if len(month.Edges) != 1 || month.Edges[0].From != "groq" || month.Edges[0].To != "together" ||
		month.Edges[0].Requests != 2 {
		t.Errorf("30-day edges = %+v, want groq -> together x2", month.Edges)
	}
	if week := get(7); len(week.Edges) != 1 || week.Edges[0].Requests != 1 {
		t.Errorf("7-day edges = %+v, want the one failover inside the week", week.Edges)
	}
}

// The router's "failed over" count is requests, not edges. A rescue by
// another model on the same provider draws no arc between two providers, and
// before this count existed the graph summed its arcs and missed it.
func TestUsageCountsSameProviderFailoversThatDrawNoEdge(t *testing.T) {
	s, db := testServerFull(t)
	seedFailover(t, db)
	storetest.WriteBatch(t, db, []*store.RequestRecord{{
		ID: "01SAMEPROV", TS: time.Now(), Dialect: "openai", Surface: "llm",
		RequestedModel: "chain", FinalProviderID: "lmstudio", FinalModel: "fast",
		Status: "success",
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "lmstudio", Model: "broken", Outcome: "retryable_provider"},
			{Seq: 2, ProviderID: "lmstudio", Model: "fast", Outcome: "success"},
		},
	}})

	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/usage?group_by=provider", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Edges      []json.RawMessage `json:"failover_edges"`
		FailedOver *int64            `json:"failed_over"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 1 {
		t.Errorf("edges = %d, want 1: the same-provider rescue is no edge", len(got.Edges))
	}
	if got.FailedOver == nil || *got.FailedOver != 2 {
		t.Errorf("failed_over = %v, want 2: %s", got.FailedOver, w.Body.String())
	}
}
