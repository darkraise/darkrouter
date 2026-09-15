package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
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

func batchingExecutor(t *testing.T, url string, size int, tune func(*config.Config)) (*Executor, *captureLogger) {
	t.Helper()
	rec := &captureLogger{}
	src := providertest.NewSource(providertest.Keyed("p", "probe", url, "sk", "e5"))
	e := executorFor(t, tune, src, map[string]adapter.Adapter{
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

	e, rec := batchingExecutor(t, up.URL, 2, nil)
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

	e, rec := batchingExecutor(t, up.URL, 2, nil)
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

// The attempt row names the status that failed the attempt. The first
// sub-batch's 200 describes a response that was never served.
func TestAFailedEmbeddingSubBatchRecordsItsStatus(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(letterUpstream(&calls, 2))
	defer up.Close()

	e, rec := batchingExecutor(t, up.URL, 2, nil)
	if w := embedFive(e); w.Code == http.StatusOK {
		t.Fatalf("status = 200 after a failed sub-batch: %s", w.Body.String())
	}
	got := rec.only(t)
	if len(got.Attempts) != 1 || got.Attempts[0].StatusCode != http.StatusInternalServerError {
		t.Errorf("attempts = %+v; want the failing sub-batch's 500", got.Attempts)
	}
}

// delayed holds every response back for d before its headers go out.
func delayed(d time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(d)
		next(w, r)
	}
}

func embedFive(e *Executor) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b","c","d","e"]}`)), openaiedge.New())
	return w
}

// A later sub-batch is a send like the first one, so its wait for headers is
// bounded by first_byte. idle bounds a gap inside a body and can be far
// shorter than a provider takes to start answering.
func TestAnEmbeddingSubBatchWaitsFirstByteForItsHeaders(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(delayed(300*time.Millisecond, letterUpstream(&calls, 0)))
	defer up.Close()

	e, _ := batchingExecutor(t, up.URL, 2, func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = 2 * time.Second
		c.Policy.Timeout.Total = 10 * time.Second
		c.Policy.Timeout.Idle = 100 * time.Millisecond
	})
	if w := embedFive(e); w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s; a sub-batch was cut at idle while inside first_byte",
			w.Code, w.Body.String())
	}
}

// total bounds the attempt, not each sub-batch: sub-batches that each answer
// inside first_byte must still not run the request past total between them.
func TestEmbeddingSubBatchesShareTheAttemptsTotal(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(delayed(250*time.Millisecond, letterUpstream(&calls, 0)))
	defer up.Close()

	e, _ := batchingExecutor(t, up.URL, 1, func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = 400 * time.Millisecond
		c.Policy.Timeout.Total = 700 * time.Millisecond
		c.Policy.Timeout.Idle = 2 * time.Second
	})
	start := time.Now()
	w := embedFive(e)
	if took := time.Since(start); w.Code == http.StatusOK || took > 1100*time.Millisecond {
		t.Fatalf("status = %d after %v; five 250ms sub-batches must be cut at the 700ms total",
			w.Code, took)
	}
}

// The attempt row's latency covers every sub-batch the attempt sent, not only
// the first, or a split request reads as fast as its first slice.
func TestEmbeddingAttemptLatencyCoversEverySubBatch(t *testing.T) {
	var calls [][]string
	up := httptest.NewServer(delayed(80*time.Millisecond, letterUpstream(&calls, 0)))
	defer up.Close()

	e, rec := batchingExecutor(t, up.URL, 2, nil)
	if w := embedFive(e); w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	if len(got.Attempts) != 1 || got.Attempts[0].LatencyMs < 3*80 {
		t.Errorf("attempts = %+v; want one attempt whose latency spans all three sub-batches",
			got.Attempts)
	}
}
