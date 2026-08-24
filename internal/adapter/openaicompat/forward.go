package openaicompat

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	url := strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	copyForwardHeader(hr, f.Header)
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil
}

// copyForwardHeader replays the allowlisted inbound headers. It is written once
// per kind rather than shared, because sharing it would need a package the
// three adapters could all import and there is nothing else to put there.
func copyForwardHeader(hr *http.Request, h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
}

var _ adapter.Forwarder = (*Adapter)(nil)
