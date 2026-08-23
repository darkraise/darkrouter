package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

func TestEmbeddingsRecordTheirInputCount(t *testing.T) {
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "e5", ir.SurfaceEmbedding))
	e.HandleEmbeddings(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b","c"],"dimensions":256,"encoding_format":"base64"}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["input_count"] != 3 || got["dimensions"] != 256 || got["encoding"] != "base64" {
		t.Errorf("surface meta = %v", got)
	}
}

func TestImagesRecordTheirCountAndSize(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-image-1", ir.SurfaceImage))
	e.HandleImages(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(
			`{"model":"gpt-image-1","prompt":"a cat","n":2,"size":"1024x1024","quality":"high"}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["image_count"] != 2 || got["size"] != "1024x1024" || got["quality"] != "high" {
		t.Errorf("surface meta = %v", got)
	}
}

func TestRerankRecordsItsDocumentCount(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.HandleRerank(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/rerank",
		strings.NewReader(`{"model":"rerank-v3.5","query":"q","documents":["a","b"],"top_n":1}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["document_count"] != 2 || got["top_n"] != 1 {
		t.Errorf("surface meta = %v", got)
	}
}

func TestModerationsRecordTheirFlaggedCount(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "omni", ir.SurfaceModeration))
	e.HandleModerations(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":["a","b"]}`)), openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["input_count"] != 2 || got["flagged_count"] != 0 {
		t.Errorf("surface meta = %v", got)
	}
}

func TestSpeechRecordsWhatActuallyReachedTheClient(t *testing.T) {
	// Not the provider's Content-Length. A truncated body has a lower count,
	// and spec §7 makes this the only place that appears.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	e.HandleSpeech(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/audio/speech",
		strings.NewReader(`{"model":"tts-1","input":"hi","voice":"alloy","response_format":"mp3"}`)),
		openaiedge.New())

	got := rec.only(t)
	if got.ResponseBytes != 10 {
		t.Errorf("response bytes = %d, want 10", got.ResponseBytes)
	}
	if got.ResponseContentType != "audio/mpeg" {
		t.Errorf("content type = %q", got.ResponseContentType)
	}
	if got.SurfaceMeta["voice"] != "alloy" || got.SurfaceMeta["response_format"] != "mp3" {
		t.Errorf("surface meta = %v", got.SurfaceMeta)
	}
}

func TestTranscriptionsRecordTheirContentTypeAndSize(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("hello there"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	e.HandleTranscriptions(httptest.NewRecorder(), transcriptionRequest(t, "whisper-1"), openaiedge.New())

	got := rec.only(t)
	if got.ResponseBytes != 11 {
		t.Errorf("response bytes = %d, want 11", got.ResponseBytes)
	}
	if !strings.HasPrefix(got.ResponseContentType, "text/plain") {
		t.Errorf("content type = %q", got.ResponseContentType)
	}
	if got.SurfaceMeta["file_name"] != "a.mp3" {
		t.Errorf("surface meta = %v", got.SurfaceMeta)
	}
}

func TestAChatRequestRecordsNoSurfaceDetail(t *testing.T) {
	// The column defaults to {}. A chat row inventing keys would make the
	// trace view show fields that mean nothing for that surface.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "m", ir.SurfaceLLM))
	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceLLM}}
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op, e.store.Current())

	if got := rec.only(t).SurfaceMeta; len(got) != 0 {
		t.Errorf("surface meta = %v, want empty", got)
	}
}
