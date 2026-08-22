package openai

import (
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Dialect struct{}

func New() *Dialect { return &Dialect{} }

func (d *Dialect) Name() string { return "openai" }

func (d *Dialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	return ParseRequest(r, maxBody)
}

func (d *Dialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponse(w, resp)
}

func (d *Dialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return WriteStream(w, events)
}

func (d *Dialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*Dialect)(nil)
