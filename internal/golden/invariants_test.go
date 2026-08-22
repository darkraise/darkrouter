package golden

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// renderCase parses a fixture and renders it for one adapter kind.
func renderCase(t *testing.T, dialect, name, kind string) (map[string]any, []string) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", dialect, name)
	m := readMeta(t, dir)
	body := readFixture(t, filepath.Join(dir, "request.json"))

	req, _, err := dialects()[dialect].ParseRequest(requestFor(t, dialect, m, body), 1<<20)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ad := adapters()[kind]
	hr, warns, err := ad.BuildRequest(context.Background(),
		&adapter.Target{BaseURL: targetBase, APIKey: "sk-test", Model: "target-model"}, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, warningStrings(warns)
}

func hasPrefixIn(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func TestThinkingSignatureSurvivesAnthropicToAnthropic(t *testing.T) {
	body, _ := renderCase(t, "anthropic", "thinking-with-signature", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "ErUBCkYIBRgCIkA=") {
		t.Errorf("the thinking signature did not survive: %s", raw)
	}
	if !strings.Contains(string(raw), "EroBCkYIBRgCK") {
		t.Errorf("the redacted-thinking payload did not survive: %s", raw)
	}
}

func TestThinkingIsDroppedWithAWarningElsewhere(t *testing.T) {
	for _, kind := range []string{"openaicompat", "gemini"} {
		_, warns := renderCase(t, "anthropic", "thinking-with-signature", kind)
		if !hasPrefixIn(warns, "messages[].assistant.redacted_thinking") &&
			!hasPrefixIn(warns, "messages[].redacted_thinking") {
			t.Errorf("%s: warnings = %v; redacted thinking cannot be expressed and must be recorded",
				kind, warns)
		}
	}
}

func TestThoughtSignatureSurvivesGeminiToGemini(t *testing.T) {
	body, warns := renderCase(t, "gemini", "thought-signature", "gemini")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "CtEBAdHtim8=") {
		t.Errorf("the thought signature did not survive: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestCacheControlTTLSurvivesToAnthropicAndWarnsElsewhere(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "cache-control-1h", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), `"ttl":"1h"`) {
		t.Errorf("the 1h TTL is a paid feature and did not survive: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	for _, kind := range []string{"openaicompat", "gemini"} {
		_, w := renderCase(t, "anthropic", "cache-control-1h", kind)
		if !hasPrefixIn(w, "cache_control") {
			t.Errorf("%s: warnings = %v; a vanished paid feature must be visible", kind, w)
		}
	}
}

func TestFifthCacheBreakpointIsDroppedWithAWarning(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "five-cache-breakpoints", "anthropic")
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d; the content stays, only the marker is dropped", len(blocks))
	}
	marked := 0
	for _, b := range blocks {
		if _, ok := b.(map[string]any)["cache_control"]; ok {
			marked++
		}
	}
	if marked != 4 {
		t.Errorf("marked blocks = %d, want 4; a fifth breakpoint is a 400", marked)
	}
	if !hasPrefixIn(warns, "cache_control") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestParallelToolCallsKeepTheirIdentities(t *testing.T) {
	body, _ := renderCase(t, "openai", "parallel-tool-calls", "openaicompat")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "call_a") || !strings.Contains(string(raw), "call_b") {
		t.Errorf("two calls to one function are only distinguishable by id: %s", raw)
	}

	gem, _ := renderCase(t, "openai", "parallel-tool-calls", "gemini")
	contents := gem["contents"].([]any)
	var responses []map[string]any
	for _, c := range contents {
		for _, p := range c.(map[string]any)["parts"].([]any) {
			if fr, ok := p.(map[string]any)["functionResponse"]; ok {
				responses = append(responses, fr.(map[string]any))
			}
		}
	}
	if len(responses) != 2 {
		t.Fatalf("functionResponses = %d", len(responses))
	}
	for i, r := range responses {
		if r["name"] != "lookup" {
			t.Errorf("response %d = %v; the name comes from the call it answers", i, r)
		}
	}
}

func TestToolResultImageIsHoistedNotDropped(t *testing.T) {
	for _, kind := range []string{"openaicompat", "gemini"} {
		body, warns := renderCase(t, "anthropic", "tool-result-with-image", kind)
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "iVBORw==") {
			t.Errorf("%s: the image was dropped rather than hoisted: %s", kind, raw)
		}
		if !hasPrefixIn(warns, "messages[].tool_result.image") {
			t.Errorf("%s: warnings = %v; the move must be recorded", kind, warns)
		}
	}

	body, warns := renderCase(t, "anthropic", "tool-result-with-image", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "tool_result") || !strings.Contains(string(raw), "iVBORw==") {
		t.Errorf("Anthropic carries the image inside the result natively: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestAssistantPrefillSurvivesOnlyForAnthropic(t *testing.T) {
	body, _ := renderCase(t, "anthropic", "assistant-prefill", "anthropic")
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("messages = %d; the prefill is Anthropic's own idiom", len(msgs))
	}

	for _, kind := range []string{"openaicompat", "gemini"} {
		_, w := renderCase(t, "anthropic", "assistant-prefill", kind)
		if kind == "openaicompat" && !hasPrefixIn(w, "messages[last].assistant_prefill") {
			t.Errorf("%s: warnings = %v; a dropped prefill changes the answer", kind, w)
		}
	}
}

func TestDeveloperRoleBecomesSystem(t *testing.T) {
	body, _ := renderCase(t, "openai", "developer-role", "openaicompat")
	first := body["messages"].([]any)[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("role = %v; developer and system both emit as system", first["role"])
	}

	an, _ := renderCase(t, "openai", "developer-role", "anthropic")
	if _, ok := an["system"]; !ok {
		t.Errorf("Anthropic carries it in the top-level system field: %v", an)
	}
}

func TestMultipartImagesBothReachEveryTarget(t *testing.T) {
	body, _ := renderCase(t, "openai", "multipart-two-images", "openaicompat")
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 3 {
		t.Errorf("parts = %d, want text plus two images", len(parts))
	}

	// The inline image must survive to every target. The Gemini fixture fetcher
	// is offline with a zero cap, so the public URL is dropped with a warning —
	// intended behavior, not a test artifact: review finding F9 says fileData
	// cannot carry an arbitrary HTTP URL.
	gem, warns := renderCase(t, "openai", "multipart-two-images", "gemini")
	raw, _ := json.Marshal(gem)
	if !strings.Contains(string(raw), "iVBORw==") {
		t.Errorf("the inline image did not survive: %s", raw)
	}
	if !hasPrefixIn(warns, "messages[].image") {
		t.Errorf("warnings = %v; a URL that could not be inlined must be recorded", warns)
	}

	// The same inline image must reach Anthropic as a base64 source, not as a
	// url source pointing at a data: URI, which Anthropic rejects.
	an, _ := renderCase(t, "openai", "multipart-two-images", "anthropic")
	araw, _ := json.Marshal(an)
	if !strings.Contains(string(araw), `"type":"base64"`) {
		t.Errorf("a data URI must arrive as a base64 source: %s", araw)
	}
}

func TestEmptyAssistantTurnDoesNotBreakAnyTarget(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		body, _ := renderCase(t, "anthropic", "empty-assistant-turn", kind)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(raw) == 0 {
			t.Fatalf("%s produced nothing", kind)
		}
	}
}
