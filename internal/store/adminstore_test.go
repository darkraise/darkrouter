package store

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestASessionRoundTrips(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-1", time.Hour); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a live session did not validate")
	}
}

func TestAnUnknownSessionIsAMissRatherThanAnError(t *testing.T) {
	// The two mean different things to the caller: a miss renders the login
	// screen, an error is a 500. Collapsing them makes an outage look like a
	// logout.
	db := migrated(t)
	ok, err := db.TouchSession(context.Background(), "never-existed", time.Hour)
	if err != nil {
		t.Fatalf("a miss was reported as an error: %v", err)
	}
	if ok {
		t.Error("an unknown session validated")
	}
}

func TestTouchExtendsTheExpiry(t *testing.T) {
	// Spec §3: the expiry slides. Without this an operator is logged out
	// thirty days after logging in regardless of use.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-2", time.Minute); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE id = ?`, HashSessionID("sess-2")).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TouchSession(ctx, "sess-2", 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE id = ?`, HashSessionID("sess-2")).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("expiry did not slide: %d -> %d", before, after)
	}
}

func TestAnExpiredSessionDoesNotValidate(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-3", -time.Minute); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-3", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an expired session validated")
	}
}

func TestAnExpiredSessionIsNotResurrectedByTouch(t *testing.T) {
	// The expiry check lives in the UPDATE's WHERE. A read-then-write would
	// extend the row it just decided was dead.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-4", -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TouchSession(ctx, "sess-4", 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-4", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an expired session came back to life")
	}
}

func TestDeleteSessionRemovesTheRow(t *testing.T) {
	// Spec §3: logout deletes the row rather than only clearing the cookie.
	// A cleared cookie leaves a valid session id in the database for anyone
	// who copied it.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-5", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession(ctx, "sess-5"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM sessions WHERE id = 'sess-5'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d rows remain after logout", n)
	}
}

func TestSweepRemovesOnlyExpiredSessions(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "live", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "dead", -time.Hour); err != nil {
		t.Fatal(err)
	}
	n, err := db.SweepSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept %d rows, want 1", n)
	}
	ok, _ := db.TouchSession(ctx, "live", time.Hour)
	if !ok {
		t.Error("the sweep removed a live session")
	}
}

func TestCreateAndReadAProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P One", Preset: "groq", Kind: "openaicompat",
		BaseURL: "https://x/v1", Priority: 7, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ProviderRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "p1" || rows[0].Preset != "groq" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Priority != 7 || !rows[0].Enabled {
		t.Errorf("row = %+v", rows[0])
	}
	if rows[0].AuthStyle != "bearer" {
		t.Errorf("auth style = %q; a row created here must match one from the importer",
			rows[0].AuthStyle)
	}
}

func TestCreatingADuplicateProviderIsAnError(t *testing.T) {
	// The settings screen turns this into "that id is taken" rather than a
	// silent overwrite of a working provider.
	db := migrated(t)
	ctx := context.Background()
	p := ProviderRow{ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1"}
	if err := db.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, p); err == nil {
		t.Error("a duplicate id was accepted")
	}
}

func TestUpdateTouchesOnlyWhatThePatchNames(t *testing.T) {
	// A value struct cannot tell "set priority to 0" from "leave it alone",
	// and 0 is a legal priority meaning last resort.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat",
		BaseURL: "https://x/v1", Priority: 7, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := db.UpdateProvider(ctx, "p1", ProviderPatch{Priority: &zero}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.ProviderRows(ctx)
	if rows[0].Priority != 0 {
		t.Errorf("priority = %d, want 0", rows[0].Priority)
	}
	if rows[0].BaseURL != "https://x/v1" {
		t.Errorf("base url = %q; an untouched field changed", rows[0].BaseURL)
	}
	if !rows[0].Enabled {
		t.Error("enabled changed; the patch did not name it")
	}
}

func TestAnEmptyPatchIsAnError(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateProvider(ctx, "p1", ProviderPatch{}); err == nil {
		t.Error("an empty patch succeeded; the UI sent a form it did not fill in")
	}
}

func TestUpdatingAnUnknownProviderIsAnError(t *testing.T) {
	db := migrated(t)
	enabled := false
	if err := db.UpdateProvider(context.Background(), "nope",
		ProviderPatch{Enabled: &enabled}); err == nil {
		t.Error("patching a provider that does not exist succeeded")
	}
}

func TestDeleteCascadesToCredentialsAndModels(t *testing.T) {
	// A provider row without its credentials cannot serve; a credential
	// without its provider is a decryptable secret nobody can account for.
	// The schema's ON DELETE CASCADE does this, which foreign keys being on
	// makes real — this is the test that proves the pragma is actually set.
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p1", Label: "k", Secret: "sk-x", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, last_seen_at)
		 VALUES ('p1','m','live',1)`); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteProvider(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM providers WHERE id = 'p1'`,
		`SELECT count(*) FROM provider_keys WHERE provider_id = 'p1'`,
		`SELECT count(*) FROM models WHERE provider_id = 'p1'`,
	} {
		var n int
		if err := db.Read.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s left %d rows", q, n)
		}
	}
}

func TestDeletingAnUnknownProviderIsAnError(t *testing.T) {
	if err := migrated(t).DeleteProvider(context.Background(), "nope"); err == nil {
		t.Error("deleting a provider that does not exist succeeded")
	}
}

func TestDeleteCredentialLeavesTheProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p1", Label: "k", Secret: "sk-x", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteCredential(ctx, "p1", id); err != nil {
		t.Fatal(err)
	}
	creds, err := db.Credentials(ctx, key, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 0 {
		t.Errorf("credentials = %+v", creds)
	}
	rows, _ := db.ProviderRows(ctx)
	if len(rows) != 1 {
		t.Error("deleting a credential removed its provider")
	}
}

func seedRequests(t *testing.T, db *DB, n int) {
	t.Helper()
	batch := make([]*RequestRecord, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, &RequestRecord{
			ID: fmt.Sprintf("01REQ%08d", i),
			// Distinct milliseconds so the ordering is unambiguous except
			// where a test deliberately collides them.
			TS:              time.UnixMilli(int64(1700000000000 + i)),
			Dialect:         "openai",
			Surface:         "llm",
			RequestedModel:  "m",
			FinalProviderID: "groq",
			FinalModel:      "m",
			Status:          "success",
		})
	}
	db.WriteBatchForTest(t, batch)
}

func TestListRequestsReturnsNewestFirst(t *testing.T) {
	db := migrated(t)
	seedRequests(t, db, 5)
	got, err := db.ListRequests(context.Background(), RequestQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].ID != "01REQ00000004" {
		t.Errorf("first row = %q, want the newest", got[0].ID)
	}
}

func TestAPageBoundaryNeitherRepeatsNorSkips(t *testing.T) {
	db := migrated(t)
	seedRequests(t, db, 10)
	ctx := context.Background()

	first, err := db.ListRequests(ctx, RequestQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	last := first[len(first)-1]
	second, err := db.ListRequests(ctx, RequestQuery{
		Limit: 4, AfterTS: last.TSMs, AfterID: last.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range append(append([]RequestSummary{}, first...), second...) {
		if seen[r.ID] {
			t.Errorf("row %q appeared twice across the boundary", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 8 {
		t.Errorf("saw %d distinct rows across two pages of 4", len(seen))
	}
}

func TestAnInsertMidScrollDoesNotShiftThePages(t *testing.T) {
	// Spec §7 names this case. Offset pagination gets it wrong: a row inserted
	// at the head shifts every later page by one, so the reader sees a row
	// twice and never sees another. Keyset does not.
	db := migrated(t)
	seedRequests(t, db, 10)
	ctx := context.Background()

	first, err := db.ListRequests(ctx, RequestQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new request lands at the head while the operator reads page one.
	db.WriteBatchForTest(t, []*RequestRecord{{
		ID: "01REQZZZZZZZZ", TS: time.UnixMilli(1700000099999),
		Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success",
	}})

	last := first[len(first)-1]
	second, err := db.ListRequests(ctx, RequestQuery{
		Limit: 4, AfterTS: last.TSMs, AfterID: last.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range second {
		for _, f := range first {
			if r.ID == f.ID {
				t.Errorf("row %q repeated after an insert landed mid-scroll", r.ID)
			}
		}
		if r.ID == "01REQZZZZZZZZ" {
			t.Error("the newly inserted row appeared on page two")
		}
	}
}

func TestIdenticalTimestampsStillOrderTotally(t *testing.T) {
	// ULIDs are lexicographically ordered, which is what makes the tie-break
	// total. Without it a page boundary on a busy millisecond repeats forever.
	db := migrated(t)
	ctx := context.Background()
	var batch []*RequestRecord
	for _, id := range []string{"01AAA", "01BBB", "01CCC"} {
		batch = append(batch, &RequestRecord{
			ID: id, TS: time.UnixMilli(1700000000000),
			Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success",
		})
	}
	db.WriteBatchForTest(t, batch)

	got, err := db.ListRequests(ctx, RequestQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "01CCC" || got[1].ID != "01BBB" {
		t.Fatalf("page one = %+v", got)
	}
	next, err := db.ListRequests(ctx, RequestQuery{
		Limit: 2, AfterTS: got[1].TSMs, AfterID: got[1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].ID != "01AAA" {
		t.Errorf("page two = %+v", next)
	}
}

func TestFiltersNarrowTheResult(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	db.WriteBatchForTest(t, []*RequestRecord{
		{ID: "01A", TS: time.UnixMilli(3), Dialect: "openai", Surface: "llm",
			RequestedModel: "m", FinalProviderID: "groq", Status: "success"},
		{ID: "01B", TS: time.UnixMilli(2), Dialect: "openai", Surface: "embedding",
			RequestedModel: "e", FinalProviderID: "openai", Status: "error"},
	})
	got, err := db.ListRequests(ctx, RequestQuery{Limit: 10, Provider: "groq"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "01A" {
		t.Errorf("provider filter = %+v", got)
	}
	got, err = db.ListRequests(ctx, RequestQuery{Limit: 10, Surface: "embedding"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "01B" {
		t.Errorf("surface filter = %+v", got)
	}
}

func TestAnOversizedLimitIsClamped(t *testing.T) {
	// Refusing would make a UI bug look like a server outage; an unbounded
	// page would build a million-row array in memory.
	db := migrated(t)
	seedRequests(t, db, 5)
	got, err := db.ListRequests(context.Background(), RequestQuery{Limit: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Errorf("got %d rows", len(got))
	}
}

func TestTheAttemptCountIsPerRequest(t *testing.T) {
	db := migrated(t)
	db.WriteBatchForTest(t, []*RequestRecord{{
		ID: "01A", TS: time.UnixMilli(1), Dialect: "openai", Surface: "llm",
		RequestedModel: "m", Status: "success",
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "a", Model: "m", Outcome: "retryable_provider"},
			{Seq: 2, ProviderID: "b", Model: "m", Outcome: "success"},
		},
	}})
	got, err := db.ListRequests(context.Background(), RequestQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Attempts != 2 {
		t.Errorf("attempts = %+v; a failover is what an operator scans for", got)
	}
}

func TestRequestTraceCarriesCandidatesSkipsAndAttempts(t *testing.T) {
	db := migrated(t)
	db.SeedFailoverTraceForTest(t, "01TRACE")

	tr, ok, err := db.RequestTrace(context.Background(), "01TRACE")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the trace was not found")
	}
	if len(tr.Candidates) != 3 {
		t.Errorf("candidates = %v", tr.Candidates)
	}
	if len(tr.Skips) != 2 || tr.Skips[0] != "c/m3:cooling" {
		t.Errorf("skips = %v; the drawer cannot say why a target was not tried", tr.Skips)
	}
	if len(tr.Attempts) != 2 {
		t.Fatalf("attempts = %+v", tr.Attempts)
	}
	if tr.Attempts[0].Seq != 1 || tr.Attempts[1].Outcome != "success" {
		t.Errorf("attempts are out of order or wrong: %+v", tr.Attempts)
	}
	if len(tr.Warnings) != 1 {
		t.Errorf("warnings = %v", tr.Warnings)
	}
	if tr.SurfaceMeta["input_count"].(float64) != 3 {
		t.Errorf("surface meta = %v", tr.SurfaceMeta)
	}
	if tr.Bodies == nil {
		t.Error("bodies is nil; it must be an empty slice so the drawer can range over it")
	}
}

func TestAnUnknownTraceIsAMissRatherThanAnError(t *testing.T) {
	db := migrated(t)
	_, ok, err := db.RequestTrace(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("a miss was reported as an error: %v", err)
	}
	if ok {
		t.Error("an unknown id was found")
	}
}

func TestATraceWithNoCandidatesReturnsEmptySlices(t *testing.T) {
	// Every list the drawer ranges over must be an array, never null.
	db := migrated(t)
	db.WriteBatchForTest(t, []*RequestRecord{{
		ID: "01BARE", TS: time.UnixMilli(1), Dialect: "openai",
		Surface: "llm", RequestedModel: "m", Status: "error",
	}})
	tr, ok, err := db.RequestTrace(context.Background(), "01BARE")
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if tr.Candidates == nil || tr.Skips == nil || tr.Warnings == nil ||
		tr.Attempts == nil || tr.Bodies == nil {
		t.Errorf("a nil list would render as null: %+v", tr)
	}
}

func TestCapturedBodiesAreReadWhenPresent(t *testing.T) {
	// capture.bodies has no writer, so this inserts directly. The query has to
	// work the day one lands, and nothing else would exercise it.
	db := migrated(t)
	ctx := context.Background()
	db.WriteBatchForTest(t, []*RequestRecord{{
		ID: "01BODY", TS: time.UnixMilli(1), Dialect: "openai",
		Surface: "llm", RequestedModel: "m", Status: "success",
	}})
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO request_bodies (request_id, request_json, response_json, expires_at)
		 VALUES ('01BODY', '{"in":1}', '{"out":2}', 9999999999999)`); err != nil {
		t.Fatal(err)
	}
	tr, _, err := db.RequestTrace(ctx, "01BODY")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Bodies) != 2 {
		t.Fatalf("bodies = %+v", tr.Bodies)
	}
	if tr.Bodies[0].Kind != "request" || tr.Bodies[1].Kind != "response" {
		t.Errorf("bodies = %+v", tr.Bodies)
	}
}

func TestUsageByAliasSplitsTheDay(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	for _, r := range []struct {
		alias string
		n     int64
	}{{"fast-coder", 7}, {"cheap", 3}} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
			 VALUES ('2026-08-25','groq','m',?,?)`, r.alias, r.n); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.UsageBy(ctx, 30, UsageByAlias)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Key] = r.Requests
	}
	if got["fast-coder"] != 7 || got["cheap"] != 3 {
		t.Fatalf("want fast-coder=7 cheap=3, got %v", got)
	}

	// The day-only rollup still aggregates across aliases.
	flat, err := db.UsageBy(ctx, 30, UsageByDayOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 1 || flat[0].Requests != 10 {
		t.Fatalf("want one day totalling 10, got %+v", flat)
	}
}

// TestUsageByLimitsDaysNotRows is the fixture that tells a day-bounded LIMIT
// apart from a row-bounded one. Three days x two providers is six rows.
// Asking UsageBy for 2 days must return every row from the two newest days:
// four rows spanning exactly two distinct days. A row-bounded `LIMIT 2`
// instead returns the first two rows the query happens to emit, which cover
// only one day -- so this test fails against that bug and passes against a
// correct day-scoped LIMIT.
func TestUsageByLimitsDaysNotRows(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	for _, day := range []string{"2026-08-23", "2026-08-24", "2026-08-25"} {
		for _, provider := range []string{"groq", "openai"} {
			if _, err := db.Write.ExecContext(ctx,
				`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
				 VALUES (?, ?, 'm', '', 1)`, day, provider); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows, err := db.UsageBy(ctx, 2, UsageByProvider)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 rows (2 days x 2 providers), got %d: %+v", len(rows), rows)
	}
	days := map[string]bool{}
	for _, r := range rows {
		days[r.Day] = true
	}
	if len(days) != 2 {
		t.Fatalf("want 2 distinct days, got %d: %v", len(days), days)
	}
	if days["2026-08-23"] {
		t.Errorf("the oldest day should have been dropped by the 2-day limit: %v", days)
	}
	if !days["2026-08-24"] || !days["2026-08-25"] {
		t.Errorf("want the two newest days, got %v", days)
	}
}

// TestUsageByClampsDays pins the 1..365 clamp: days=0, a negative value and
// an oversized value must all read as if days=30 had been asked for. Without
// the clamp, days=0 would query LIMIT 0 (zero days back) and days=10000
// would place no bound at all, so an unclamped implementation returns a
// different row count than a clamped one on this fixture -- this test fails
// if the clamp is removed.
func TestUsageByClampsDays(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','',1)`); err != nil {
		t.Fatal(err)
	}

	base, err := db.UsageBy(ctx, 30, UsageByDayOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(base) != 1 {
		t.Fatalf("fixture setup: want 1 row at days=30, got %d", len(base))
	}

	for _, days := range []int{0, -5, 10000} {
		got, err := db.UsageBy(ctx, days, UsageByDayOnly)
		if err != nil {
			t.Fatalf("days=%d: %v", days, err)
		}
		if len(got) != len(base) {
			t.Errorf("days=%d: want %d rows (clamped to 30), got %d",
				days, len(base), len(got))
		}
	}
}

func TestLatencyPercentiles(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()
	// 1..100ms; p50 is the 50th value and p95 the 95th.
	for i := 1; i <= 100; i++ {
		total := int64(i)
		rec := &RequestRecord{
			ID: fmt.Sprintf("r%03d", i), TS: now,
			RequestedModel: "m", FinalProviderID: "groq", FinalModel: "m",
			TotalMs: &total,
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.WriteBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	p50, p95, err := db.LatencyPercentiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if p50 != 50 || p95 != 95 {
		t.Fatalf("want p50=50 p95=95, got %d/%d", p50, p95)
	}
}

func TestRecentFailoversReturnsOnlyMultiAttemptRequests(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()
	mk := func(id string, attempts int) {
		rec := &RequestRecord{
			ID: id, TS: now, RequestedModel: "m",
			FinalProviderID: "groq", FinalModel: "m",
		}
		for i := 1; i <= attempts; i++ {
			rec.Attempts = append(rec.Attempts, AttemptRecord{
				Seq: i, ProviderID: "groq", Model: "m", Outcome: "success",
			})
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.WriteBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	mk("one", 1)
	mk("three", 3)

	got, err := db.RecentFailovers(ctx, time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "three" || got[0].Attempts != 3 {
		t.Fatalf("want only the 3-attempt request, got %+v", got)
	}
}

func TestRecentFailoversExcludesOnesOlderThanTheWindow(t *testing.T) {
	// A quiet gateway must not show a month-old failover on the live overview
	// as though it just happened.
	db := migrated(t)
	ctx := context.Background()
	mk := func(id string, ts time.Time) {
		rec := &RequestRecord{
			ID: id, TS: ts, RequestedModel: "m",
			FinalProviderID: "groq", FinalModel: "m",
		}
		for i := 1; i <= 3; i++ {
			rec.Attempts = append(rec.Attempts, AttemptRecord{
				Seq: i, ProviderID: "groq", Model: "m", Outcome: "success",
			})
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.WriteBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	mk("stale", time.Now().Add(-2*time.Hour))
	mk("fresh", time.Now())

	got, err := db.RecentFailovers(ctx, time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("want only the failover inside the window, got %+v", got)
	}
}

func TestSpendSinceCoversTheWholeDay(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Both rows are placed relative to startOfDay rather than to time.Now():
	// a row placed relative to "now" (e.g. "an hour ago") can land before
	// midnight and flip days when the test happens to run in the small hours,
	// which would make this flaky rather than a fixed fixture.
	early, late := int64(1000), int64(250)
	for i, r := range []struct {
		ts   time.Time
		cost int64
	}{
		{startOfDay.Add(30 * time.Minute), early},
		{startOfDay.Add(20 * time.Hour), late},
	} {
		c := r.cost
		if _, err := w.WriteBatch(ctx, []*RequestRecord{{
			ID: fmt.Sprintf("s%d", i), TS: r.ts, ResolvedAlias: "fast",
			FinalProviderID: "groq", FinalModel: "m", CostMicros: &c,
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "success", CostMicros: &c,
			}},
		}}); err != nil {
			t.Fatal(err)
		}
	}

	micros, priced, err := db.SpendSince(ctx, startOfDay)
	if err != nil {
		t.Fatal(err)
	}
	if !priced {
		t.Fatal("priced must be true when any row carries a cost")
	}
	if micros == nil || *micros != early+late {
		got := "nil"
		if micros != nil {
			got = strconv.FormatInt(*micros, 10)
		}
		t.Fatalf("spend = %s, want %d: a request from earlier today was dropped",
			got, early+late)
	}
}

func TestSpendSinceExcludesRowsBeforeSince(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	yesterday := int64(9999)
	if _, err := w.WriteBatch(ctx, []*RequestRecord{{
		ID: "before", TS: startOfDay.Add(-time.Hour), ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m", CostMicros: &yesterday,
		Attempts: []AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m",
			Outcome: "success", CostMicros: &yesterday,
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	micros, priced, err := db.SpendSince(ctx, startOfDay)
	if err != nil {
		t.Fatal(err)
	}
	if priced || micros != nil {
		t.Fatalf("spend = %v priced=%v, want nil/false: only a row from before "+
			"since exists", micros, priced)
	}
}

func TestSpendSinceIsNilWhenNothingIsPriced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	if _, err := w.WriteBatch(ctx, []*RequestRecord{{
		ID: "unpriced", TS: startOfDay.Add(time.Hour), ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m",
		Attempts: []AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m", Outcome: "success",
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	micros, priced, err := db.SpendSince(ctx, startOfDay)
	if err != nil {
		t.Fatal(err)
	}
	if priced || micros != nil {
		t.Fatalf("spend = %v priced=%v, want nil/false: an unpriced model must "+
			"not read as a confident zero", micros, priced)
	}
}

func TestDiscoveryHealthAggregatesPerProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	for _, p := range []string{"groq", "nebius"} {
		if err := db.CreateProvider(ctx, ProviderRow{
			ID: p, Name: p, Kind: "openaicompat", BaseURL: "https://" + p + "/v1", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(providerID, modelID, state string, streak int) {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO models (provider_id, model_id, state, missing_streak)
			 VALUES (?, ?, ?, ?)`, providerID, modelID, state, streak); err != nil {
			t.Fatal(err)
		}
	}
	seed("groq", "m1", "live", 0)
	seed("groq", "m2", "live", 0)
	seed("groq", "m3", "stale", 3)
	seed("nebius", "m4", "removed_upstream", 9)

	got, err := db.DiscoveryHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ProviderID != "groq" || got[1].ProviderID != "nebius" {
		t.Fatalf("want groq then nebius in order, got %+v", got)
	}
	g := got[0]
	if g.Total != 3 || g.Live != 2 || g.Stale != 1 || g.RemovedUpstream != 0 || g.MaxMissingStreak != 3 {
		t.Fatalf("groq rollup wrong: %+v", g)
	}
	n := got[1]
	if n.Total != 1 || n.RemovedUpstream != 1 || n.MaxMissingStreak != 9 {
		t.Fatalf("nebius rollup wrong: %+v", n)
	}
}

func TestDiscoveryHealthIsEmptySliceNotNil(t *testing.T) {
	// A nil slice marshals to JSON null, which the console would have to special
	// case; an empty result must already be the empty-array shape.
	got, err := migrated(t).DiscoveryHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("want an empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("want no rows before any provider is catalogued, got %+v", got)
	}
}

func TestSessionRowsReadTheStoredMilliseconds(t *testing.T) {
	// CreateSession writes UnixMilli. A reader that decodes those as seconds
	// puts every row about 56,000 years into the future, which is both a
	// nonsense timestamp on screen and an expiry filter that can never
	// exclude anything.
	db := migrated(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Minute)
	if err := db.CreateSession(ctx, "sess-live", time.Hour); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SessionRows(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].CreatedAt.Before(before) || rows[0].CreatedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("created_at is %s, want roughly now", rows[0].CreatedAt)
	}
	if got := rows[0].ExpiresAt.Sub(rows[0].CreatedAt); got < 59*time.Minute || got > 61*time.Minute {
		t.Errorf("expiry is %s after creation, want about an hour", got)
	}
}

func TestSessionRowsOmitAnExpiredSession(t *testing.T) {
	// The listing is the screen an operator revokes from. A row that expired
	// last week is not a browser anyone can sign out.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-dead", time.Hour); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SessionRows(ctx, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want none: an expired session was listed", len(rows))
	}
}
