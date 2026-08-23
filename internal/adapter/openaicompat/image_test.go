package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildImageRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildImage(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "gpt-image-1"},
		&ir.ImageRequest{
			Prompt: "a cat", N: 2, Size: "1024x1024", Quality: "high",
			ResponseFormat: "b64_json", User: "u",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/images/generations" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-image-1" || body["prompt"] != "a cat" {
		t.Errorf("body = %v", body)
	}
	if body["n"].(float64) != 2 || body["response_format"] != "b64_json" {
		t.Errorf("body = %v", body)
	}
}

func TestBuildImageOmitsEveryUnsetOptional(t *testing.T) {
	// An explicit null or empty string is a 400 on several of these, and a
	// zero n asks for no images at all.
	hr, _, err := New().BuildImage(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "dall-e-3"},
		&ir.ImageRequest{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	for _, k := range []string{"n", "size", "quality", "style", "response_format",
		"background", "output_format", "moderation", "output_compression", "user"} {
		if _, present := body[k]; present {
			t.Errorf("an unset %q was sent as %v", k, body[k])
		}
	}
}

func TestParseImageReportsUsageWhenTheProviderDoes(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"created":17,"data":[{"b64_json":"aGk="}],
		  "usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}`))}
	out, err := New().ParseImage(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !out.UsageReported {
		t.Error("UsageReported = false on a response carrying usage")
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 22 {
		t.Errorf("usage = %+v", out.Usage)
	}
	if len(out.Images) != 1 || out.Images[0].Base64 != "aGk=" {
		t.Errorf("images = %+v", out.Images)
	}
}

func TestParseImageDistinguishesAbsentUsageFromZero(t *testing.T) {
	// The dall-e models report none. Zero tokens must mean "not reported", not
	// "free", or the cost column records a confident zero.
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"created":17,"data":[{"url":"https://example.invalid/a.png","revised_prompt":"r"}]}`))}
	out, err := New().ParseImage(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.UsageReported {
		t.Error("UsageReported = true on a response with no usage object")
	}
	if out.Images[0].URL == "" || out.Images[0].RevisedPrompt != "r" {
		t.Errorf("images = %+v", out.Images)
	}
}

func TestParseImageRejectsAnImagelessBody(t *testing.T) {
	resp := &http.Response{StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"created":1,"data":[]}`))}
	if _, err := New().ParseImage(resp); err == nil {
		t.Fatal("a 200 with no images parsed cleanly")
	}
}
