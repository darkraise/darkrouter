package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func seedLog(t *testing.T, db *store.DB, n int) {
	t.Helper()
	batch := make([]*store.RequestRecord, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, &store.RequestRecord{
			ID: "01REQ" + string(rune('A'+i)), TS: time.UnixMilli(int64(1700000000000 + i)),
			Dialect: "openai", Surface: "llm", RequestedModel: "m",
			FinalProviderID: "groq", FinalModel: "m", Status: "success",
		})
	}
	storetest.WriteBatch(t, db, batch)
}

func TestTheRequestsEndpointPagesWithACursor(t *testing.T) {
	s, db := testServerFull(t)
	seedLog(t, db, 6)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=3", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var page struct {
		Requests   []struct{ ID string } `json:"requests"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 3 || page.NextCursor == "" {
		t.Fatalf("page one = %+v", page)
	}

	w = do(t, s, cookie, token, "GET", "/api/requests?limit=3&cursor="+page.NextCursor, "")
	var next struct {
		Requests []struct{ ID string } `json:"requests"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if len(next.Requests) != 3 {
		t.Fatalf("page two = %+v", next.Requests)
	}
	for _, a := range page.Requests {
		for _, b := range next.Requests {
			if a.ID == b.ID {
				t.Errorf("row %q appeared on both pages", a.ID)
			}
		}
	}
}

func TestACursorFromDifferentFiltersIsRejected(t *testing.T) {
	// Spec §4.2. The alternative is a page of rows from another result set,
	// which the client cannot detect.
	s, db := testServerFull(t)
	seedLog(t, db, 4)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=2&provider=groq", "")
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if page.NextCursor == "" {
		t.Fatal("no cursor to test with")
	}

	w = do(t, s, cookie, token, "GET",
		"/api/requests?limit=2&provider=openai&cursor="+page.NextCursor, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAnOversizedLimitIsClampedNotRefused(t *testing.T) {
	// Refusing would make a UI bug look like a server outage.
	s, db := testServerFull(t)
	seedLog(t, db, 5)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=1000000", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAnEmptyLogReturnsAnArrayNotNull(t *testing.T) {
	// The table ranges over it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/requests", "")
	if !strings.Contains(w.Body.String(), `"requests":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestTheTraceEndpointExplainsAFailover(t *testing.T) {
	// Spec §6: three attempts must read as three labelled rows with reasons,
	// and the candidates never tried must say why.
	s, db := testServerFull(t)
	storetest.SeedFailoverTrace(t, db, "01FAIL")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests/01FAIL", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var tr struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
		Attempts   []struct {
			Seq      int    `json:"seq"`
			Provider string `json:"provider"`
			Outcome  string `json:"outcome"`
			Error    string `json:"error"`
		} `json:"attempts"`
		Warnings []string `json:"warnings"`
		Bodies   []any    `json:"bodies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.Attempts) != 2 || tr.Attempts[0].Seq != 1 {
		t.Fatalf("attempts = %+v", tr.Attempts)
	}
	if tr.Attempts[0].Error == "" {
		t.Error("the failed attempt carries no reason")
	}
	if len(tr.Skips) != 2 {
		t.Errorf("skips = %v; the drawer cannot say why a candidate was not tried", tr.Skips)
	}
	if len(tr.Candidates) != 3 {
		t.Errorf("candidates = %v", tr.Candidates)
	}
	if tr.Bodies == nil {
		t.Error("bodies is null; the drawer cannot range over it")
	}
}

func TestTraceEndpointCarriesPerAttemptUsage(t *testing.T) {
	// The drawer shows the burn beneath each attempt, not only the request's
	// total -- without per-attempt figures a failover's discarded tokens are
	// invisible next to the try that actually served.
	s, db := testServerFull(t)
	storetest.SeedFailoverTrace(t, db, "01FAIL")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests/01FAIL", "")
	var tr struct {
		Attempts []struct {
			Seq        int    `json:"seq"`
			TokensIn   int64  `json:"tokens_in"`
			TokensOut  int64  `json:"tokens_out"`
			CostMicros *int64 `json:"cost_micros"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.Attempts) != 2 {
		t.Fatalf("attempts = %+v", tr.Attempts)
	}
	failed, served := tr.Attempts[0], tr.Attempts[1]
	if failed.TokensIn != 15 || failed.TokensOut != 5 || failed.CostMicros == nil || *failed.CostMicros != 200 {
		t.Errorf("failed attempt usage = %+v; a discarded try's burn must still show", failed)
	}
	if served.TokensIn != 10 || served.TokensOut != 20 || served.CostMicros == nil || *served.CostMicros != 1234 {
		t.Errorf("served attempt usage = %+v", served)
	}
}

func TestRequestSurfacesReportCacheReadTokens(t *testing.T) {
	// tokens_in now excludes cache reads, so an operator reconciling against a
	// provider invoice needs cache_read_tokens alongside it to recover the
	// full prompt size.
	s, db := testServerFull(t)
	storetest.SeedFailoverTrace(t, db, "01FAIL")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests", "")
	var list struct {
		Requests []struct {
			ID              string `json:"id"`
			TokensIn        int64  `json:"tokens_in"`
			CacheReadTokens int64  `json:"cache_read_tokens"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Requests) != 1 || list.Requests[0].CacheReadTokens != 4 {
		t.Fatalf("requests = %+v; cache reads missing from the list", list.Requests)
	}

	w = do(t, s, cookie, token, "GET", "/api/requests/01FAIL", "")
	var tr struct {
		TokensIn        int64 `json:"tokens_in"`
		CacheReadTokens int64 `json:"cache_read_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.CacheReadTokens != 4 {
		t.Errorf("trace cache_read_tokens = %d, want 4", tr.CacheReadTokens)
	}
}

func TestTraceReportsReasoningTokens(t *testing.T) {
	// Reasoning tokens are billed inside tokens_out and are frequently most of
	// it, so a consumer reading only tokens_out cannot say where the spend
	// went. They are also the one signal that a turn reasoned that survives
	// the passthrough path: a forwarded reply carries the upstream's own field
	// names for the reasoning text, and those disagree between providers, so a
	// client matching on them alone sees nothing for half the fleet.
	s, db := testServerFull(t)
	storetest.SeedFailoverTrace(t, db, "01FAIL")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests/01FAIL", "")
	var tr struct {
		TokensOut       int64 `json:"tokens_out"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.ReasoningTokens != 12 {
		t.Errorf("trace reasoning_tokens = %d, want 12", tr.ReasoningTokens)
	}
	// Stated separately, not subtracted: tokens_out stays the total the
	// provider billed, which is what reconciles against an invoice.
	if tr.TokensOut != 20 {
		t.Errorf("trace tokens_out = %d, want 20 (reasoning is inside it)", tr.TokensOut)
	}
}

func TestAnUnknownTraceIs404(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/requests/nope", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTraceEndpointNamesTheAttemptPath(t *testing.T) {
	// Spec §11's first criterion is only checkable from outside the process if
	// the trace says which path served the request.
	s, db := testServerFull(t)
	storetest.WriteBatch(t, db, []*store.RequestRecord{{
		ID: "01PATH", TS: time.UnixMilli(1700000000000),
		Dialect: "openai", Surface: "llm", RequestedModel: "m",
		FinalProviderID: "p", FinalModel: "m", Status: "success",
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "p", Model: "m", Outcome: "success", StatusCode: 200,
				Path: "passthrough"},
		},
	}})
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests/01PATH", "")
	var body struct {
		Attempts []struct {
			Path string `json:"path"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Attempts) == 0 || body.Attempts[0].Path != "passthrough" {
		t.Errorf("attempts = %+v", body.Attempts)
	}
}

func seedRow(t *testing.T, db *store.DB, id, errorCode, path string) {
	t.Helper()
	rec := &store.RequestRecord{
		ID: id, TS: time.UnixMilli(1700000000000), Dialect: "openai",
		Surface: "llm", RequestedModel: "m", FinalProviderID: "groq",
		FinalModel: "m", Status: "success", ErrorCode: errorCode,
	}
	if path != "" {
		rec.Attempts = []store.AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m", Outcome: "success", Path: path,
		}}
	}
	storetest.WriteBatch(t, db, []*store.RequestRecord{rec})
}

func TestFilteringByErrorCodeReturnsOnlyMatchingRows(t *testing.T) {
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "", "")
	seedRow(t, db, "01REQB", "rate_limit", "")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?error_code=rate_limit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var page struct {
		Requests []struct {
			ID        string `json:"id"`
			ErrorCode string `json:"error_code"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 1 || page.Requests[0].ID != "01REQB" {
		t.Fatalf("want only 01REQB, got %+v", page.Requests)
	}
}

func TestACursorMintedUnderOneErrorCodeIsRejectedUnderAnother(t *testing.T) {
	// The hash is a mismatch detector. A cursor that survived a filter change
	// would page through rows the operator is not looking at, which reads as
	// the table showing the wrong data rather than as a rejected request.
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "rate_limit", "")
	seedRow(t, db, "01REQB", "rate_limit", "")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?error_code=rate_limit&limit=1", "")
	var first struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("no cursor on a page that has more rows")
	}
	w = do(t, s, cookie, token, "GET",
		"/api/requests?error_code=timeout&cursor="+first.NextCursor, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestARequestRowNamesTheServingPath(t *testing.T) {
	// §6.2's passthrough-versus-translated chip. The row has no other source
	// for it: the trace carries a path per attempt, and the list does not.
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "", "passthrough")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests", "")
	var page struct {
		Requests []struct {
			Path string `json:"path"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 1 || page.Requests[0].Path != "passthrough" {
		t.Fatalf("path missing from the row view: %+v", page.Requests)
	}
}

func TestConsoleRequestsAreSeparableFromClientTraffic(t *testing.T) {
	// The whole point of the column: the playground and a provider's test
	// drawer send real requests through the real executor, so they land in the
	// same log a client's does. An operator reading a provider's log has to be
	// able to tell which is which.
	s, db := testServerFull(t)
	now := time.Now()
	storetest.WriteBatch(t, db, []*store.RequestRecord{
		{
			ID: "01CLIENT", TS: now, Dialect: "openai", Surface: "llm",
			RequestedModel: "m", FinalProviderID: "groq", Status: "success",
			Source: store.SourceProxy,
		},
		{
			ID: "01CONSOLE", TS: now, Dialect: "openai", Surface: "llm",
			RequestedModel: "m", FinalProviderID: "groq", Status: "success",
			Source: store.SourceConsole,
		},
	})

	cookie, token := login(t, s)
	only := func(query string) []string {
		res := do(t, s, cookie, token, "GET", "/api/requests"+query, "")
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", res.Code, res.Body.String())
		}
		var page struct {
			Requests []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(page.Requests))
		for _, r := range page.Requests {
			ids = append(ids, r.ID)
		}
		return ids
	}

	if got := only("?source=console"); len(got) != 1 || got[0] != "01CONSOLE" {
		t.Errorf("console filter returned %v", got)
	}
	if got := only("?source=proxy"); len(got) != 1 || got[0] != "01CLIENT" {
		t.Errorf("proxy filter returned %v", got)
	}
	if got := only(""); len(got) != 2 {
		t.Errorf("unfiltered returned %v, want both", got)
	}
}

func TestARequestWithNoSourceReadsAsProxy(t *testing.T) {
	// Every row written before the column existed came through the front door.
	// Backfilling them as anything else would invent a fact about history.
	s, db := testServerFull(t)
	storetest.WriteBatch(t, db, []*store.RequestRecord{{
		ID: "01OLD", TS: time.Now(), Dialect: "openai", Surface: "llm",
		RequestedModel: "m", Status: "success",
	}})
	cookie, token := login(t, s)
	res := do(t, s, cookie, token, "GET", "/api/requests?source=proxy", "")
	if !strings.Contains(res.Body.String(), "01OLD") {
		t.Errorf("an unmarked request did not read as proxy: %s", res.Body.String())
	}
}
