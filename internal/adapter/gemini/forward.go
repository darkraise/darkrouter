package gemini

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	if f.Method == "" {
		// The model and the operation share one path segment, so without the
		// operation there is nothing to build. Defaulting to generateContent
		// would turn a client's stream into a unary call and hang it.
		return nil, errors.New("gemini: forward carries no URL operation")
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" +
		url.PathEscape(t.Model) + ":" + f.Method
	if q := f.Query.Encode(); q != "" {
		endpoint += "?" + q
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	for k, vs := range f.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		// The header rather than ?key=: a query parameter lands in access logs.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, nil
}

var _ adapter.Forwarder = (*Adapter)(nil)
