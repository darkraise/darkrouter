package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// BuildCountRequest reuses the request builder and rewrites the method. The
// body shape is identical, so counting the same rendering the model would see
// is free here — unlike Anthropic, where two fields have to come back out.
func (a *Adapter) BuildCountRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	counting := *req
	counting.Stream = false
	hr, _, err := a.f.BuildRequest(ctx, t, &counting)
	if err != nil {
		return nil, err
	}
	hr.URL.Path = strings.TrimSuffix(hr.URL.Path, ":generateContent") + ":countTokens"
	hr.URL.RawQuery = ""
	return hr, nil
}

func (a *Adapter) ParseCountResponse(resp *http.Response) (int, error) {
	defer resp.Body.Close()
	var w struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return 0, err
	}
	return w.TotalTokens, nil
}

var _ adapter.TokenCounter = (*Adapter)(nil)
