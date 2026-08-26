package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
)

// countBody selects the counting dialect. There is no "openai": that wire has
// no counting endpoint, so an OpenAI-dialect count could only ever be the
// local estimate, and offering it would present an estimate as a reading.
type countBody struct {
	Dialect string `json:"dialect"`
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
}

func countRequest(ctx context.Context, body countBody) (*http.Request, edge.CountWriter, string, error) {
	if body.Model == "" || body.Prompt == "" {
		return nil, nil, "", errors.New("model and prompt are required")
	}
	var (
		d       edge.CountWriter
		path    string
		segment string
		payload map[string]any
	)
	switch body.Dialect {
	case "anthropic":
		d = anthropicedge.New()
		path = "/v1/messages/count_tokens"
		payload = map[string]any{
			"model":    body.Model,
			"messages": []map[string]any{{"role": "user", "content": body.Prompt}},
		}
	case "gemini":
		d = geminiedge.New()
		segment = body.Model + ":countTokens"
		path = "/v1beta/models/" + url.PathEscape(body.Model) + ":countTokens"
		payload = map[string]any{
			"contents": []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": body.Prompt}}},
			},
		}
	default:
		return nil, nil, "", errors.New("dialect must be anthropic or gemini")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, "", err
	}
	r.Header.Set("Content-Type", "application/json")
	if segment != "" {
		r.SetPathValue("model", segment)
	}
	return r, d, body.Dialect, nil
}

func (s *Server) handlePlaygroundCount(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body countBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, d, native, err := countRequest(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.deps.Exec.HandleCount(w, pr, d, native)
}
