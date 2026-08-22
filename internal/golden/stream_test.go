package golden

import (
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/edge"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
)

// streamDialects is dialects() plus the second Gemini wire form. Spec §3.2
// makes SSE versus a chunked JSON array a per-request choice, so a suite that
// exercised only one of them would leave half the Gemini writer unverified —
// which is exactly what the first version of this harness did.
func streamDialects() map[string]edge.Dialect {
	out := map[string]edge.Dialect{}
	for name, d := range dialects() {
		if name == "gemini" {
			continue
		}
		out[name] = d
	}
	out["gemini-sse"] = &geminiedge.Dialect{SSE: true}
	out["gemini-array"] = &geminiedge.Dialect{SSE: false}
	return out
}

// readStreamEvents parses a streamed body into reviewable values. Raw text
// would carry the volatile `created` field into the golden file and make every
// run differ.
//
// A body that is a bare JSON array is Gemini's non-SSE form; anything else is
// parsed as SSE framing.
func readStreamEvents(body string) []any {
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "[") {
		var chunks []any
		if err := json.Unmarshal([]byte(trimmed), &chunks); err == nil {
			out := []any{}
			for _, c := range chunks {
				out = append(out, map[string]any{"data": normalize(c)})
			}
			return out
		}
	}
	out := []any{}
	for _, block := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		entry := map[string]any{}
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				entry["event"] = name
			}
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				var v any
				if err := json.Unmarshal([]byte(payload), &v); err != nil {
					entry["data"] = payload // [DONE] and anything else non-JSON
				} else {
					entry["data"] = normalize(v)
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// collectEvents drains a stream sequence into a golden-friendly value,
// recording a terminal error as its own entry rather than losing it.
func collectEvents(seq iter.Seq2[ir.StreamEvent, error]) []any {
	out := []any{}
	for ev, err := range seq {
		if err != nil {
			out = append(out, map[string]any{"error": err.Error()})
			break
		}
		out = append(out, ev)
	}
	return out
}

// replay turns a captured event list back into a sequence, so the edge writers
// see exactly what the adapter produced.
func replay(events []ir.StreamEvent, final error) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
}

func streamCaseDirs(t *testing.T, kind string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", "streams", kind)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// drain runs an adapter's stream parser over a fixture, returning the events
// and any terminal error.
func drain(t *testing.T, kind, body string) ([]ir.StreamEvent, error) {
	t.Helper()
	var (
		events []ir.StreamEvent
		final  error
	)
	for ev, err := range adapters()[kind].ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			final = err
			break
		}
		events = append(events, ev)
	}
	return events, final
}

func TestGoldenStreams(t *testing.T) {
	for kind, ad := range adapters() {
		for _, dir := range streamCaseDirs(t, kind) {
			t.Run(kind+"/"+filepath.Base(dir), func(t *testing.T) {
				body := string(readFixture(t, filepath.Join(dir, "upstream.sse")))

				compareJSON(t, filepath.Join(dir, "events.json"),
					collectEvents(ad.ParseStream(strings.NewReader(body), 1<<20)))

				events, final := drain(t, kind, body)
				for dialect, d := range streamDialects() {
					rec := recorder()
					if err := d.WriteStream(rec, replay(events, final)); err != nil {
						t.Fatalf("%s: %v", dialect, err)
					}
					compareJSON(t, filepath.Join(dir, "written", dialect+".json"),
						readStreamEvents(rec.Body.String()))
				}
			})
		}
	}
}

func TestStreamErrorReachesEveryDialect(t *testing.T) {
	dir := filepath.Join("testdata", "golden", "streams", "anthropic", "error-after-three-blocks")
	body := string(readFixture(t, filepath.Join(dir, "upstream.sse")))

	events, final := drain(t, "anthropic", body)
	if final == nil {
		t.Fatal("the fixture ends in an error event and the adapter must surface it")
	}

	// Every dialect renders the failure in its own shape, and none of them may
	// end the stream as though it had succeeded normally.
	for dialect, d := range streamDialects() {
		rec := recorder()
		if err := d.WriteStream(rec, replay(events, final)); err != nil {
			t.Fatalf("%s: %v", dialect, err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, "Overloaded") {
			t.Errorf("%s: the message did not reach the client: %s", dialect, out)
		}
		switch dialect {
		case "openai":
			if !strings.Contains(out, `"error"`) || !strings.HasSuffix(out, "data: [DONE]\n\n") {
				t.Errorf("openai: spec §4.9 wants an error payload then DONE: %s", out)
			}
		case "anthropic":
			if !strings.Contains(out, "event: error") {
				t.Errorf("anthropic: spec §4.9 wants a real error event: %s", out)
			}
			if strings.Contains(out, "message_stop") {
				t.Errorf("anthropic: an errored stream must not also stop normally: %s", out)
			}
		case "gemini-sse", "gemini-array":
			if !strings.Contains(out, "promptFeedback") {
				t.Errorf("%s: SSE has no error event, so the shape is a final chunk: %s", dialect, out)
			}
		}
	}
}
