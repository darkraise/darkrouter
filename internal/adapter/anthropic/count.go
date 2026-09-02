package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// BuildCountRequest reuses the message rendering and strips what the counting
// endpoint rejects. Rendering the conversation twice by different rules would
// mean counting a prompt the model never sees.
func (a *Adapter) BuildCountRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	counting := *req
	counting.Stream = false
	hr, _, err := BuildRequest(ctx, t, &counting)
	if err != nil {
		return nil, err
	}

	raw, err := drainBody(hr)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// count_tokens counts input, so an output cap is meaningless, streaming has
	// nothing to stream, and an output schema shapes a response that will never
	// be generated. Anthropic rejects the first two outright; the third is
	// stripped because an unrecognized field on this endpoint is a 400 risk for
	// no benefit.
	delete(body, "max_tokens")
	delete(body, "stream")
	delete(body, "output_config")

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/messages/count_tokens"
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	out.Header = hr.Header.Clone()
	out.ContentLength = int64(len(buf))
	return out, nil
}

func (a *Adapter) ParseCountResponse(resp *http.Response) (int, error) {
	defer resp.Body.Close()
	var w struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return 0, err
	}
	return w.InputTokens, nil
}

var _ adapter.TokenCounter = (*Adapter)(nil)

// drainBody reads and closes a freshly built request's body. The request is
// discarded immediately afterwards, so nothing has to be put back.
func drainBody(hr *http.Request) ([]byte, error) {
	if hr.Body == nil {
		return []byte("{}"), nil
	}
	defer hr.Body.Close()
	return io.ReadAll(hr.Body)
}
