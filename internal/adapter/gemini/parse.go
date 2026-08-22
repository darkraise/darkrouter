package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wirePart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	InlineData       *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type wireCandidate struct {
	Content struct {
		Role  string     `json:"role"`
		Parts []wirePart `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type wireUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:     u.PromptTokenCount,
		OutputTokens:    u.CandidatesTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
		ReasoningTokens: u.ThoughtsTokenCount,
	}
}

type wireResponse struct {
	ResponseID     string          `json:"responseId"`
	ModelVersion   string          `json:"modelVersion"`
	Candidates     []wireCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata wireUsage `json:"usageMetadata"`
}

// finishReason maps Gemini's enum. hasCall carries the one thing the enum does
// not say: Gemini reports STOP for a turn that called a tool, and an agentic
// client that reads that as end_turn stops mid-task.
//
// The bool reports whether the value was recognized. An unknown one degrades to
// end_turn with a warning rather than failing, because the enum grows.
func finishReason(s string, hasCall bool) (ir.StopReason, bool) {
	switch s {
	case "", "FINISH_REASON_UNSPECIFIED", "STOP":
		if hasCall {
			return ir.StopToolUse, true
		}
		return ir.StopEndTurn, true
	case "MAX_TOKENS":
		return ir.StopMaxTokens, true
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "IMAGE_SAFETY":
		return ir.StopContentFilter, true
	case "MALFORMED_FUNCTION_CALL", "OTHER", "LANGUAGE":
		return ir.StopError, true
	default:
		return ir.StopEndTurn, false
	}
}

func partToIR(p wirePart) (ir.ContentBlock, bool) {
	switch {
	case p.FunctionCall != nil:
		args := p.FunctionCall.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: p.FunctionCall.ID, Name: p.FunctionCall.Name, Input: args,
		}}, true
	case p.Thought:
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: p.Text, Signature: p.ThoughtSignature,
		}}, true
	case p.InlineData != nil:
		return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{
			MIME: p.InlineData.MimeType, Data: p.InlineData.Data,
		}}, true
	case p.Text != "":
		return ir.ContentBlock{Type: ir.BlockText, Text: p.Text}, true
	default:
		return ir.ContentBlock{}, false
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w wireResponse
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}

	// A blocked prompt carries no candidates and no finish reason at all.
	// Reporting it as an empty success is the failure review finding F12 names.
	if len(w.Candidates) == 0 && w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
		return nil, &ir.Error{
			Type:    ir.ErrContentFilter,
			Message: "the prompt was blocked: " + w.PromptFeedback.BlockReason,
			Code:    w.PromptFeedback.BlockReason,
		}
	}

	out := &ir.Response{
		ID: w.ResponseID, Model: w.ModelVersion,
		Usage: w.UsageMetadata.toIR(), StopReason: ir.StopEndTurn,
	}
	if len(w.Candidates) == 0 {
		return out, nil
	}

	c := w.Candidates[0]
	hasCall := false
	for _, p := range c.Content.Parts {
		blk, ok := partToIR(p)
		if !ok {
			continue
		}
		if blk.Type == ir.BlockToolUse {
			hasCall = true
		}
		out.Content = append(out.Content, blk)
	}

	sr, known := finishReason(c.FinishReason, hasCall)
	out.StopReason = sr
	if !known {
		out.Warnings = append(out.Warnings, ir.Warning{
			Field: "finishReason", Target: targetName,
			Reason: "unrecognized value " + c.FinishReason + "; reported as end_turn",
		})
	}
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}
