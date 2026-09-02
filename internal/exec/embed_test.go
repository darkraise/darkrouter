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

// embedUpstream answers /v1/embeddings with one vector, reporting the model it
// was asked for so a test can tell which candidate served.
func embedUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","model":"` + in.Model +
			`","data":[{"object":"embedding","index":0,"embedding":[0.5,0.25]}],` +
			`"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}
}

func TestEmbeddingsServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "e5", ir.SurfaceEmbedding))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 1 || body.Data[0].Embedding[0] != 0.5 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "embedding" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 4 || got.TokensOut != 0 {
		t.Errorf("tokens = %d in, %d out; embeddings report input only", got.TokensIn, got.TokensOut)
	}
	if w.Header().Get("X-Darkrouter-Model") != "e5" {
		t.Errorf("X-Darkrouter-Model = %q; spec §8 requires it always",
			w.Header().Get("X-Darkrouter-Model"))
	}
}

func TestEmbeddingsFailOverToASecondProvider(t *testing.T) {
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(embedUpstream())
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "e5", good.URL, "e5", ir.SurfaceEmbedding)
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("the failing provider was called %d times", hits.Load())
	}
	got := rec.only(t)
	if len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "vector space") {
			t.Errorf("a same-model failover raised the cross-model warning: %q", warn)
		}
	}
}

func TestACrossModelEmbeddingFailoverWarns(t *testing.T) {
	// Spec §8: the vectors come from a different vector space and nothing in
	// the body says so. The warning on the request row is the only record.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(embedUpstream())
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "e5-small", good.URL, "e5-large", ir.SurfaceEmbedding)
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"embed","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	var found string
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "vector space") {
			found = warn
		}
	}
	if found == "" {
		t.Fatalf("no cross-model warning in %v", got.Warnings)
	}
	if !strings.Contains(found, "e5-small") || !strings.Contains(found, "e5-large") {
		t.Errorf("warning = %q; it must name both models or it cannot be acted on", found)
	}
	if w.Header().Get("X-Darkrouter-Model") != "e5-large" {
		t.Errorf("X-Darkrouter-Model = %q, want the model that actually served",
			w.Header().Get("X-Darkrouter-Model"))
	}
}

func TestAnEmbeddingRequestWithNoEmbeddingProviderIsRefused(t *testing.T) {
	// Spec §10's third per-surface case: nothing is attempted and the error
	// names the fact.
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"chat-only","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "surface") {
		t.Errorf("body = %s; the error must name the surface as the reason", w.Body.String())
	}
	got := rec.only(t)
	if len(got.Attempts) != 0 {
		t.Errorf("attempts = %d; a surface no provider offers must attempt nothing", len(got.Attempts))
	}
}

// The chat route refuses a compressed body before parsing it; the auxiliary
// routes share that refusal rather than each answering a different 400.
func TestACompressedEmbeddingBodyIsRefusedLikeChat(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	r := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"m","input":"x"}`))
	r.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	e.HandleEmbeddings(rec, r, openaiedge.New())
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code = %d body = %s, want 415", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "content-encoding is not supported") {
		t.Errorf("body = %s", rec.Body.String())
	}
	if r := logger.only(t); r.ErrorCode != string(ir.ErrUnsupportedMedia) {
		t.Errorf("ErrorCode = %q; the refusal must still be logged", r.ErrorCode)
	}
}
