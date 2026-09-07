package exec

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/provider"
)

func capturingExecutor(t *testing.T, upstream string, maxBytes int) (*Executor, *captureLogger) {
	t.Helper()
	cfgStore := config.NewStoreOf(testConfig(t, func(c *config.Config) {
		c.Capture.Bodies = true
		c.Capture.MaxBytes = int64(maxBytes)
		c.Capture.Retention = time.Hour
		c.Providers = []config.ProviderConfig{{
			ID: "groq", Kind: "openaicompat", BaseURL: upstream,
			APIKey: "sk", Models: []string{"m"},
		}}
	}))
	log := &captureLogger{}
	e := New(cfgStore, provider.NewYAMLSource(cfgStore),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Log: log})
	return e, log
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCaptureStoresTextBodiesWhenEnabled(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer up.Close()
	e, log := capturingExecutor(t, up.URL, 4096)

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"ping"}]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.Handle(w, r, openaiedge.New())
	if w.Code != 200 {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	rec := log.only(t)
	if !strings.Contains(rec.RequestBody, `"ping"`) {
		t.Errorf("request body = %q, want the prompt", rec.RequestBody)
	}
	if !strings.Contains(rec.ResponseBody, "pong") {
		t.Errorf("response body = %q, want the answer", rec.ResponseBody)
	}
	if rec.BodiesExpireAt.IsZero() {
		t.Error("bodies carry no expiry")
	}
}

func TestCaptureTruncatesAtTheCapAndSkipsBinary(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer up.Close()
	e, log := capturingExecutor(t, up.URL, 8)

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"ping"}]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.Handle(w, r, openaiedge.New())
	rec := log.only(t)
	if !strings.HasSuffix(rec.RequestBody, "truncated at capture.max_bytes") || !strings.HasPrefix(rec.RequestBody, `{"model"`) {
		t.Errorf("request body = %q, want the first 8 bytes and a truncation note", rec.RequestBody)
	}

	c := newBodyCapture(config.CaptureConfig{MaxBytes: 64, Retention: 1})
	rw := c.arm(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader("x")))
	rw.Header().Set("Content-Type", "audio/mpeg")
	rw.WriteHeader(200)
	_, _ = rw.Write([]byte{0, 1, 2, 3})
	if got := c.response.text(); got != "" {
		t.Errorf("audio response captured as %q; binary bodies are never held", got)
	}
}
