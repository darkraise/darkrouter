package admin

import (
	"bytes"
	"encoding/json"
	"net/http"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
)

type playgroundBody struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream *bool  `json:"stream,omitempty"`
}

// handlePlayground runs a real request through the real executor.
//
// Anything else would verify the playground rather than the gateway: a mock
// would pass while the credential it exists to test is wrong. Going through
// exec.Handle also means the playground inherits failover, the budget gate and
// the request log for free, and the trace link works because the request really
// is in the log.
func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body playgroundBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}
	msgs := []map[string]any{}
	if body.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": body.System})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": body.Prompt})

	chat, err := json.Marshal(map[string]any{
		"model": body.Model, "messages": msgs, "stream": stream,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A synthetic request against the proxy's own dialect. The path is what the
	// OpenAI dialect expects; nothing routes on it here, but a mismatched path
	// would make this handler the one place the shape differs from production.
	pr, err := http.NewRequestWithContext(r.Context(),
		http.MethodPost, "/v1/chat/completions", bytes.NewReader(chat))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pr.Header.Set("Content-Type", "application/json")

	// exec.Handle writes X-Darkrouter-Request before anything else, which is
	// what gives the SPA the id for the trace link before the stream starts.
	s.deps.Exec.Handle(w, pr, openaiedge.New())
}
