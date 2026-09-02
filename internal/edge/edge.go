// Package edge holds the inbound dialects clients speak to Darkrouter.
package edge

import (
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/ir"
)

// ReadCappedBody reads an inbound body under the configured cap, reading one
// byte past it so "exactly at the cap" is not rejected. An oversized body is
// a typed error: it asks the client for the same thing an oversized upload
// does, and only the type carries that distinction out to the status code.
func ReadCappedBody(r *http.Request, maxBody int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBody {
		return nil, &ir.Error{
			Type:    ir.ErrPayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody),
		}
	}
	return body, nil
}

// Passthrough carries what the phase 9 fast path needs to forward a request
// without re-rendering it. Every dialect populates it; eligibility is decided
// per attempt in the executor, not here.
type Passthrough struct {
	Body       []byte // the raw inbound body, retained for replay across attempts
	ModelField string // top-level JSON key holding the model, or "" when in the URL
	Surface    ir.Surface

	// Method is the URL-carried operation for a dialect whose model lives in
	// the path — Gemini's generateContent or streamGenerateContent. Exactly one
	// of ModelField and Method is set; both empty means the dialect declared no
	// rewritable identifier and the request is not forwardable.
	Method string

	// Query is the inbound query string with this dialect's credential
	// parameter removed. Replayed onto the upstream URL so ?alt=sse survives a
	// forward, while Darkrouter's own proxy token never leaves the process.
	Query url.Values

	// Stream mirrors the parsed request's stream flag. The forwarder needs it
	// to decide on stream_options injection and on which response reader to
	// use, and it does not hold the ir.Request.
	Stream bool
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

// ModerationDialect is the inbound wire form of the moderation surface.
type ModerationDialect interface {
	Name() string
	ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error)
	WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// RerankDialect is the inbound wire form of the rerank surface. Its shape is
// Cohere v2, which Darkrouter adopts because OpenAI defines no rerank endpoint.
type RerankDialect interface {
	Name() string
	ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error)
	WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// ImageDialect is the inbound wire form of the image surface.
type ImageDialect interface {
	Name() string
	ParseImage(r *http.Request, maxBody int64) (*ir.ImageRequest, error)
	WriteImage(w http.ResponseWriter, resp *ir.ImageResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// SpeechDialect is the inbound wire form of the tts surface. It has no writer:
// the response is forwarded byte for byte.
type SpeechDialect interface {
	Name() string
	ParseSpeech(r *http.Request, maxBody int64) (*ir.SpeechRequest, error)
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// CountWriter is implemented by a dialect with a token-counting endpoint.
type CountWriter interface {
	Dialect
	WriteCount(w http.ResponseWriter, tokens int) error
}
