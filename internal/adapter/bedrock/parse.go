package bedrock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxBody bounds what a misbehaving upstream can make the parser allocate.
const maxBody = 32 << 20

type wireConverse struct {
	Output struct {
		Message struct {
			Role    string      `json:"role"`
			Content []wireBlock `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string    `json:"stopReason"`
	Usage      wireUsage `json:"usage"`
}

type wireBlock struct {
	Text    string `json:"text"`
	ToolUse *struct {
		ToolUseID string          `json:"toolUseId"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
	} `json:"toolUse"`
	ReasoningContent *struct {
		ReasoningText *struct {
			Text      string `json:"text"`
			Signature string `json:"signature"`
		} `json:"reasoningText"`
		RedactedContent string `json:"redactedContent"`
	} `json:"reasoningContent"`
}

type wireUsage struct {
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
}

func (u wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheWriteInputTokens,
	}
}

// stopReason maps Converse's vocabulary. guardrail_intervened and
// content_filtered are the same fact to a client: the model was stopped by a
// policy rather than by the request.
func stopReason(s string) ir.StopReason {
	switch s {
	case "end_turn":
		return ir.StopEndTurn
	case "max_tokens":
		return ir.StopMaxTokens
	case "tool_use":
		return ir.StopToolUse
	case "stop_sequence":
		return ir.StopStopSequence
	case "content_filtered", "guardrail_intervened":
		return ir.StopContentFilter
	}
	return ir.StopEndTurn
}

func blockToIR(b wireBlock) (ir.ContentBlock, bool) {
	switch {
	case b.ToolUse != nil:
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ToolUse.ToolUseID, Name: b.ToolUse.Name, Input: b.ToolUse.Input,
		}}, true
	case b.ReasoningContent != nil:
		if rt := b.ReasoningContent.ReasoningText; rt != nil {
			return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
				Text: rt.Text, Signature: rt.Signature,
			}}, true
		}
		if b.ReasoningContent.RedactedContent != "" {
			return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{
				Data: b.ReasoningContent.RedactedContent,
			}}, true
		}
		return ir.ContentBlock{}, false
	case b.Text != "":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, true
	}
	return ir.ContentBlock{}, false
}

// ParseResponse takes ownership of resp.Body and always closes it.
func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read bedrock response: %w", err)
	}
	var w wireConverse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("parse bedrock response: %w", err)
	}

	out := &ir.Response{
		StopReason: stopReason(w.StopReason),
		Usage:      w.Usage.toIR(),
	}
	for _, b := range w.Output.Message.Content {
		if blk, ok := blockToIR(b); ok {
			out.Content = append(out.Content, blk)
		}
	}
	// Converse returns no id and no model name. Leaving both empty is honest;
	// the executor already knows the model it dispatched to.
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}

// ClassifyBody refines a 400. Bedrock reports a model it does not serve as a
// ValidationException rather than a 404, and treating that as fatal would kill
// a failover chain at its first provider.
func (a *Adapter) ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	base := Classify(resp, err)
	if base != adapter.OutcomeFatal || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return base
	}
	text := strings.ToLower(string(body))
	for _, marker := range []string{
		"model identifier is invalid",
		"model id is invalid",
		"could not find model",
		"don't have access to the model",
		"inference profile",
	} {
		if strings.Contains(text, marker) {
			return adapter.OutcomeRetryableModel
		}
	}
	return base
}

var _ adapter.BodyClassifier = (*Adapter)(nil)
