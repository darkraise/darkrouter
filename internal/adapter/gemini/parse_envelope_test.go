package gemini

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseResponseRejectsABodyWithNoCandidates(t *testing.T) {
	_, err := ParseResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))})
	if err == nil {
		t.Fatal("an object with no candidates and no block reason is not a completion")
	}
}
