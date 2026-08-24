package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	if f.Method == "" {
		// The model and the operation share one path segment, so without the
		// operation there is nothing to build. Defaulting to generateContent
		// would turn a client's stream into a unary call and hang it.
		return nil, errors.New("gemini: forward carries no URL operation")
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" +
		url.PathEscape(t.Model) + ":" + f.Method
	if q := f.Query.Encode(); q != "" {
		endpoint += "?" + q
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(f.Body))
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
		// The header rather than ?key=: a query parameter lands in access logs.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, nil
}

func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" {
		return adapter.RawEvent{}
	}
	var w struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string          `json:"text"`
					FunctionCall json.RawMessage `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata  *wireUsage `json:"usageMetadata"`
		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if json.Unmarshal([]byte(ev.Data), &w) != nil {
		return adapter.RawEvent{}
	}
	// Gemini's SSE defines no error event type, so a refusal arrives as a
	// chunk carrying promptFeedback.blockReason. Before commit that is the
	// provider's answer rather than content.
	if w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
		return adapter.RawEvent{ErrPayload: ev.Data}
	}
	out := adapter.RawEvent{}
	for _, c := range w.Candidates {
		for _, p := range c.Content.Parts {
			// A part carrying only a thoughtSignature yields an empty thinking
			// delta on the IR path, which is not content-bearing there either.
			if p.Text != "" || len(p.FunctionCall) > 0 {
				out.Content = true
			}
		}
	}
	if w.UsageMetadata != nil {
		u := w.UsageMetadata.toIR()
		out.Usage = &u
	}
	return out
}

func (a *Adapter) RecognizeUsage(body []byte) *ir.Usage {
	var env struct {
		UsageMetadata *wireUsage `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &env) != nil || env.UsageMetadata == nil {
		return nil
	}
	u := env.UsageMetadata.toIR()
	return &u
}

var _ adapter.Forwarder = (*Adapter)(nil)
