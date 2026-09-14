package tokenize

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tiktoken-go/tokenizer"

	"github.com/darkraise/darkrouter/internal/ir"
)

func count(t *testing.T, req *ir.Request, model string) int {
	t.Helper()
	n, err := Count(context.Background(), req, model)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

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
	got := count(t, req, "gpt-4o")
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
	got := count(t, req, "claude-sonnet-4-5")
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
	if count(t, withMore, "claude-x") <= count(t, base, "claude-x")+50 {
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
	if count(t, withImage, "gpt-4o") > 50 {
		t.Error("base64 payload must not be counted as text; tiling rules decide an image's cost")
	}
}

func TestCountIsNeverNegativeOnAnEmptyRequest(t *testing.T) {
	if got := count(t, &ir.Request{}, "gpt-4o"); got < 0 {
		t.Errorf("Count = %d", got)
	}
}

// The codec is built once per encoding. Building it per count would pay the
// vocabulary load on every request to an endpoint that exists to be cheap.
func TestTheCodecIsConstructedOncePerEncoding(t *testing.T) {
	calls := 0
	orig := getCodec
	getCodec = func(e tokenizer.Encoding) (tokenizer.Codec, error) {
		calls++
		return orig(e)
	}
	t.Cleanup(func() { getCodec = orig })
	resetCodecs()

	req := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "the quick brown fox"}},
	}}}
	first := count(t, req, "gpt-4o")
	second := count(t, req, "gpt-4o")
	if first != second {
		t.Fatalf("counts differ: %d vs %d", first, second)
	}
	if calls != 1 {
		t.Errorf("codec constructed %d times, want 1", calls)
	}
	count(t, req, "gpt-4-turbo")
	if calls != 2 {
		t.Errorf("a second encoding must construct its own codec once: %d calls", calls)
	}
}

func textRequest(text string) *ir.Request {
	return &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}},
	}}}
}

// countWithin runs Count on its own goroutine so a pathological input fails
// the test at the deadline instead of hanging the package.
func countWithin(t *testing.T, ctx context.Context, req *ir.Request, d time.Duration) (int, error) {
	t.Helper()
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := Count(ctx, req, "gpt-4o")
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(d):
		t.Fatalf("Count did not return within %v", d)
		return 0, nil
	}
}

func TestCountBoundsTheWorkOfOneUnbrokenRun(t *testing.T) {
	// One mebibyte of a single letter is one regex piece, and merging a piece
	// is quadratic in its length: unbounded, this runs for minutes.
	const n = 1 << 20
	got, err := countWithin(t, context.Background(), textRequest(strings.Repeat("a", n)), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// o200k encodes a long run of "a" as eight-letter tokens.
	if want := n / 8; got < want || got > want+want/50 {
		t.Errorf("Count = %d, want within 2%% above %d", got, want)
	}
}

func TestCountStopsWhenTheRequestIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := countWithin(t, ctx, textRequest(strings.Repeat("a", 1<<20)), 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; a cancelled count must say so rather than return a number", err)
	}
}

// ordinaryText exercises every boundary the segmenter cuts at: spaces after
// words, space and tab runs, newline runs, a slash after a newline, digits,
// contractions, punctuation and non-ASCII letters and spaces.
const ordinaryText = "The gateway's router picks a target; it doesn't retry a 400.\n\n" +
	"func main() {\n\tfmt.Println(\"hello, world\")  // two spaces\n}\n" +
	"return x.\n// a comment after punctuation\n" +
	"/usr/local/bin/darkrouter --config=/etc/darkrouter.yaml\n" +
	"Prices: 1234567 units at 3.14159 each, or 42%.\r\n" +
	"Café naïve résumé — «quoted» text. Non-breaking　ideographic space.\n" +
	"日本語の文章 と 中文 句子 mixed with English words.\n" +
	"{\"model\":\"gpt-4o\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\n" +
	"   indented    with      runs   of   spaces   \n\t\tand tabs\t\there\n"

func TestSegmentingDoesNotChangeTheCountOfOrdinaryText(t *testing.T) {
	for _, enc := range []Encoding{O200k, Cl100k} {
		codec := codecs[enc].get()
		whole, err := codec.Count(ordinaryText)
		if err != nil {
			t.Fatal(err)
		}
		// Every clean cut on its own, so a wrong cut rule is pinned to the
		// position it breaks.
		for i := 1; i < len(ordinaryText); i++ {
			if !cleanCut(ordinaryText, i) {
				continue
			}
			left, _ := codec.Count(ordinaryText[:i])
			right, _ := codec.Count(ordinaryText[i:])
			if left+right != whole {
				t.Errorf("%s: cutting at %d (%q|%q) counts %d, whole counts %d", enc,
					i, ordinaryText[max(0, i-8):i], ordinaryText[i:min(len(ordinaryText), i+8)],
					left+right, whole)
			}
		}

		// And end to end, on text long enough to be split into segments.
		long := strings.Repeat(ordinaryText, 3*segmentBytes/len(ordinaryText))
		want, _ := codec.Count(long)
		if got := counterFor(context.Background(), enc)(long); got != want {
			t.Errorf("%s: segmented count = %d, whole count = %d", enc, got, want)
		}
	}
}
