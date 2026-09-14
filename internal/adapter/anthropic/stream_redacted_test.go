package anthropic

import (
	"net/http/httptest"
	"strings"
	"testing"

	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
)

// Anthropic ships a redacted block's payload whole in its content_block_start
// and has no delta for it, so a start that loses data loses the block.
func TestRedactedThinkingKeepsItsDataThroughTheWriter(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":1}}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"EmwKAhgBEgy3va3pzix"}}`) +
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`) +
		sseEvent("message_stop", `{"type":"message_stop"}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	start := evs[2]
	if start.Delta == nil || start.Delta.Thinking != "EmwKAhgBEgy3va3pzix" {
		t.Errorf("block start = %+v, want the redacted payload carried", start.Delta)
	}

	rec := httptest.NewRecorder()
	if err := anthropicedge.WriteStream(rec, ParseStream(strings.NewReader(body), 1<<20)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"data":"EmwKAhgBEgy3va3pzix"`) {
		t.Errorf("written stream lost the redacted payload:\n%s", rec.Body.String())
	}
}
