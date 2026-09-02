package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
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

func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" {
		return adapter.RawEvent{}
	}
	var w struct {
		Type    string `json:"type"`
		Message *struct {
			Usage wireUsage `json:"usage"`
		} `json:"message"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Usage *wireUsage      `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(ev.Data), &w) != nil {
		return adapter.RawEvent{}
	}

	switch w.Type {
	case "error":
		return adapter.RawEvent{ErrPayload: ev.Data}
	case "message_start":
		if w.Message == nil {
			return adapter.RawEvent{}
		}
		u := w.Message.Usage.toIR()
		return adapter.RawEvent{Usage: &u}
	case "message_delta":
		if w.Usage == nil {
			return adapter.RawEvent{}
		}
		u := w.Usage.toIR()
		return adapter.RawEvent{Usage: &u}
	case "content_block_delta":
		if w.Delta == nil {
			return adapter.RawEvent{}
		}
		// The same rule as stream.go: a signature_delta carries no text, and
		// an empty text_delta is not content. content_block_start
		// is not here either — it maps to EventBlockStart on the IR path, and
		// committing on it would commit one event earlier than the IR path
		// does on every Anthropic stream.
		content := w.Delta.Text != "" || w.Delta.Thinking != "" || w.Delta.PartialJSON != ""
		return adapter.RawEvent{Content: content}
	default:
		return adapter.RawEvent{}
	}
}

func (a *Adapter) RecognizeUsage(body []byte) *ir.Usage {
	var env struct {
		Usage *wireUsage `json:"usage"`
	}
	if json.Unmarshal(body, &env) != nil || env.Usage == nil {
		return nil
	}
	u := env.Usage.toIR()
	return &u
}

var _ adapter.Forwarder = (*Adapter)(nil)
