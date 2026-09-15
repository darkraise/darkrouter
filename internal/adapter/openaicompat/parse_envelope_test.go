package openaicompat

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parseBody(body string) (*ir.Response, error) {
	return ParseResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
}

func TestParseResponseReportsAnErrorEnvelopeUnderA200(t *testing.T) {
	_, err := parseBody(`{"error":{"message":"quota exhausted","type":"insufficient_quota"}}`)
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrRateLimit || e.Message != "quota exhausted" {
		t.Fatalf("err = %v, want the upstream's rate-limit error", err)
	}
}

func TestParseResponseRejectsABodyWithNoChoices(t *testing.T) {
	if _, err := parseBody(`{}`); err == nil {
		t.Fatal("an object with no choices is not a completion")
	}
}

func TestParseResponseAcceptsAnEmptyMessage(t *testing.T) {
	out, err := parseBody(`{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`)
	if err != nil || len(out.Content) != 0 {
		t.Fatalf("out = %+v, err = %v; empty content is a legitimate answer", out, err)
	}
}
