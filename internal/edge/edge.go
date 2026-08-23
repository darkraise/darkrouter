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

	// ProxyToken extracts the inbound proxy credential in this dialect's own
	// form. Master design §13: an OpenAI client sends Authorization: Bearer, an
	// Anthropic client x-api-key, a Gemini client x-goog-api-key or ?key=. All
	// three are compared against the same server.proxy_token.
	ProxyToken(r *http.Request) string

	ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *Passthrough, error)
	WriteResponse(w http.ResponseWriter, resp *ir.Response) error
	WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// EmbeddingDialect is the inbound wire form of the embedding surface.
//
// It is a separate interface rather than more methods on Dialect because the
// two shapes share nothing — an embedding request has no messages and its
// response has no content blocks — and Anthropic and Gemini would each stub
// four methods to say they do not serve it.
type EmbeddingDialect interface {
	Name() string
	ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error)
	WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// CountWriter is implemented by a dialect with a token-counting endpoint.
type CountWriter interface {
	Dialect
	WriteCount(w http.ResponseWriter, tokens int) error
}
