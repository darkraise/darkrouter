package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// The three ways an auxiliary surface differs from every other. Everything else
// — the budget gate, health, credential rotation, classification, attempt
// records, commit semantics — belongs to the loop.
type (
	AuxBuild      func(context.Context, *adapter.Target, adapter.Adapter) (*http.Request, []ir.Warning, error)
	AuxRespond    func(*CommitWriter, *http.Response, *AttemptCtx) (adapter.Outcome, *ir.Error)
	AuxWriteError func(http.ResponseWriter, *ir.Error) error
)

// AuxOp is SurfaceOp with the boilerplate written once.
//
// Six surfaces differ in what they render and how they write, and are identical
// in everything else. Six near-duplicate types implementing four methods each
// would be thirty methods of ceremony around a dozen lines of real difference,
// and every one of them an opportunity to diverge on the parts that must not.
type AuxOp struct {
	dialect  string
	query    router.Query
	build    AuxBuild
	respond  AuxRespond
	writeErr AuxWriteError
}

func NewAuxOp(dialect string, q router.Query, build AuxBuild, respond AuxRespond, writeErr AuxWriteError) *AuxOp {
	return &AuxOp{dialect: dialect, query: q, build: build, respond: respond, writeErr: writeErr}
}

func (o *AuxOp) Dialect() string { return o.dialect }

func (o *AuxOp) Query() router.Query { return o.query }

func (o *AuxOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	return o.build(ctx, tgt, ad)
}

func (o *AuxOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	return o.respond(cw, resp, ac)
}

func (o *AuxOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.writeErr(w, e)
}

var _ SurfaceOp = (*AuxOp)(nil)

// ErrBodyTooLarge distinguishes an oversized response from a malformed one.
// They classify differently: an oversized body is a provider fault worth
// failing over, a syntax error may be a refusal shaped like one.
var ErrBodyTooLarge = errors.New("response body exceeds the cap")

// ReadCapped reads at most max bytes and reports ErrBodyTooLarge if there were
// more. It reads max+1 to tell "exactly at the cap" from "over it" — a response
// landing exactly on the boundary is legitimate and must not be rejected.
func ReadCapped(r io.Reader, max int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return buf, err
	}
	if int64(len(buf)) > max {
		return buf[:max], fmt.Errorf("%w: %d bytes", ErrBodyTooLarge, max)
	}
	return buf, nil
}

// DecodeJSON reads a bounded JSON document into v.
//
// Bounded because an unbounded read from a misbehaving provider is the hazard
// max_body_bytes prevents on the way in and nothing was preventing on the way
// out. The body is read whole rather than streamed into a decoder so the cap is
// enforced before any parsing work happens.
func DecodeJSON(r io.Reader, max int64, v any) error {
	buf, err := ReadCapped(r, max)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
