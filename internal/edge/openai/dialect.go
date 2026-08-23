package openai

import (
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Dialect struct{}

func New() *Dialect { return &Dialect{} }

func (d *Dialect) Name() string { return "openai" }

// ProxyToken reads the bearer token. RFC 7235 auth schemes are
// case-insensitive, so a client sending "bearer" must still authenticate.
func (d *Dialect) ProxyToken(r *http.Request) string {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

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

// ResponsesDialect serves /v1/responses. It is a fourth edge.Dialect rather
// than a seventh SurfaceOp because Responses is chat-shaped: making it a
// dialect is what lets it inherit the attempt loop, the commit rule, the budget
// gate and the request log without a word of new executor code.
// ResponsesDialect is constructed per request, not once per route.
//
// The response object has to echo the request — tools, tool_choice and
// parallel_tool_calls are required fields with no default in the OpenAI SDK's
// model — and only ParseRequest sees the request while only the writers build
// the response. A route-scoped instance would have to hold that echo across
// concurrent requests, which is a data race and a wrong answer. Gemini's
// NewFor(r) already establishes this shape for the same reason.
type ResponsesDialect struct {
	echo *responsesEcho
}

func NewResponses() *ResponsesDialect { return &ResponsesDialect{} }

// Name is not "openai". The request row's dialect column is how an operator
// tells a Responses client from a chat client, and both speak OpenAI over the
// same auth.
func (d *ResponsesDialect) Name() string { return "openai-responses" }

func (d *ResponsesDialect) ProxyToken(r *http.Request) string {
	return (&Dialect{}).ProxyToken(r)
}

func (d *ResponsesDialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	req, pt, echo, err := ParseResponses(r, maxBody)
	// Held for the writers. Safe because this value serves exactly one request.
	d.echo = echo
	return req, pt, err
}

func (d *ResponsesDialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponses(w, resp, d.echo)
}

func (d *ResponsesDialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return WriteResponsesStream(w, events, d.echo)
}

// WriteError is chat's. The Responses error body has the same shape, and
// duplicating it would be two places to keep in step for no difference.
func (d *ResponsesDialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*ResponsesDialect)(nil)
