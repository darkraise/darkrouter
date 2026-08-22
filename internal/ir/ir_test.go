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
