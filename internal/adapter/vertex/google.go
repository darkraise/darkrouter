package vertex

import (
	"context"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
)

// gemini is shared: its only state is the media fetcher, which is safe for
// concurrent use and expensive enough not to rebuild per request.
var gemini = geminiadapter.New()

// buildGoogle reuses phase 4's Gemini builder unchanged.
//
// That builder appends "/models/{model}:generateContent" to the target's base
// URL, so handing it a base URL ending in the publisher segment produces
// exactly Vertex's path. The alternative — a second Gemini renderer — would
// drift from the one the golden files cover.
func buildGoogle(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	inner := *t
	inner.BaseURL = baseFor(t) + "/" + PublisherGoogle
	// The bearer token comes from internal/auth, so the Gemini builder must
	// not write x-goog-api-key.
	inner.APIKey = ""
	return gemini.BuildRequest(ctx, &inner, req)
}
