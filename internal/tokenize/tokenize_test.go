package tokenize

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEncodingForKnownFamilies(t *testing.T) {
	cases := []struct {
		model string
		want  Encoding
	}{
		{"gpt-5", O200k},
		{"gpt-4.1-mini", O200k},
		{"gpt-4o", O200k},
		{"o3-mini", O200k},
		{"openai/gpt-oss-120b", O200k},
		{"gpt-4-turbo", Cl100k},
		{"gpt-3.5-turbo", Cl100k},
		{"text-embedding-3-small", Cl100k},
		{"claude-sonnet-4-5", Heuristic},
		{"gemini-2.0-flash", Heuristic},
		{"llama-3.3-70b", Heuristic},
		{"", Heuristic},
	}
	for _, tc := range cases {
		if got := EncodingFor(tc.model); got != tc.want {
			t.Errorf("EncodingFor(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestCountUsesTheBPEForAKnownFamily(t *testing.T) {
	// A long run of one character is where the BPE and the heuristic diverge
	// hard: o200k merges the run into ~50 tokens where characters-over-four
	// says 100. A bound that both answers satisfy would pass even if the
	// tokenizer silently failed to load, which is the failure worth catching.
	req := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: strings.Repeat("a", 400)}},
	}}}
	got := Count(req, "gpt-4o")
	if got > 80 {
		t.Errorf("Count = %d; that is the characters-over-four answer, so the BPE did not load", got)
	}
	if got < 20 {
		t.Errorf("Count = %d; implausibly low for 400 characters", got)
	}
}

func TestCountFallsBackToCharactersOverFour(t *testing.T) {
	text := strings.Repeat("a", 400)
	req := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}},
	}}}
	got := Count(req, "claude-sonnet-4-5")
	if got < 100 || got > 110 {
		t.Errorf("Count = %d; 400 characters over four is 100 plus overhead", got)
	}
}

func TestCountIncludesSystemToolsAndToolResults(t *testing.T) {
	base := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
	}}}
	withMore := &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: strings.Repeat("s", 200)}},
		Messages: append([]ir.Message{}, base.Messages...),
		Tools: []ir.Tool{{
			Name: "lookup", Description: strings.Repeat("d", 200),
			Schema: []byte(`{"type":"object","properties":{}}`),
		}},
	}
	if Count(withMore, "claude-x") <= Count(base, "claude-x")+50 {
		t.Error("system text and tool declarations both consume context and must be counted")
	}
}

func TestCountIgnoresMedia(t *testing.T) {
	withImage := &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "look"},
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: strings.Repeat("A", 10000)}},
		},
	}}}
	if Count(withImage, "gpt-4o") > 50 {
		t.Error("base64 payload must not be counted as text; tiling rules decide an image's cost")
	}
}

func TestCountIsNeverNegativeOnAnEmptyRequest(t *testing.T) {
	if got := Count(&ir.Request{}, "gpt-4o"); got < 0 {
		t.Errorf("Count = %d", got)
	}
}
