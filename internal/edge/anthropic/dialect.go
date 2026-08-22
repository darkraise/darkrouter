package anthropic

import (
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Dialect struct{}

func New() *Dialect { return &Dialect{} }

func (d *Dialect) Name() string { return "anthropic" }

// ProxyToken accepts both forms master design §13 lists. x-api-key is
// Anthropic's own and wins when a client sends both, which some SDKs do while
// they migrate between credential styles.
func (d *Dialect) ProxyToken(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("x-api-key")); k != "" {
		return k
	}
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
