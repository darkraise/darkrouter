package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/store"
)

// BuildCapabilityRequest asks a local runtime what one model can do.
//
// Ollama's /api/show sits beside its OpenAI-compatible surface rather than
// under it, so the /v1 suffix comes off. The model id is sent exactly as
// discovery reported it: normalizing the tag separator here would ask about a
// model the runtime does not have.
func BuildCapabilityRequest(ctx context.Context, p Probe, modelID string) (*http.Request, error) {
	root := strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/v1")
	body, err := json.Marshal(map[string]string{"model": modelID})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build capability request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	applyAuth(r, p)
	return r, nil
}

// ParseOllamaShow reads what the runtime reported.
//
// The bool is "the runtime said something", not "the model can do something".
// A response carrying neither signal must not become a confident all-false: it
// would outrank models.dev in the merge and mark a capable model incapable.
func ParseOllamaShow(body []byte) (store.ModelCapabilities, bool) {
	var doc struct {
		Capabilities []string `json:"capabilities"`
		Template     string   `json:"template"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return store.ModelCapabilities{}, false
	}

	if len(doc.Capabilities) > 0 {
		var caps store.ModelCapabilities
		for _, c := range doc.Capabilities {
			switch strings.ToLower(c) {
			case "tools":
				caps.Tools = true
			case "vision":
				caps.Vision = true
			case "thinking", "reasoning":
				caps.Reasoning = true
			}
		}
		return caps, true
	}

	// Older builds report no array. The template is the only evidence, and it
	// is exactly the signal spec §6 describes: a template that renders .Tools
	// is a template for a model that takes them.
	if doc.Template != "" {
		return store.ModelCapabilities{Tools: strings.Contains(doc.Template, ".Tools")}, true
	}
	return store.ModelCapabilities{}, false
}
