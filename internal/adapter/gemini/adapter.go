package gemini

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Adapter wraps a Fetcher, which BuildRequest needs in order to inline media
// the Gemini API will not fetch for itself.
type Adapter struct{ f *Fetcher }

func New() *Adapter { return &Adapter{f: NewFetcher()} }

// NewWithFetcher lets a test supply a bounded or stubbed fetcher.
func NewWithFetcher(f *Fetcher) *Adapter { return &Adapter{f: f} }

func (a *Adapter) Kind() string { return "gemini" }

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return a.f.BuildRequest(ctx, t, req)
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
