package catalog

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestBuildCapabilityRequestTargetsApiShow(t *testing.T) {
	// /api/show sits beside the OpenAI-compatible /v1, not under it.
	r, err := BuildCapabilityRequest(context.Background(),
		Probe{Kind: "openaicompat", BaseURL: "http://localhost:11434/v1", AuthStyle: "none"}, "llama3.3:70b")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.String() != "http://localhost:11434/api/show" {
		t.Errorf("url = %s", r.URL)
	}
	if r.Method != "POST" {
		t.Errorf("method = %s, want POST", r.Method)
	}
	body, _ := io.ReadAll(r.Body)
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	// The tag must survive verbatim: normalizing it here would ask Ollama
	// about a model it does not have.
	if payload["model"] != "llama3.3:70b" {
		t.Errorf("payload = %v", payload)
	}
}

func TestParseOllamaShowReadsTheCapabilitiesArray(t *testing.T) {
	caps, ok := ParseOllamaShow([]byte(`{"capabilities":["completion","tools","vision"]}`))
	if !ok {
		t.Fatal("reported no capabilities")
	}
	if !caps.Tools || !caps.Vision {
		t.Errorf("caps = %+v", caps)
	}
	if caps.Reasoning {
		t.Error("reasoning invented from a runtime that did not report it")
	}
}

func TestParseOllamaShowReadsThinkingAsReasoning(t *testing.T) {
	caps, ok := ParseOllamaShow([]byte(`{"capabilities":["completion","thinking"]}`))
	if !ok || !caps.Reasoning {
		t.Errorf("caps = %+v, ok = %v", caps, ok)
	}
}

func TestParseOllamaShowFallsBackToTheTemplate(t *testing.T) {
	// Older builds report no capabilities array; the template is the only
	// evidence, and spec §6 describes exactly that signal.
	caps, ok := ParseOllamaShow([]byte(`{"template":"{{ if .Tools }}{{ range .Tools }}{{ end }}{{ end }}"}`))
	if !ok || !caps.Tools {
		t.Errorf("caps = %+v, ok = %v", caps, ok)
	}
	plain, ok := ParseOllamaShow([]byte(`{"template":"{{ .System }}{{ .Prompt }}"}`))
	if !ok {
		t.Fatal("a template with no tools reported nothing at all")
	}
	if plain.Tools {
		t.Error("tools claimed for a template that does not mention them")
	}
}

func TestParseOllamaShowReportsNothingWhenItKnowsNothing(t *testing.T) {
	// A response with neither signal must report "did not say" rather than a
	// confident all-false, which would overwrite what models.dev knows.
	for _, body := range []string{`{}`, `{"details":{}}`, "", "not json"} {
		if _, ok := ParseOllamaShow([]byte(body)); ok {
			t.Errorf("%q reported capabilities", body)
		}
	}
}
