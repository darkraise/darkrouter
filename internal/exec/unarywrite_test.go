package exec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
)

// A client that hung up on a unary response did not receive it, as one that
// hung up on a stream did not. The row says cancelled, and the provider, which
// delivered, is not blamed for it.
func TestAUnaryResponseTheClientDidNotTakeIsCancelled(t *testing.T) {
	chatUp := unaryUpstream()
	defer chatUp.Close()

	for _, tc := range []struct {
		name     string
		upstream http.Handler
		preset   string
		cat      *catalog.Store
		serve    func(e *Executor, w http.ResponseWriter)
	}{
		{name: "forwarded chat", upstream: chatUp.Config.Handler,
			serve: func(e *Executor, w http.ResponseWriter) {
				e.Handle(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
					`{"model":"m","messages":[{"role":"user","content":"ping"}]}`)), openaiedge.New())
			}},
		{name: "translated chat", upstream: chatUp.Config.Handler,
			serve: func(e *Executor, w http.ResponseWriter) {
				e.Handle(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(
					`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`)),
					anthropicedge.New())
			}},
		{name: "embeddings", upstream: embedUpstream(), cat: catalogWith("p", "m", ir.SurfaceEmbedding),
			serve: func(e *Executor, w http.ResponseWriter) {
				e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
					strings.NewReader(`{"model":"m","input":["a"]}`)), openaiedge.New())
			}},
		{name: "transcription", cat: catalogWith("p", "m", ir.SurfaceSTT),
			upstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"text":"hello"}`))
			}),
			serve: func(e *Executor, w http.ResponseWriter) {
				e.HandleTranscriptions(w, transcriptionRequest(t, "m"), openaiedge.New())
			}},
		{name: "moderation", upstream: moderationUpstream(), cat: catalogWith("p", "m", ir.SurfaceModeration),
			serve: func(e *Executor, w http.ResponseWriter) {
				e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
					strings.NewReader(`{"model":"m","input":"hello"}`)), openaiedge.New())
			}},
		{name: "image", upstream: imageUpstream(true), cat: catalogWith("p", "m", ir.SurfaceImage),
			serve: func(e *Executor, w http.ResponseWriter) {
				e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
					strings.NewReader(`{"model":"m","prompt":"a cat","n":1}`)), openaiedge.New())
			}},
		{name: "rerank", upstream: rerankUpstream(nil), preset: "cohere", cat: catalogWith("p", "m", ir.SurfaceRerank),
			serve: func(e *Executor, w http.ResponseWriter) {
				e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
					`{"model":"m","query":"q","documents":["a","b"]}`)), openaiedge.New())
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(tc.upstream)
			defer up.Close()

			logger, h := &captureLogger{}, &captureHealth{}
			deps := Deps{Log: logger, Health: h}
			if tc.cat != nil {
				deps.Catalog = tc.cat
			}
			p := providertest.Keyed("p", "openaicompat", up.URL, "sk", "m")
			p.Preset = tc.preset
			e := executorFor(t, nil, providertest.NewSource(p),
				map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, deps)

			tc.serve(e, &failingWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("broken pipe")})

			got := logger.only(t)
			if got.Status != "cancelled" || got.ErrorCode != "" {
				t.Errorf("record = status %q error %q, want cancelled with no error code",
					got.Status, got.ErrorCode)
			}
			if _, sig := h.only(t); sig.Outcome != adapter.OutcomeClientCancelled {
				t.Errorf("breaker heard %q, want %q — the provider delivered",
					sig.Outcome, adapter.OutcomeClientCancelled)
			}
		})
	}
}
