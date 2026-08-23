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

func rerankUpstream(seen *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r-1","results":[{"index":0,"relevance_score":0.9}]}`))
	}
}

func TestRerankServesEndToEndAtThePresetPath(t *testing.T) {
	var path string
	upstream := httptest.NewServer(rerankUpstream(&path))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["a","b"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if path != "/v2/rerank" {
		t.Errorf("upstream path = %q, want the preset's declared path", path)
	}
	var body struct {
		Results []struct {
			Index int `json:"index"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "rerank" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
}

func TestRerankEchoesDocumentsTheProviderDoesNotReturn(t *testing.T) {
	// Cohere v2 returns no documents. Darkrouter holds them and results carry
	// the index, so return_documents is honored here rather than forwarded.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r-1","results":[
		  {"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}]}`))
	}))
	defer upstream.Close()

	e, _ := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["alpha","beta"],"return_documents":true}`)),
		openaiedge.New())

	var body struct {
		Results []struct {
			Index    int `json:"index"`
			Document *struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if body.Results[0].Document == nil || body.Results[0].Document.Text != "beta" {
		t.Errorf("first result document = %v, want the document at index 1", body.Results[0].Document)
	}
	if body.Results[1].Document == nil || body.Results[1].Document.Text != "alpha" {
		t.Errorf("second result document = %v", body.Results[1].Document)
	}
}

func TestRerankOmitsDocumentsWhenTheClientDidNotAsk(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, _ := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["alpha"]}`)), openaiedge.New())

	if strings.Contains(w.Body.String(), "document") {
		t.Errorf("body = %s; a document was returned unasked", w.Body.String())
	}
}

func TestRerankFailsOverToASecondProvider(t *testing.T) {
	// Spec §10 requires a failover case per surface.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(rerankUpstream(nil))
	defer good.Close()

	e, rec := failoverPairPreset(t, bad.URL, good.URL, "cohere", "rerank-v3.5", ir.SurfaceRerank)
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d, body = %s",
			w.Code, hits.Load(), w.Body.String())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestARerankRequestWithNoRerankProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"chat-only","query":"q","documents":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}

func TestARerankRequestRecordsItsDroppedDocumentFields(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.HandleRerank(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
			`{"model":"rerank-v3.5","query":"q","documents":[{"text":"t","title":"T"}]}`)),
		openaiedge.New())

	got := rec.only(t)
	if len(got.Warnings) == 0 || !strings.Contains(strings.Join(got.Warnings, " "), "title") {
		t.Errorf("warnings = %v; the dropped field never reached the request row", got.Warnings)
	}
}
