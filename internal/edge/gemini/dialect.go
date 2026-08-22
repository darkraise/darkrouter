package gemini

import (
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Dialect carries the one request-scoped decision this edge has to make:
// spec §3.2 lets a client ask for streaming as SSE or as a chunked JSON array,
// and WriteStream cannot see the request that chose.
type Dialect struct{ SSE bool }

func New() *Dialect { return &Dialect{} }

// NewFor reads ?alt=sse. Both client styles exist, so the absence of the
// parameter selects the JSON-array form rather than defaulting to SSE.
func NewFor(r *http.Request) *Dialect {
	return &Dialect{SSE: r.URL.Query().Get("alt") == "sse"}
}

func (d *Dialect) Name() string { return "gemini" }

// ProxyToken accepts both forms master design §13 lists. The header wins,
// because a key in the query string ends up in access logs.
func (d *Dialect) ProxyToken(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("x-goog-api-key")); k != "" {
		return k
	}
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

func (d *Dialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	return ParseRequest(r, maxBody)
}

func (d *Dialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponse(w, resp)
}

func (d *Dialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return writeStream(w, events, d.SSE)
}

func (d *Dialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*Dialect)(nil)
