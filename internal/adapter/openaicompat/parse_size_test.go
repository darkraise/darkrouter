package openaicompat

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// oversizedJSON is a well-formed response whose one string field alone is
// past the limit, so only the size bound can reject it.
func oversizedJSON() io.Reader {
	pad := io.LimitReader(repeatByte('x'), adapter.MaxResponseBytes)
	return io.MultiReader(strings.NewReader(`{"id":"`), pad, strings.NewReader(`","choices":[]}`))
}

type repeatByte byte

func (b repeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestParseResponseRejectsAnOversizedBody(t *testing.T) {
	_, err := ParseResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(oversizedJSON())})
	if !errors.Is(err, adapter.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}

func TestAReplayedUnaryBodyIsBoundedToo(t *testing.T) {
	var got error
	for _, err := range ParseStream(oversizedJSON(), 1<<20) {
		if err != nil {
			got = err
		}
	}
	if !errors.Is(got, adapter.ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", got)
	}
}
