package main

import (
	"fmt"
	"testing"
)

// Every other 9router test builds nineEntry as a Go struct literal, which
// never exercises a single `json:"…"` tag. That is how transport.authHeader --
// a field no upstream entry carries -- sat in the decoder unnoticed while the
// suite stayed green.
//
// These are byte-for-byte copies of five upstream files at 9router 699edac3,
// chosen for the shapes that decide what darkrouter does with an entry: a
// plain API-key provider, a non-openai wire format, an oauth category, and
// both shapes of the transport.auth header block. shared.js sits beside the
// registry directory exactly as upstream has it, because gemini.js imports
// from there.
//
// Refresh them from upstream when the schema moves; a failure here means a
// struct tag no longer names a field the registry publishes.
func TestGoldenSampleDecodesTheRealSchema(t *testing.T) {
	got, err := scrapeNineRouter("testdata/ninerouter/registry")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]nineEntry{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if len(byID) != 5 {
		t.Fatalf("decoded %d entries, want 5: %v", len(byID), byID)
	}

	for _, tc := range []struct {
		id   string
		want nineEntry
	}{{
		// A plain API-key provider: no authType, no transport.auth, no format.
		id: "cerebras",
		want: nineEntry{
			ID: "cerebras", Alias: "cerebras", Category: "apikey",
			Display: nineDisplay{
				Name: "Cerebras", Color: "#FF4F00", TextIcon: "CB",
				Website: "https://www.cerebras.ai",
				Notice:  nineNotice{APIKeyURL: "https://cloud.cerebras.ai/platform"},
			},
			Transport: nineTransport{
				BaseURL:     "https://api.cerebras.ai/v1/chat/completions",
				ValidateURL: "https://api.cerebras.ai/v1/models",
				Quirks:      map[string]bool{"dropClientMetadata": true},
			},
		},
	}, {
		// A non-openai wire format, which the gate must see to drop the entry.
		id: "anthropic",
		want: nineEntry{
			ID: "anthropic", Alias: "anthropic", Category: "apikey",
			Display: nineDisplay{
				Name: "Anthropic", Color: "#D97757", TextIcon: "AN",
				Website: "https://console.anthropic.com",
				Notice:  nineNotice{APIKeyURL: "https://console.anthropic.com/settings/keys"},
			},
			Transport: nineTransport{
				BaseURL: "https://api.anthropic.com/v1/messages",
				Format:  "claude",
			},
			ServiceKinds: []string{"llm", "imageToText"},
		},
	}, {
		// The flat transport.auth shape, on the one surviving entry that has it.
		id: "kimchi",
		want: nineEntry{
			ID: "kimchi", Alias: "kimchi", Category: "freeTier",
			Display: nineDisplay{
				Name: "Kimchi", Color: "#FF521D", TextIcon: "KC",
				Website: "https://kimchi.dev",
			},
			Transport: nineTransport{
				BaseURL: "https://llm.kimchi.dev/openai/v1/chat/completions",
				Format:  "openai",
				Auth:    nineAuth{Header: "Authorization"},
			},
			ServiceKinds: []string{"llm", "imageToText"},
		},
	}, {
		// category: "oauth" is what gates the credential flow, and it carries
		// no authType at all -- the field the merge once keyed on.
		id: "codebuddy-cn",
		want: nineEntry{
			ID: "codebuddy-cn", Alias: "cbcn", Category: "oauth",
			Display: nineDisplay{
				Name: "CodeBuddy CN", Color: "#006EFF",
				Website: "https://copilot.tencent.com",
			},
			Transport: nineTransport{
				BaseURL: "https://copilot.tencent.com/v2/chat/completions",
				Auth:    nineAuth{Header: "Authorization"},
			},
		},
	}, {
		// The nested transport.auth shape: the API key and the OAuth token
		// travel in different headers, and only the API-key one is ours.
		id: "gemini",
		want: nineEntry{
			ID: "gemini", Alias: "gemini", Category: "freeTier", AuthType: "apikey",
			Display: nineDisplay{
				Name: "Gemini", Color: "#4285F4", TextIcon: "GE",
				Website: "https://ai.google.dev",
				Notice:  nineNotice{APIKeyURL: "https://aistudio.google.com/app/apikey"},
			},
			Transport: nineTransport{
				BaseURL: "https://generativelanguage.googleapis.com/v1beta/models",
				Format:  "gemini",
				Auth:    nineAuth{APIKey: nineAuthKind{Header: "x-goog-api-key"}},
			},
			ServiceKinds: []string{"llm", "embedding", "image", "imageToText", "webSearch", "tts", "stt"},
		},
	}} {
		t.Run(tc.id, func(t *testing.T) {
			e := byID[tc.id]
			// Models are long and volatile; the schema question is the shape.
			e.Models = nil
			if diff := diffEntry(e, tc.want); diff != "" {
				t.Errorf("decoded %s does not match the file:\n%s", tc.id, diff)
			}
		})
	}
}

// The header each sample's credential actually travels in, read through the
// same path toPreset uses.
func TestGoldenSampleAuthHeaderResolves(t *testing.T) {
	got, err := scrapeNineRouter("testdata/ninerouter/registry")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"cerebras":     "",
		"anthropic":    "",
		"kimchi":       "Authorization",
		"codebuddy-cn": "Authorization",
		"gemini":       "x-goog-api-key",
	}
	for _, e := range got {
		if h := e.Transport.Auth.apiKeyHeader(); h != want[e.ID] {
			t.Errorf("%s apiKeyHeader = %q, want %q", e.ID, h, want[e.ID])
		}
	}
}

func diffEntry(got, want nineEntry) string {
	if fmt.Sprintf("%#v", got) == fmt.Sprintf("%#v", want) {
		return ""
	}
	return fmt.Sprintf("got:  %#v\nwant: %#v", got, want)
}
