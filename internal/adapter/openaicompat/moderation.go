package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxModerationBytes bounds the response read. A verdict set is small; an
// unbounded read from a misbehaving provider is the hazard max_body_bytes
// prevents inbound and nothing was preventing outbound.
const maxModerationBytes = 4 << 20

func (a *Adapter) BuildModeration(ctx context.Context, t *adapter.Target,
	req *ir.ModerationRequest) (*http.Request, []ir.Warning, error) {

	buf, err := json.Marshal(map[string]any{"model": t.Model, "input": req.Input})
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/moderations"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build moderation request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

type moderationEnvelope struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Results []struct {
		Flagged    bool                `json:"flagged"`
		Categories map[string]bool     `json:"categories"`
		Scores     map[string]float64  `json:"category_scores"`
		Applied    map[string][]string `json:"category_applied_input_types"`
	} `json:"results"`
}

func (a *Adapter) ParseModeration(resp *http.Response) (*ir.ModerationResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModerationBytes))
	if err != nil {
		return nil, fmt.Errorf("read moderation response: %w", err)
	}
	var env moderationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse moderation response: %w", err)
	}
	if len(env.Results) == 0 {
		// A 200 with no verdict is a provider fault: the client asked about
		// input it got no answer on, and reporting success hides that.
		return nil, errors.New("moderation response carried no results")
	}
	out := &ir.ModerationResponse{
		ID: env.ID, Model: env.Model,
		Results: make([]ir.ModerationResult, 0, len(env.Results)),
	}
	for _, r := range env.Results {
		out.Results = append(out.Results, ir.ModerationResult{
			Flagged: r.Flagged, Categories: r.Categories, Scores: r.Scores,
			AppliedInputTypes: r.Applied,
		})
	}
	return out, nil
}

var _ adapter.Moderator = (*Adapter)(nil)
