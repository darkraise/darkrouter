package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
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
	db.WriteBatchForTest(t, batch)
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
	db.SeedFailoverTraceForTest(t, "01FAIL")
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
	db.WriteBatchForTest(t, []*store.RequestRecord{{
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
