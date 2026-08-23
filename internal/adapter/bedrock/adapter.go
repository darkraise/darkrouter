// Package bedrock renders the IR to AWS Bedrock's Converse API.
//
// Converse rather than InvokeModel, spec §3.1: InvokeModel takes each model
// family's native payload, reintroducing exactly the per-family branching this
// design exists to avoid. Converse takes one message shape across families,
// with tool use and image content, and the IR maps to it directly.
//
// Authentication is not this package's concern. internal/auth signs the built
// request, which is what keeps one HTTP transport and one timeout policy across
// every adapter rather than routing through the Bedrock service client.
package bedrock

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "bedrock" }

// Surfaces is llm alone. Bedrock serves embeddings through a different API
// shape entirely, and claiming the surface here would route embedding requests
// to a Converse endpoint that answers 400.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.SurfaceSet{ir.SurfaceLLM: true}
}

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return BuildRequest(ctx, t, req)
}

func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return ParseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return ParseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return Classify(resp, err)
}

var _ adapter.Adapter = (*Adapter)(nil)

// Replaced in Task 6.
func ParseStream(io.Reader, int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		yield(ir.StreamEvent{}, errors.New("bedrock: stream parsing arrives in task 6"))
	}
}
