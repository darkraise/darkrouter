package exec

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
)

// A query-param key is part of the request URL, and a transport failure quotes
// that URL in full. The text reaches the attempt row, the log and the client
// holding a proxy token, none of whom may see the provider's key.
func TestATransportFailureDoesNotCarryAQueryParamKey(t *testing.T) {
	const secret = "sk-live/secret+value="
	// dropAfter answers the first n calls and then closes the connection
	// without a response, which fails the send itself.
	dropAfter := func(n int64, sawKey *atomic.Bool, answer http.HandlerFunc) http.HandlerFunc {
		var calls atomic.Int64
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("key") == secret {
				sawKey.Store(true)
			}
			if calls.Add(1) <= n {
				answer(w, r)
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
	}

	for _, tc := range []struct {
		name string
		// answered is how many calls succeed before the connection drops.
		answered int64
		serve    func(t *testing.T, base string) (*httptest.ResponseRecorder, *captureLogger)
	}{
		{"completion", 0, func(t *testing.T, base string) (*httptest.ResponseRecorder, *captureLogger) {
			p := providertest.Keyed("p", "openaicompat", base, secret, "m")
			p.AuthStyle = "query-param"
			logger := &captureLogger{}
			e := executorFor(t, nil, providertest.NewSource(p),
				map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Log: logger})
			return post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`), logger
		}},
		{"embedding sub-batch", 1, func(t *testing.T, base string) (*httptest.ResponseRecorder, *captureLogger) {
			p := providertest.Keyed("p", "probe", base, secret, "e5")
			p.AuthStyle = "query-param"
			logger := &captureLogger{}
			e := executorFor(t, nil, providertest.NewSource(p), map[string]adapter.Adapter{
				"probe": batchingAdapter{Adapter: openaicompat.New(), size: 1},
			}, Deps{Log: logger, Catalog: catalogWith("p", "e5", ir.SurfaceEmbedding)})
			w := httptest.NewRecorder()
			e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
				strings.NewReader(`{"model":"e5","input":["a","b"]}`)), openaiedge.New())
			return w, logger
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sawKey atomic.Bool
			var calls [][]string
			up := httptest.NewServer(dropAfter(tc.answered, &sawKey, letterUpstream(&calls, 0)))
			defer up.Close()

			w, logger := tc.serve(t, up.URL)
			if !sawKey.Load() {
				t.Fatal("the key was not sent as a query parameter, so this proves nothing")
			}
			r := logger.only(t)
			var texts []string
			for _, a := range r.Attempts {
				texts = append(texts, a.Error)
			}
			if !strings.Contains(strings.Join(texts, "\n"), "EOF") {
				t.Fatalf("attempt errors = %q, want the transport failure recorded", texts)
			}
			texts = append(texts, w.Body.String())
			for _, text := range texts {
				for _, form := range []string{secret, url.QueryEscape(secret), "secret"} {
					if strings.Contains(text, form) {
						t.Errorf("%q carries the key as %q", text, form)
					}
				}
			}
		})
	}
}
