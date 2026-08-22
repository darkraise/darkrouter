// Package edge holds the inbound dialects clients speak to Darkrouter.
package edge

import (
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Passthrough carries what the Phase 9 fast path needs to forward a request
// without re-rendering it. Phase 1 populates it; nothing consumes it yet.
type Passthrough struct {
	Body       []byte // the raw inbound body, retained for replay across attempts
	ModelField string // top-level JSON key holding the model, or "" when in the URL
	Surface    ir.Surface
}

type Dialect interface {
	Name() string
	ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *Passthrough, error)
	WriteResponse(w http.ResponseWriter, resp *ir.Response) error
	WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
