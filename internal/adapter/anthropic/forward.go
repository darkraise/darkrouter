package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	url := strings.TrimRight(t.BaseURL, "/") + "/messages"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(f.Body))
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
		hr.Header.Set("x-api-key", t.APIKey)
	}
	// The client's own version, when it sent one, survives. A client pinned to
	// an older wire contract is exactly who the fast path exists for, and
	// overwriting the header would change the response shape underneath it.
	if hr.Header.Get("anthropic-version") == "" {
		hr.Header.Set("anthropic-version", DefaultVersion)
	}
	return hr, nil
}

var _ adapter.Forwarder = (*Adapter)(nil)
