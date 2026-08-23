package exec

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// transcriptionRequest builds a real multipart upload with the model field
// after the file part, which is where several clients put it.
func transcriptionRequest(t *testing.T, model string) *http.Request {
	t.Helper()
	body, ct := buildForm(t, [][2]string{{"model", model}, {"response_format", "json"}},
		[2]string{"a.mp3", "AUDIO"}, true)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	return r
}

func TestTranscriptionsServeJSON(t *testing.T) {
	var sawModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("upstream could not parse the form: %v", err)
		}
		sawModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello there","duration":2.5,
		  "usage":{"type":"tokens","input_tokens":7,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if sawModel != "whisper-1" {
		t.Errorf("upstream saw model %q", sawModel)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["text"] != "hello there" {
		t.Errorf("body = %s", w.Body.String())
	}
	if _, ok := body["duration"]; !ok {
		t.Error("duration was dropped; the body must be forwarded verbatim")
	}
	got := rec.only(t)
	if got.Surface != "stt" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 7 || got.TokensOut != 3 {
		t.Errorf("tokens = %d/%d", got.TokensIn, got.TokensOut)
	}
}

func TestTranscriptionsForwardPlainTextByContentType(t *testing.T) {
	// The route cannot tell srt from json; the response header can.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "1\n00:00:00,000 --> 00:00:02,500\nhello there\n")
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("content-type = %q; the upstream's was not preserved", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "00:00:02,500") {
		t.Errorf("body = %q; a subtitle body was not forwarded intact", w.Body.String())
	}
}

func TestTranscriptionsForwardSSEByContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"hel\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hello\"}\n\n")
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "transcript.text.delta") ||
		!strings.Contains(w.Body.String(), "transcript.text.done") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestATranscriptionSurvivesAFirstProviderFailure(t *testing.T) {
	// The done criterion: the buffered body is replayed against a second
	// provider whose model name differs, and the form carries the new name.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	var sawModel, sawFile string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("replayed form did not parse: %v", err)
		}
		sawModel = r.FormValue("model")
		f, _, err := r.FormFile("file")
		if err == nil {
			b, _ := io.ReadAll(f)
			sawFile = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	defer good.Close()

	e, rec := failoverPair(t, bad.URL, "whisper-1", good.URL, "distil-whisper", ir.SurfaceSTT)
	w := httptest.NewRecorder()
	body, ct := buildForm(t, [][2]string{{"model", "embed"}}, [2]string{"a.mp3", "AUDIO"}, true)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	e.HandleTranscriptions(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("the failing provider was called %d times", hits.Load())
	}
	if sawModel != "distil-whisper" {
		t.Errorf("replayed model = %q; the in-form name was not rewritten for the second target", sawModel)
	}
	if sawFile != "AUDIO" {
		t.Errorf("replayed file = %q; the upload did not survive the replay", sawFile)
	}
	if got := rec.only(t); len(got.Attempts) != 2 {
		t.Errorf("attempts = %d", len(got.Attempts))
	}
}

func TestAnOversizedUploadIsRefusedBeforeAnyUpstreamCall(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer upstream.Close()

	e, rec := executorForCapped(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT), 512)
	w := httptest.NewRecorder()
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}},
		[2]string{"a.mp3", strings.Repeat("A", 4096)}, false)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	e.HandleTranscriptions(w, r, openaiedge.New())

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("an oversized upload reached an upstream %d times", hits.Load())
	}
	if got := rec.only(t); got.ErrorCode != string(ir.ErrPayloadTooLarge) {
		t.Errorf("error code = %q", got.ErrorCode)
	}
}

func TestATranscriptionWithNoSTTProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "chat-only"), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
