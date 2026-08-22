package ir

import "testing"

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
		{"embeddings", SurfaceEmbeddings, true},
		{"images", SurfaceImages, true},
		{"audio", SurfaceAudio, true},
		{"rerank", SurfaceRerank, true},
		{"moderations", SurfaceModerations, true},
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
