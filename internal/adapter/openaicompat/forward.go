package openaicompat

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
	url := strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	copyForwardHeader(hr, f.Header)
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil
}

// copyForwardHeader replays the allowlisted inbound headers. It is written once
// per kind rather than shared, because sharing it would need a package the
// three adapters could all import and there is nothing else to put there.
func copyForwardHeader(hr *http.Request, h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
}

// forwardChunk is the subset of a streamed chunk the recognizer reads. It is
// separate from wireChunk in parse.go on purpose: that one exists to build IR,
// this one exists to answer three questions without building anything.
type forwardChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *wireUsage      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" || ev.Data == sse.Done {
		return adapter.RawEvent{}
	}
	var c forwardChunk
	if json.Unmarshal([]byte(ev.Data), &c) != nil {
		// An unparseable event is not a reason to stop forwarding: after
		// commit the recognizer's opinion no longer matters, and before it a
		// silent chunk is not evidence of a fault.
		return adapter.RawEvent{}
	}
	if len(c.Error) > 0 && string(c.Error) != "null" {
		return adapter.RawEvent{ErrPayload: ev.Data}
	}
	out := adapter.RawEvent{}
	for _, ch := range c.Choices {
		// A name-only first tool_calls delta carries empty arguments, and a
		// literal "tool_calls": null decodes to a slice of length zero — so
		// neither trips this on its own. Phase 3's content definition is
		// ToolInput != "", and matching it here keeps this path from
		// committing one event earlier than the IR path does, the same
		// parity the anthropic recognizer holds for its own three kinds.
		toolContent := false
		for _, tc := range ch.Delta.ToolCalls {
			if tc.Function.Arguments != "" {
				toolContent = true
				break
			}
		}
		if ch.Delta.Content != "" || ch.Delta.ReasoningContent != "" || toolContent {
			out.Content = true
			break
		}
	}
	if c.Usage != nil {
		u := c.Usage.toIR()
		out.Usage = &u
		out.UsageOnly = len(c.Choices) == 0
	}
	return out
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
