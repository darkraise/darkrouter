package bedrock

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/ir"
)

// frame encodes one eventstream message the way Bedrock does: an event type in
// the headers, a JSON payload in the body. Building frames with the SDK's own
// encoder beats a checked-in binary blob: a hand-written one with a wrong CRC
// fails for a reason unrelated to the code under test, and a reader cannot see
// what is in it.
func frame(t *testing.T, w io.Writer, eventType string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue(eventType)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: raw,
	}
	if err := eventstream.NewEncoder().Encode(w, msg); err != nil {
		t.Fatal(err)
	}
}

func exceptionFrame(t *testing.T, w io.Writer, exceptionType, message string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"message": message})
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("exception")},
			{Name: ":exception-type", Value: eventstream.StringValue(exceptionType)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: raw,
	}
	if err := eventstream.NewEncoder().Encode(w, msg); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, r io.Reader) ([]ir.StreamEvent, error) {
	t.Helper()
	var out []ir.StreamEvent
	for ev, err := range ParseStream(r, 1<<20) {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func TestStreamDecodesAWholeTurn(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "messageStart", map[string]any{"role": "assistant"})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "It is "}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "17C."}})
	frame(t, &buf, "contentBlockStop", map[string]any{"contentBlockIndex": 0})
	frame(t, &buf, "messageStop", map[string]any{"stopReason": "end_turn"})
	frame(t, &buf, "metadata", map[string]any{
		"usage": map[string]any{"inputTokens": 12, "outputTokens": 7}})

	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var sawStart, sawStop bool
	var usage *ir.Usage
	for _, ev := range events {
		switch ev.Type {
		case ir.EventMessageStart:
			sawStart = true
		case ir.EventContentDelta:
			text += ev.Delta.Text
		case ir.EventMessageStop:
			sawStop = true
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if !sawStart || !sawStop {
		t.Errorf("missing start/stop: %+v", events)
	}
	if text != "It is 17C." {
		t.Errorf("text = %q", text)
	}
	// Usage arrives after messageStop. Completing on messageStop would report
	// zero tokens for every streamed Bedrock request.
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestStreamHandlesAFrameSplitAcrossReads(t *testing.T) {
	// Spec §7 names this case. A decoder that assumed one Read returns one
	// whole frame works against a bytes.Buffer and fails against a socket.
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "split"}})
	frame(t, &buf, "messageStop", map[string]any{"stopReason": "end_turn"})

	events, err := collect(t, &oneByteReader{r: bytes.NewReader(buf.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, ev := range events {
		if ev.Type == ir.EventContentDelta {
			text += ev.Delta.Text
		}
	}
	if text != "split" {
		t.Errorf("text = %q; a frame split across reads was not reassembled", text)
	}
}

// oneByteReader returns one byte per Read, which is the worst case a socket can
// present and the one a length-prefixed decoder must survive.
type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestStreamSurfacesAMidStreamException(t *testing.T) {
	// Spec §7 names this too. An exception frame after content has flowed is
	// the case where the status line said 200 and the failure arrived later.
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "partial"}})
	exceptionFrame(t, &buf, "modelStreamErrorException", "the model stream failed")

	events, err := collect(t, &buf)
	if err == nil {
		t.Fatal("a mid-stream exception must surface as an error")
	}
	if len(events) == 0 {
		t.Error("the content before the exception must still be delivered")
	}
}

func TestStreamDecodesAToolCall(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockStart", map[string]any{
		"contentBlockIndex": 1,
		"start": map[string]any{"toolUse": map[string]any{
			"toolUseId": "tu_1", "name": "get_weather"}}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 1,
		"delta":             map[string]any{"toolUse": map[string]any{"input": `{"city":`}}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 1,
		"delta":             map[string]any{"toolUse": map[string]any{"input": `"Oslo"}`}}})
	frame(t, &buf, "contentBlockStop", map[string]any{"contentBlockIndex": 1})

	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	var id, name, input string
	for _, ev := range events {
		if ev.Delta == nil {
			continue
		}
		id += ev.Delta.ToolID
		name += ev.Delta.ToolName
		input += ev.Delta.ToolInput
	}
	if id != "tu_1" || name != "get_weather" {
		t.Errorf("tool id/name = %q/%q", id, name)
	}
	if input != `{"city":"Oslo"}` {
		t.Errorf("tool input = %q", input)
	}
}

func TestStreamDecodesReasoningDeltas(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0,
		"delta":             map[string]any{"reasoningContent": map[string]any{"text": "hmm"}}})
	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Delta.Thinking != "hmm" {
		t.Errorf("events = %+v", events)
	}
}

func TestStreamReportsACorruptFrame(t *testing.T) {
	// A truncated or corrupted frame must be an error rather than a silent
	// stop: a stream that ends early looks like a short completion.
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "ok"}})
	raw := buf.Bytes()
	corrupt := append([]byte{}, raw...)
	corrupt = append(corrupt, 0x00, 0x00, 0x00, 0x40, 0xff, 0xff)

	if _, err := collect(t, bytes.NewReader(corrupt)); err == nil {
		t.Fatal("a corrupt frame must surface as an error")
	}
}

func TestStreamCarriesRedactedReasoning(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "messageStart", map[string]any{"role": "assistant"})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{
			"reasoningContent": map[string]any{"redactedContent": "AAAA"}}})
	frame(t, &buf, "contentBlockStop", map[string]any{"contentBlockIndex": 0})
	frame(t, &buf, "messageStop", map[string]any{"stopReason": "end_turn"})

	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	var got *ir.Delta
	for _, ev := range events {
		if ev.Type == ir.EventContentDelta {
			got = ev.Delta
		}
	}
	if got == nil || got.Type != ir.BlockRedactedThinking || got.Thinking != "AAAA" {
		t.Errorf("delta = %+v, want a redacted-thinking delta carrying the payload", got)
	}
}
