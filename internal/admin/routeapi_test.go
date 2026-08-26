package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type previewBody struct {
	Candidates []struct {
		ProviderID string `json:"provider_id"`
		KeyID      string `json:"key_id"`
		Model      string `json:"model"`
		Kind       string `json:"kind"`
		Inferred   bool   `json:"inferred"`
	} `json:"candidates"`
	Skips []struct {
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
		Reason     string `json:"reason"`
	} `json:"skips"`
	Error string `json:"error"`
}

func preview(t *testing.T, s *Server, body string) (previewBody, int) {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/route/preview", body)
	var out previewBody
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v\n%s", err, w.Body.String())
		}
	}
	return out, w.Code
}

func TestPreviewReturnsTheOrderedCandidates(t *testing.T) {
	s := testServerWithExecutor(t, "http://127.0.0.1:1", "m")
	got, code := preview(t, s, `{"model":"m"}`)
	if code != 200 {
		t.Fatalf("preview = %d", code)
	}
	if len(got.Candidates) == 0 {
		t.Fatalf("no candidates for a configured model: %+v", got)
	}
	if got.Candidates[0].ProviderID != "p" || got.Candidates[0].Model != "m" {
		t.Errorf("first candidate = %+v", got.Candidates[0])
	}
}

func TestPreviewAgreesWithTheRouterExactly(t *testing.T) {
	// §12 states this as an equality, and order is the half that matters: a
	// handler that sorted candidates for display would silently misreport
	// failover order, which is the one thing the Routing screen exists to show.
	s := testServerWithExecutor(t, "http://127.0.0.1:1", "m")

	at := time.Now()
	snap, err := s.deps.Exec.RouteSnapshot(context.Background(), at,
		s.deps.Config.Current())
	if err != nil {
		t.Fatal(err)
	}
	want, wantSkips, rerr := router.Resolve(
		router.Query{Model: "m", Surface: ir.SurfaceLLM}, snap)
	if rerr != nil {
		t.Fatal(rerr)
	}

	got, code := preview(t, s, `{"model":"m"}`)
	if code != 200 {
		t.Fatalf("preview = %d", code)
	}
	if len(got.Candidates) != len(want) {
		t.Fatalf("preview returned %d candidates, router returned %d",
			len(got.Candidates), len(want))
	}
	for i := range want {
		if got.Candidates[i].ProviderID != want[i].ProviderID ||
			got.Candidates[i].Model != want[i].Model ||
			got.Candidates[i].KeyID != want[i].KeyID {
			t.Errorf("candidate %d: preview %+v, router %+v",
				i, got.Candidates[i], want[i])
		}
	}
	if len(got.Skips) != len(wantSkips) {
		t.Errorf("preview returned %d skips, router returned %d",
			len(got.Skips), len(wantSkips))
	}
}

func TestPreviewExplainsAnEmptyResult(t *testing.T) {
	// The skips are the only account of why nothing routed, so an empty
	// candidate list without them is a dead end for the operator.
	s := testServerWithExecutor(t, "http://127.0.0.1:1", "m")
	got, code := preview(t, s, `{"model":"absent"}`)
	if code != 200 {
		t.Fatalf("preview = %d", code)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("an unconfigured model produced candidates: %+v", got.Candidates)
	}
	if got.Error == "" && len(got.Skips) == 0 {
		t.Error("neither an error nor any skip explains the empty result")
	}
}

func TestPreviewRecordsNoRequest(t *testing.T) {
	// A dry run that showed up in the request log would make the trace lie
	// about what the gateway served.
	s := testServerWithExecutor(t, "http://127.0.0.1:1", "m")
	before := countRequests(t, s)
	if _, code := preview(t, s, `{"model":"m"}`); code != 200 {
		t.Fatalf("preview = %d", code)
	}
	if after := countRequests(t, s); after != before {
		t.Errorf("preview logged %d request rows", after-before)
	}
}

func countRequests(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.deps.DB.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPreviewNeedsASession(t *testing.T) {
	s := testServerWithExecutor(t, "http://127.0.0.1:1", "m")
	r := httptest.NewRequest("POST", "/api/route/preview", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthenticated preview = %d, want 401", w.Code)
	}
}
