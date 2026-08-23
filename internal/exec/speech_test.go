package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// watchWriter reports each Write as it happens, so a test can prove bytes
// reached the client before the upstream finished sending.
type watchWriter struct {
	http.ResponseWriter
	writes chan []byte
}

func (w *watchWriter) Write(b []byte) (int, error) {
	cp := append([]byte(nil), b...)
	select {
	case w.writes <- cp:
	default:
	}
	return w.ResponseWriter.Write(b)
}

func (w *watchWriter) Flush() {}

func speechRequest() *http.Request {
	return httptest.NewRequest("POST", "/v1/audio/speech",
		strings.NewReader(`{"model":"tts-1","input":"hello","voice":"alloy"}`))
}

func TestSpeechIsNeverHeldWhole(t *testing.T) {
	// The upstream refuses to send its second chunk until the first has reached
	// the client. An implementation that buffers the audio deadlocks here
	// instead of passing, which is the enforceable form of "never captured".
	gotFirst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("FIRST"))
		w.(http.Flusher).Flush()
		select {
		case <-gotFirst:
		case <-time.After(5 * time.Second):
			t.Error("the first chunk never reached the client; the body was buffered")
		}
		_, _ = w.Write([]byte("SECOND"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	inner := httptest.NewRecorder()
	ww := &watchWriter{ResponseWriter: inner, writes: make(chan []byte, 8)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.HandleSpeech(ww, speechRequest(), openaiedge.New())
	}()

	select {
	case b := <-ww.writes:
		if string(b) != "FIRST" {
			t.Errorf("first chunk = %q", b)
		}
		close(gotFirst)
	case <-time.After(5 * time.Second):
		t.Fatal("no bytes reached the client before the upstream finished")
	}
	<-done

	if got := inner.Body.String(); got != "FIRSTSECOND" {
		t.Errorf("body = %q", got)
	}
	if ct := inner.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("content-type = %q; the upstream's was not preserved", ct)
	}
	got := rec.only(t)
	if got.Surface != "tts" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
}

func TestSpeechForwardsAnSSEBodyUnchanged(t *testing.T) {
	// stream_format: "sse" changes nothing in the op: whatever arrives is
	// forwarded with a flush per chunk.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"speech.audio.delta\",\"audio\":\"AA==\"}\n\n"))
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	w := httptest.NewRecorder()
	e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(
		`{"model":"tts-1","input":"hi","voice":"alloy","stream_format":"sse"}`)), openaiedge.New())

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "speech.audio.delta") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestSpeechFailsOverBeforeTheFirstByte(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("AUDIO"))
	}))
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "tts-1", good.URL, "tts-1", ir.SurfaceTTS)
	w := httptest.NewRecorder()
	e.HandleSpeech(w, speechRequest(), openaiedge.New())

	if w.Code != http.StatusOK || w.Body.String() != "AUDIO" {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestASpeechRequestWithNoTTSProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(
		`{"model":"chat-only","input":"hi","voice":"alloy"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}

func TestAMalformedSpeechBodyIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	for _, body := range []string{`{"model":"tts-1","voice":"alloy"}`, `{"model":"tts-1","input":"hi"}`} {
		w := httptest.NewRecorder()
		e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech",
			strings.NewReader(body)), openaiedge.New())
		if w.Code != http.StatusBadRequest {
			t.Errorf("HandleSpeech(%s) status = %d, want 400", body, w.Code)
		}
	}
}
