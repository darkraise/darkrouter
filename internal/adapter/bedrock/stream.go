package bedrock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireStreamDelta struct {
	Text    string `json:"text"`
	ToolUse *struct {
		Input string `json:"input"`
	} `json:"toolUse"`
	ReasoningContent *struct {
		Text            string `json:"text"`
		Signature       string `json:"signature"`
		RedactedContent string `json:"redactedContent"`
	} `json:"reasoningContent"`
}

type wireStreamEvent struct {
	Role              string `json:"role"`
	ContentBlockIndex int    `json:"contentBlockIndex"`
	Start             *struct {
		ToolUse *struct {
			ToolUseID string `json:"toolUseId"`
			Name      string `json:"name"`
		} `json:"toolUse"`
	} `json:"start"`
	Delta      *wireStreamDelta `json:"delta"`
	StopReason string           `json:"stopReason"`
	Usage      *wireUsage       `json:"usage"`
}

// ParseStream decodes AWS binary eventstream framing into IR events.
//
// maxLine is ignored: it bounds an SSE line, and an eventstream frame carries
// its own length prefix which the decoder validates against a CRC. The
// parameter stays because adapter.Adapter requires it.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	_ = maxLine
	return func(yield func(ir.StreamEvent, error) bool) {
		dec := eventstream.NewDecoder()
		// Reused across frames. The decoder grows it as needed and returns a
		// Message whose Payload aliases it, so anything kept beyond one
		// iteration must be copied — which is why the JSON is unmarshalled
		// immediately rather than stashed.
		payload := make([]byte, 0, 8<<10)

		for {
			msg, err := dec.Decode(r, payload)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				yield(ir.StreamEvent{}, fmt.Errorf("decode eventstream frame: %w", err))
				return
			}

			switch headerValue(msg.Headers, ":message-type") {
			case "exception":
				// The status line was 200 and the failure arrived later. The
				// executor reclassifies from this error, so naming the
				// exception type matters.
				yield(ir.StreamEvent{}, fmt.Errorf("bedrock stream exception %s: %s",
					headerValue(msg.Headers, ":exception-type"), msg.Payload))
				return
			case "error":
				yield(ir.StreamEvent{}, fmt.Errorf("bedrock stream error %s: %s",
					headerValue(msg.Headers, ":error-code"), msg.Payload))
				return
			}

			evs, err := decodeEvent(headerValue(msg.Headers, ":event-type"), msg.Payload)
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			for _, ev := range evs {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
}

func headerValue(h eventstream.Headers, name string) string {
	v := h.Get(name)
	if v == nil {
		return ""
	}
	s, ok := v.(eventstream.StringValue)
	if !ok {
		return ""
	}
	return string(s)
}

// decodeEvent maps one Converse stream event to zero or more IR events. Zero is
// a real answer: contentBlockStart for a text block carries nothing the IR does
// not already have.
func decodeEvent(eventType string, payload []byte) ([]ir.StreamEvent, error) {
	var w wireStreamEvent
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &w); err != nil {
			return nil, fmt.Errorf("parse %s payload: %w", eventType, err)
		}
	}

	switch eventType {
	case "messageStart":
		return []ir.StreamEvent{{Type: ir.EventMessageStart}}, nil

	case "contentBlockStart":
		if w.Start != nil && w.Start.ToolUse != nil {
			return []ir.StreamEvent{{
				Type:  ir.EventBlockStart,
				Index: w.ContentBlockIndex,
				Delta: &ir.Delta{
					Type:     ir.BlockToolUse,
					ToolID:   w.Start.ToolUse.ToolUseID,
					ToolName: w.Start.ToolUse.Name,
				},
			}}, nil
		}
		return []ir.StreamEvent{{Type: ir.EventBlockStart, Index: w.ContentBlockIndex}}, nil

	case "contentBlockDelta":
		if w.Delta == nil {
			return nil, nil
		}
		switch {
		case w.Delta.ToolUse != nil:
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{Type: ir.BlockToolUse, ToolInput: w.Delta.ToolUse.Input},
			}}, nil
		case w.Delta.ReasoningContent != nil && w.Delta.ReasoningContent.RedactedContent != "":
			// The IR delta has no field of its own for a redacted payload;
			// it rides in Thinking under the redacted block kind, which is
			// what tells a writer not to show it as reasoning text.
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{
					Type:     ir.BlockRedactedThinking,
					Thinking: w.Delta.ReasoningContent.RedactedContent,
				},
			}}, nil
		case w.Delta.ReasoningContent != nil:
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{
					Type:      ir.BlockThinking,
					Thinking:  w.Delta.ReasoningContent.Text,
					Signature: w.Delta.ReasoningContent.Signature,
				},
			}}, nil
		case w.Delta.Text != "":
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{Type: ir.BlockText, Text: w.Delta.Text},
			}}, nil
		}
		return nil, nil

	case "contentBlockStop":
		return []ir.StreamEvent{{Type: ir.EventBlockStop, Index: w.ContentBlockIndex}}, nil

	case "messageStop":
		return []ir.StreamEvent{{
			Type: ir.EventMessageStop, StopReason: stopReason(w.StopReason),
		}}, nil

	case "metadata":
		// Usage arrives after messageStop, exactly as it does for Groq's
		// OpenAI-compatible stream. Completing on messageStop would report
		// zero tokens for every streamed Bedrock request.
		if w.Usage == nil {
			return nil, nil
		}
		u := w.Usage.toIR()
		return []ir.StreamEvent{{Type: ir.EventMessageDelta, Usage: &u}}, nil
	}
	return nil, nil
}
