package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func moderationUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"modr-1","model":"omni","results":[
		  {"flagged":false,"categories":{"hate":false},"category_scores":{"hate":0.001}}]}`))
	}
}

func TestModerationsServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "omni", ir.SurfaceModeration))
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Results []struct {
			Flagged bool `json:"flagged"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "moderation" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 0 || got.TokensOut != 0 {
		t.Errorf("tokens = %d/%d; the moderation endpoint reports none", got.TokensIn, got.TokensOut)
	}
}

func TestModerationsFailOverToASecondProvider(t *testing.T) {
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(moderationUpstream())
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "omni", good.URL, "omni", ir.SurfaceModeration)
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d", w.Code, hits.Load())
	}
	if got := rec.only(t); got.FinalProviderID != "good" {
		t.Errorf("final = %q", got.FinalProviderID)
	}
}

func TestAModerationRequestWithNoModerationProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"chat-only","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
