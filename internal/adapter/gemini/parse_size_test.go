package gemini

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestParseResponseRejectsAnOversizedBody(t *testing.T) {
	pad := bytes.Repeat([]byte("x"), adapter.MaxResponseBytes)
	body := io.MultiReader(strings.NewReader(`{"responseId":"`), bytes.NewReader(pad),
		strings.NewReader(`","candidates":[]}`))
	_, err := ParseResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(body)})
	if !errors.Is(err, adapter.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}
