package gemini

import (
	"errors"
	"io"
	"testing"
)

func TestAStreamClosedBeforeAFinishReasonIsAnError(t *testing.T) {
	body := data(`{"responseId":"r1","candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`)
	if _, err := collect(t, body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}
