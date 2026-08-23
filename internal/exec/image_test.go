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

func imageUpstream(usage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"created":17,"data":[{"b64_json":"aGk="}]`
		if usage {
			body += `,"usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}`
		}
		_, _ = w.Write([]byte(body + `}`))
	}
}

func TestImagesServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-image-1", ir.SurfaceImage))
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-1","prompt":"a cat","n":1}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].B64 != "aGk=" {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "image" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 11 || got.TokensOut != 22 {
		t.Errorf("tokens = %d in, %d out; gpt-image-1 reports both", got.TokensIn, got.TokensOut)
	}
}

func TestAnImageCallReportingNoUsageRecordsNone(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(false))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "dall-e-3", ir.SurfaceImage))
	e.HandleImages(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/images/generations",
			strings.NewReader(`{"model":"dall-e-3","prompt":"a cat"}`)), openaiedge.New())

	got := rec.only(t)
	if got.Status != "success" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.TokensIn != 0 || got.TokensOut != 0 {
		t.Errorf("tokens = %d/%d; the provider reported none", got.TokensIn, got.TokensOut)
	}
	// CostMicros is nil for every surface today — nothing computes cost. This
	// is the guard for when pricing lands: a dall-e call must stay NULL rather
	// than record a confident zero.
	if got.CostMicros != nil {
		t.Errorf("cost = %d; a call with no reported usage must leave cost NULL", *got.CostMicros)
	}
}

func TestImagesFailOverToASecondProvider(t *testing.T) {
	// Spec §10 requires a failover case per surface.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(imageUpstream(true))
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "gpt-image-1", good.URL, "gpt-image-1", ir.SurfaceImage)
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-1","prompt":"a cat"}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d", w.Code, hits.Load())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestAnImageRequestWithNoImageProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"chat-only","prompt":"a cat"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
