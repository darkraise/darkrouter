package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
)

// batchingAdapter is an embedder whose upstream takes at most size inputs per
// request.
type batchingAdapter struct {
	*openaicompat.Adapter
	size int
}

func (b batchingAdapter) EmbeddingBatches(_ *adapter.Target, req *ir.EmbeddingRequest) []int {
	var out []int
	for left := req.InputCount(); left > 0; left -= b.size {
		out = append(out, min(left, b.size))
	}
	return out
}

// letterUpstream embeds each input as its letter's position, so a response
// shows which input every vector came from, and records each call's inputs.
func letterUpstream(calls *[][]string, failCall int) http.HandlerFunc {
	var n atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		call := int(n.Add(1))
		var in struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		*calls = append(*calls, in.Input)
		if call == failCall {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		data := make([]string, len(in.Input))
		for i, s := range in.Input {
			data[i] = `{"object":"embedding","index":` + strconv.Itoa(i) +
				`,"embedding":[` + strconv.Itoa(int(s[0]-'a')+1) + `]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[` + strings.Join(data, ",") +
			`],"usage":{"prompt_tokens":3,"total_tokens":3}}`))
	}
}

func batchingExecutor(t *testing.T, url string, size int) (*Executor, *captureLogger) {
	t.Helper()
	rec := &captureLogger{}
	src := providertest.NewSource(providertest.Keyed("p", "probe", url, "sk", "e5"))
	e := executorFor(t, nil, src, map[string]adapter.Adapter{
		"probe": batchingAdapter{Adapter: openaicompat.New(), size: size},
	}, Deps{Log: rec, Catalog: catalogWith("p", "e5", ir.SurfaceEmbedding)})
	return e, rec
}

// An upstream that caps inputs per request is sent consecutive sub-batches,
// and the client receives one response in its own input order.
func TestEmbeddingsAreSplitToTheAdaptersBatchLimit(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(letterUpstream(&calls, 0))
	defer up.Close()

	e, rec := batchingExecutor(t, up.URL, 2)
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b","c","d","e"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got, _ := json.Marshal(calls); string(got) != `[["a","b"],["c","d"],["e"]]` {
		t.Errorf("upstream calls = %s, want three sub-batches in order", got)
	}
	var body struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 5 {
		t.Fatalf("data = %s", w.Body.String())
	}
	for i, d := range body.Data {
		if d.Index != i || d.Embedding[0] != float64(i+1) {
			t.Errorf("data[%d] = index %d vector %v, want index %d vector [%d]", i, d.Index, d.Embedding, i, i+1)
		}
	}
	got := rec.only(t)
	if got.Status != "success" || got.TokensIn != 9 {
		t.Errorf("record = status %q, tokens in %d; want success and the three batches' 9", got.Status, got.TokensIn)
	}
	if len(got.Attempts) != 1 {
		t.Errorf("attempts = %d, want the sub-batches inside one attempt", len(got.Attempts))
	}
}

// A sub-batch that fails fails the attempt before anything is written: half
// a batch of vectors is not a response.
func TestAFailedEmbeddingSubBatchFailsTheAttempt(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(letterUpstream(&calls, 2))
	defer up.Close()

	e, rec := batchingExecutor(t, up.URL, 2)
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b","c","d","e"]}`)), openaiedge.New())

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200 after a failed sub-batch: %s", w.Body.String())
	}
	if len(calls) != 2 {
		t.Errorf("upstream calls = %d, want the fan-out to stop at the failure", len(calls))
	}
	got := rec.only(t)
	if got.Status == "success" || len(got.Attempts) != 1 || got.Attempts[0].Outcome != "retryable_provider" {
		t.Errorf("record = status %q attempts %+v", got.Status, got.Attempts)
	}
}
