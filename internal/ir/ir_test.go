package ir

import (
	"encoding/json"
	"testing"
)

func TestNeedsDetectsTools(t *testing.T) {
	r := &Request{Tools: []Tool{{Name: "get_weather"}}}
	if !r.Needs().Tools {
		t.Fatal("expected Tools to be true when the request declares tools")
	}
}

func TestNeedsDetectsVisionFromAnyMessage(t *testing.T) {
	r := &Request{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockImage, Media: &Media{MIME: "image/png"}}}},
	}}
	if !r.Needs().Vision {
		t.Fatal("expected Vision to be true when any message carries an image block")
	}
}

func TestNeedsDetectsReasoning(t *testing.T) {
	r := &Request{Reasoning: &Reasoning{Effort: "high"}}
	if !r.Needs().Reasoning {
		t.Fatal("expected Reasoning to be true when a reasoning budget is set")
	}
}

func TestNeedsIsFalseForPlainTextRequest(t *testing.T) {
	r := &Request{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}},
	}}
	n := r.Needs()
	if n.Tools || n.Vision || n.Reasoning {
		t.Fatalf("expected all needs false, got %+v", n)
	}
}

func TestParseSurface(t *testing.T) {
	cases := []struct {
		in     string
		want   Surface
		wantOK bool
	}{
		{"llm", SurfaceLLM, true},
		{"embedding", SurfaceEmbedding, true},
		{"image", SurfaceImage, true},
		{"tts", SurfaceTTS, true},
		{"stt", SurfaceSTT, true},
		{"rerank", SurfaceRerank, true},
		{"moderation", SurfaceModeration, true},
		{"", "", false},
		{"nonsense", "", false},
		// Surfaces come from stored catalog rows and inbound routes, both of
		// which are lower-case by construction; accepting other casings would
		// let two spellings of one surface diverge.
		{"LLM", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseSurface(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("ParseSurface(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestToolResultTextConcatenatesTextBlocks(t *testing.T) {
	tr := &ToolResult{
		ToolUseID: "call_1",
		Content: []ContentBlock{
			{Type: BlockText, Text: "42"},
			{Type: BlockImage, Media: &Media{MIME: "image/png", Data: "AAAA"}},
			{Type: BlockText, Text: " degrees"},
		},
	}
	if got := tr.Text(); got != "42 degrees" {
		t.Errorf("Text() = %q, want %q", got, "42 degrees")
	}
}

func TestToolResultTextOnNilIsEmpty(t *testing.T) {
	var tr *ToolResult
	if got := tr.Text(); got != "" {
		t.Errorf("Text() = %q, want empty", got)
	}
}

func TestWarningStringNamesFieldTargetAndReason(t *testing.T) {
	w := Warning{Field: "cache_control", Target: "gemini", Reason: "no equivalent"}
	if got := w.String(); got != "cache_control -> gemini: no equivalent" {
		t.Errorf("String() = %q", got)
	}
}

func TestStreamEventCarriesWarnings(t *testing.T) {
	ev := StreamEvent{
		Type: EventMessageStop,
		Warnings: []Warning{{
			Field: "finishReason", Target: "gemini", Reason: "unrecognized value",
		}},
	}
	if len(ev.Warnings) != 1 || ev.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v", ev.Warnings)
	}
}

func TestNeedsFindsVisionInsideAToolResult(t *testing.T) {
	r := &Request{Messages: []Message{{
		Role: RoleTool,
		Content: []ContentBlock{{
			Type: BlockToolResult,
			ToolResult: &ToolResult{
				ToolUseID: "call_1",
				Content: []ContentBlock{
					{Type: BlockImage, Media: &Media{MIME: "image/png", Data: "AAAA"}},
				},
			},
		}},
	}}}
	if !r.Needs().Vision {
		t.Error("Needs().Vision = false; an image inside a tool result still needs vision")
	}
}

func TestSurfaceVocabularyMatchesTheMasterDesign(t *testing.T) {
	// Master design §6 fixes these seven. Phase 1 shipped six, with speech and
	// transcription collapsed into one "audio" — which cannot express a
	// provider that serves one and not the other, and phase 5's adapter matrix
	// has a row for each.
	want := []Surface{
		SurfaceLLM, SurfaceEmbedding, SurfaceImage,
		SurfaceTTS, SurfaceSTT, SurfaceRerank, SurfaceModeration,
	}
	got := AllSurfaces()
	if len(got) != len(want) {
		t.Fatalf("AllSurfaces has %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllSurfaces()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSurfaceSpellings(t *testing.T) {
	// The exact strings are what presets declare and the models.surfaces
	// column stores, so they are data, not just identifiers.
	cases := map[Surface]string{
		SurfaceLLM:        "llm",
		SurfaceEmbedding:  "embedding",
		SurfaceImage:      "image",
		SurfaceTTS:        "tts",
		SurfaceSTT:        "stt",
		SurfaceRerank:     "rerank",
		SurfaceModeration: "moderation",
	}
	for s, want := range cases {
		if string(s) != want {
			t.Errorf("surface = %q, want %q", s, want)
		}
	}
}

func TestParseSurfaceAcceptsEveryValueAndNothingElse(t *testing.T) {
	for _, s := range AllSurfaces() {
		if got, ok := ParseSurface(string(s)); !ok || got != s {
			t.Errorf("ParseSurface(%q) = (%q, %v)", s, got, ok)
		}
	}
	// The retired plural spellings must not parse. A preset still carrying one
	// would otherwise be silently dropped by the merge and the model would
	// serve chat only, with nothing saying why.
	for _, bad := range []string{"embeddings", "images", "audio", "moderations", "chat", ""} {
		if _, ok := ParseSurface(bad); ok {
			t.Errorf("ParseSurface(%q) accepted a value that is not in the vocabulary", bad)
		}
	}
}

func TestToolBuiltInNeedsAnEmptyNameAndABody(t *testing.T) {
	raw := json.RawMessage(`{}`)
	cases := []struct {
		tool Tool
		want bool
	}{
		{Tool{Name: "lookup"}, false},
		{Tool{Name: "lookup", Extra: map[string]json.RawMessage{"strict": raw}}, false},
		{Tool{Extra: map[string]json.RawMessage{"googleSearch": raw}}, true},
		{Tool{}, false},
	}
	for _, tc := range cases {
		if got := tc.tool.BuiltIn(); got != tc.want {
			t.Errorf("%+v BuiltIn = %v, want %v", tc.tool, got, tc.want)
		}
	}
}
