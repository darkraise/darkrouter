package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (a *Adapter) BuildSpeech(ctx context.Context, t *adapter.Target,
	req *ir.SpeechRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{"model": t.Model, "input": req.Input, "voice": req.Voice}
	// Omitted rather than sent empty: speed 0 is a 400 rather than a default,
	// and an empty response_format or stream_format is rejected outright.
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	for k, v := range map[string]string{
		"response_format": req.ResponseFormat,
		"instructions":    req.Instructions,
		"stream_format":   req.StreamFormat,
	} {
		if v != "" {
			body[k] = v
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/audio/speech"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build speech request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

var _ adapter.Speaker = (*Adapter)(nil)
