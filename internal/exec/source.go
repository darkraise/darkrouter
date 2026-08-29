package exec

import (
	"context"
	"net/http"

	"github.com/darkraise/darkrouter/internal/store"
)

// sourceKey carries the origin of a request through to the log writer.
//
// A context value rather than a parameter on Handle: every route already
// carries the request, the value is request-scoped by definition, and threading
// it as an argument would change the signature of the whole surface for a field
// only the record cares about.
type sourceKey struct{}

// WithSource marks a request as coming from somewhere other than the proxy.
//
// The console's playground and a provider's test drawer send real requests
// through the real executor — that is what makes them worth trusting — and so
// they land in the same log as production traffic. Marking them is what lets an
// operator reading a provider's log tell the two apart.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// SourceFrom reads the origin, defaulting to the proxy. Anything that reaches
// the executor without saying otherwise came through the gateway's front door.
func SourceFrom(ctx context.Context) string {
	if s, ok := ctx.Value(sourceKey{}).(string); ok && s != "" {
		return s
	}
	return store.SourceProxy
}

// sourceOfRequest is the same read, from a request that may be nil — the
// records built before parsing have one, but a test constructing an executor by
// hand may not.
func sourceOfRequest(r *http.Request) string {
	if r == nil {
		return store.SourceProxy
	}
	return SourceFrom(r.Context())
}
