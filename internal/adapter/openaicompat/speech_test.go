package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildSpeechRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildSpeech(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "tts-1"},
		&ir.SpeechRequest{
			Input: "hello", Voice: "alloy", ResponseFormat: "mp3",
			Speed: 1.25, Instructions: "cheerful", StreamFormat: "sse",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/audio/speech" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "tts-1" || body["input"] != "hello" || body["voice"] != "alloy" {
		t.Errorf("body = %v", body)
	}
	if body["response_format"] != "mp3" || body["speed"].(float64) != 1.25 {
		t.Errorf("body = %v", body)
	}
	if body["instructions"] != "cheerful" || body["stream_format"] != "sse" {
		t.Errorf("body = %v", body)
	}
}

func TestBuildSpeechOmitsUnsetOptionals(t *testing.T) {
	// speed 0 is not "default", it is a 400.
	hr, _, err := New().BuildSpeech(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "tts-1"},
		&ir.SpeechRequest{Input: "hi", Voice: "alloy"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	for _, k := range []string{"speed", "response_format", "instructions", "stream_format"} {
		if _, present := body[k]; present {
			t.Errorf("an unset %q was sent as %v", k, body[k])
		}
	}
}
