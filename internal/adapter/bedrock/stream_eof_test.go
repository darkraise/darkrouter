package bedrock

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestAStreamClosedBeforeMessageStopIsAnError(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "messageStart", map[string]any{"role": "assistant"})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "It is "}})
	if _, err := collect(t, &buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}
