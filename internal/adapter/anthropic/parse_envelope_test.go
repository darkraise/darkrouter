package anthropic

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parseRaw(body string) (*ir.Response, error) {
	return ParseResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
}

func TestParseResponseReportsAnErrorEnvelopeUnderA200(t *testing.T) {
	_, err := parseRaw(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrOverloaded || e.Message != "Overloaded" {
		t.Fatalf("err = %v, want the upstream's overloaded error", err)
	}
}

func TestParseResponseRejectsABodyWithNoMessage(t *testing.T) {
	if _, err := parseRaw(`{}`); err == nil {
		t.Fatal("an object that is not a message is not a completion")
	}
}

func TestParseResponseAcceptsAMessageWithNoContent(t *testing.T) {
	out, err := parseRaw(`{"type":"message","content":[],"stop_reason":"end_turn"}`)
	if err != nil || len(out.Content) != 0 {
		t.Fatalf("out = %+v, err = %v; empty content is a legitimate answer", out, err)
	}
}
