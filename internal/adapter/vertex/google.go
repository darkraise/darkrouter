package vertex

import (
	"context"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// buildGoogle reuses the Gemini adapter's builder unchanged.
//
// That builder appends "/models/{model}:generateContent" to the target's base
// URL, so handing it a base URL ending in the publisher segment produces
// exactly Vertex's path. The alternative — a second Gemini renderer — would
// drift from the one the golden files cover.
func (a *Adapter) buildGoogle(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	inner := *t
	inner.BaseURL = baseFor(t) + "/" + PublisherGoogle
	// The bearer token comes from internal/auth, so the Gemini builder must
	// not write x-goog-api-key.
	inner.APIKey = ""
	return a.gemini().BuildRequest(ctx, &inner, req)
}
