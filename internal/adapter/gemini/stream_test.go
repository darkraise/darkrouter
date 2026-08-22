package gemini

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func collect(t *testing.T, body string) ([]ir.StreamEvent, error) {
	t.Helper()
	var (
		evs  []ir.StreamEvent
		last error
	)
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			last = err
			break
		}
		evs = append(evs, ev)
	}
	return evs, last
}

func data(chunk string) string { return "data: " + chunk + "\n\n" }

func TestParseStreamAppendsTextFragments(t *testing.T) {
	body := data(`{"responseId":"r1","modelVersion":"gemini-2.0-flash","candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Type != ir.EventMessageStart || evs[0].ID != "r1" || evs[0].Model != "gemini-2.0-flash" {
		t.Fatalf("first event = %+v", evs[0])
	}

	var text strings.Builder
	for _, ev := range evs {
		if ev.Type == ir.EventContentDelta && ev.Delta.Type == ir.BlockText {
			text.WriteString(ev.Delta.Text)
		}
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q; fragments append, they do not replace", text.String())
	}
	last := evs[len(evs)-1]
	if last.Type != ir.EventMessageStop || last.StopReason != ir.StopEndTurn {
		t.Errorf("last event = %+v", last)
	}
}

func TestParseStreamOpensOneTextBlockOnly(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"text":"a"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[{"text":"b"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, ev := range evs {
		if ev.Type == ir.EventBlockStart && ev.Delta.Type == ir.BlockText {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("text block starts = %d, want 1; the fragments belong to one block", starts)
	}
}

func TestParseStreamEmitsAFunctionCallWhole(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":"f","args":{"x":1}}}]},"finishReason":"STOP"}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var start, delta, stop bool
	for _, ev := range evs {
		switch {
		case ev.Type == ir.EventBlockStart && ev.Delta.Type == ir.BlockToolUse:
			start = true
			if ev.Delta.ToolID != "call_a" || ev.Delta.ToolName != "f" {
				t.Errorf("block start = %+v", ev.Delta)
			}
		case ev.Type == ir.EventContentDelta && ev.Delta.Type == ir.BlockToolUse:
			delta = true
			if ev.Delta.ToolInput != `{"x":1}` {
				t.Errorf("tool input = %q; it arrives whole, not fragmented", ev.Delta.ToolInput)
			}
		case ev.Type == ir.EventBlockStop:
			stop = true
		}
	}
	if !start || !delta || !stop {
		t.Fatalf("events = %+v", evs)
	}
	last := evs[len(evs)-1]
	if last.StopReason != ir.StopToolUse {
		t.Errorf("stop = %q; STOP with a functionCall means tool use", last.StopReason)
	}
}

func TestParseStreamCarriesThoughtsAndSignatures(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"text":"weighing","thought":true},{"text":"","thought":true,"thoughtSignature":"sig-1"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var sawText, sawSig bool
	for _, ev := range evs {
		if ev.Type != ir.EventContentDelta || ev.Delta.Type != ir.BlockThinking {
			continue
		}
		if ev.Delta.Thinking == "weighing" {
			sawText = true
		}
		if ev.Delta.Signature == "sig-1" {
			sawSig = true
		}
	}
	if !sawText || !sawSig {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParseStreamReportsABlockedPrompt(t *testing.T) {
	_, err := collect(t, data(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`))
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrContentFilter {
		t.Fatalf("err = %v, want a content-filter *ir.Error", err)
	}
}

func TestParseStreamWarnsOnAnUnknownFinishReason(t *testing.T) {
	evs, err := collect(t, data(`{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"SOMETHING_NEW"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Type != ir.EventMessageStop || last.StopReason != ir.StopEndTurn {
		t.Fatalf("last event = %+v", last)
	}
	if len(last.Warnings) != 1 || last.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v; a stream must not lose what the unary path records",
			last.Warnings)
	}
}

func TestParseStreamIgnoresAnUnparseableChunk(t *testing.T) {
	body := "data: {not json\n\n" + data(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatalf("a bad chunk must not kill the stream: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("the good chunk was lost")
	}
}
