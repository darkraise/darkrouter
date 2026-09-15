package anthropic

import (
	"errors"
	"io"
	"testing"
)

func TestAStreamClosedBeforeItsStopIsAnError(t *testing.T) {
	// The Anthropic writer closes a sequence that simply ends with a
	// message_stop of its own, so the parser is the only place a cut
	// connection can still be told apart from a finished message.
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":3}}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half"}}`)
	if _, err := collect(t, body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestAStreamEndingAfterItsStopReasonIsComplete(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":3}}}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
	if _, err := collect(t, body); err != nil {
		t.Fatalf("err = %v", err)
	}
}
