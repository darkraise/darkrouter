package exec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

func TestAuxOpDelegatesToItsClosures(t *testing.T) {
	q := router.Query{Model: "m", Surface: ir.SurfaceEmbedding}
	var built, responded, errored bool

	op := NewAuxOp("openai", q,
		func(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
			built = true
			req, err := http.NewRequestWithContext(ctx, "POST", "http://x/v1/embeddings", strings.NewReader("{}"))
			return req, nil, err
		},
		func(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
			responded = true
			return adapter.OutcomeSuccess, nil
		},
		func(w http.ResponseWriter, e *ir.Error) error {
			errored = true
			return nil
		})

	if op.Query() != q {
		t.Errorf("Query() = %+v", op.Query())
	}
	if op.Dialect() != "openai" {
		t.Errorf("Dialect() = %q; the request row would record the wrong wire form", op.Dialect())
	}
	if _, _, err := op.Build(context.Background(), &adapter.Target{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, aerr := op.Respond(NewCommitWriter(httptest.NewRecorder()),
		&http.Response{Body: io.NopCloser(strings.NewReader(""))}, &AttemptCtx{}); aerr != nil {
		t.Fatal(aerr)
	}
	_ = op.WriteError(httptest.NewRecorder(), &ir.Error{})

	if !built || !responded || !errored {
		t.Errorf("delegation = (%v, %v, %v), want all true", built, responded, errored)
	}
}

func TestAuxOpSatisfiesSurfaceOp(t *testing.T) {
	var _ SurfaceOp = NewAuxOp("openai", router.Query{}, nil, nil, nil)
}

func TestReadCappedStopsAtTheLimit(t *testing.T) {
	// An unbounded read from a misbehaving provider is exactly the hazard
	// max_body_bytes prevents inbound and nothing was preventing outbound.
	body := io.NopCloser(strings.NewReader(strings.Repeat("a", 100)))
	got, err := ReadCapped(body, 10)
	if err == nil {
		t.Fatal("an oversized body read cleanly")
	}
	if len(got) > 10 {
		t.Errorf("read %d bytes past a 10-byte cap", len(got))
	}
}

func TestReadCappedAcceptsAnExactFit(t *testing.T) {
	// The boundary must not be an off-by-one: a response exactly at the cap is
	// legitimate, and rejecting it would fail on a perfectly good payload.
	got, err := ReadCapped(io.NopCloser(strings.NewReader("0123456789")), 10)
	if err != nil {
		t.Fatalf("a body exactly at the cap was rejected: %v", err)
	}
	if string(got) != "0123456789" {
		t.Errorf("body = %q", got)
	}
}

func TestDecodeJSONRejectsGarbage(t *testing.T) {
	// An HTML error page behind a 200 is a provider fault, not a decode
	// curiosity: it must surface as an error the loop can classify.
	var into struct {
		A int `json:"a"`
	}
	err := DecodeJSON(io.NopCloser(strings.NewReader("<html>502</html>")), 1<<20, &into)
	if err == nil {
		t.Fatal("an HTML body decoded cleanly")
	}
}

func TestDecodeJSONReadsTheDocument(t *testing.T) {
	var into struct {
		A int `json:"a"`
	}
	if err := DecodeJSON(io.NopCloser(strings.NewReader(`{"a":7}`)), 1<<20, &into); err != nil {
		t.Fatal(err)
	}
	if into.A != 7 {
		t.Errorf("a = %d, want 7", into.A)
	}
}

func TestDecodeJSONPropagatesTheCap(t *testing.T) {
	var into map[string]any
	err := DecodeJSON(io.NopCloser(strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`)), 16, &into)
	if err == nil {
		t.Fatal("an oversized document decoded cleanly")
	}
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("err = %v, want ErrBodyTooLarge so the caller can tell it from a syntax error", err)
	}
}
