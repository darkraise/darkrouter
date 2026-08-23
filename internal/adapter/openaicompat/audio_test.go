package openaicompat

import (
	"context"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildTranscriptionPostsTheFormVerbatim(t *testing.T) {
	body := []byte("--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--b--\r\n")
	hr, warns, err := New().BuildTranscription(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "whisper-1"},
		body, "multipart/form-data; boundary=b")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/audio/transcriptions" {
		t.Errorf("url = %s", hr.URL)
	}
	if got := hr.Header.Get("Content-Type"); got != "multipart/form-data; boundary=b" {
		t.Errorf("content-type = %q; the boundary must be the rendered form's", got)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}
	sent, _ := io.ReadAll(hr.Body)
	if string(sent) != string(body) {
		t.Errorf("body was altered: %q", sent)
	}
	if hr.ContentLength != int64(len(body)) {
		t.Errorf("content-length = %d, want %d", hr.ContentLength, len(body))
	}
}

func TestBuildTranscriptionIsReplayable(t *testing.T) {
	// The transport retries by calling GetBody. Without it a retried upload is
	// sent empty and the provider reports an unreadable file.
	hr, _, err := New().BuildTranscription(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		[]byte("AUDIO"), "multipart/form-data; boundary=b")
	if err != nil {
		t.Fatal(err)
	}
	if hr.GetBody == nil {
		t.Fatal("GetBody is nil")
	}
	again, err := hr.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(again)
	if string(got) != "AUDIO" {
		t.Errorf("replayed body = %q", got)
	}
}
