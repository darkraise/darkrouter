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
