package gemini

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// BuildCountRequest wraps the generateContent body in generateContentRequest.
//
// countTokens accepts only contents or a whole generateContentRequest at its
// top level; a bare systemInstruction or tools field is a 400. Wrapping the
// same body the model would see counts the system instruction and the tool
// declarations too, which a contents-only count leaves out.
func (a *Adapter) BuildCountRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	counting := *req
	counting.Stream = false
	body, _, err := a.f.renderBody(ctx, t, &counting)
	if err != nil {
		return nil, err
	}
	body["model"] = "models/" + t.Model
	buf, err := json.Marshal(map[string]any{"generateContentRequest": body})
	if err != nil {
		return nil, err
	}
	return newRequest(ctx, t, modelEndpoint(t, ":countTokens"), buf)
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
