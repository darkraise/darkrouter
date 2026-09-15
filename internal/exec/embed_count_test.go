package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Every vector answers one input by position. A provider that returns fewer
// vectors than the request carried leaves the client unable to tell which
// input went unembedded, so the response is a provider fault, not a success.
func TestAnEmbeddingResponseShortOfTheInputsIsNotServed(t *testing.T) {
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","model":"e5",` +
			`"data":[{"object":"embedding","index":0,"embedding":[0.5,0.25]}],` +
			`"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}))
	defer short.Close()

	e, rec := executorForOp(t, short.URL, catalogWith("p", "e5", ir.SurfaceEmbedding))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b"]}`)), openaiedge.New())

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200 for one vector against two inputs: %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Status == "success" {
		t.Errorf("record status = %q", got.Status)
	}
	if n := len(got.Attempts); n != 1 || got.Attempts[0].Outcome != "retryable_provider" {
		t.Errorf("attempts = %+v, want one retryable_provider attempt", got.Attempts)
	}
}
