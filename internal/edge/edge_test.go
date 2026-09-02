package edge

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestReadCappedBodyAcceptsExactlyTheCapAndTypesTheOverflow(t *testing.T) {
	at := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 10)))
	body, err := ReadCappedBody(at, 10)
	if err != nil || len(body) != 10 {
		t.Fatalf("body = %d bytes, err = %v; a body exactly at the cap is fine", len(body), err)
	}
	over := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 11)))
	_, err = ReadCappedBody(over, 10)
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrPayloadTooLarge {
		t.Fatalf("err = %v; the overflow must reach the client as a 413, not a 400", err)
	}
}
