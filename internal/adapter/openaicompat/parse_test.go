package openaicompat

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func collect(t *testing.T, body string) []ir.StreamEvent {
	t.Helper()
	var got []ir.StreamEvent
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ev)
	}
	return got
}

func TestParseResponseExtractsTextAndUsage(t *testing.T) {
	body := `{"id":"x","model":"m","choices":[{"message":{"content":"hello"},
		"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	got, err := ParseResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content[0].Text != "hello" || got.StopReason != ir.StopEndTurn {
		t.Fatalf("got %+v", got)
	}
	if got.Usage.InputTokens != 2 || got.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", got.Usage)
	}
}

func TestParseResponseMapsFinishReasons(t *testing.T) {
	for wire, want := range map[string]ir.StopReason{
		"stop":           ir.StopEndTurn,
		"length":         ir.StopMaxTokens,
		"tool_calls":     ir.StopToolUse,
		"content_filter": ir.StopContentFilter,
	} {
		body := `{"choices":[{"message":{"content":""},"finish_reason":"` + wire + `"}]}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
		got, err := ParseResponse(resp)
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s -> %s, want %s", wire, got.StopReason, want)
		}
	}
}

func TestParseStreamEmitsBlockLifecycleForText(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)

	var starts, deltas, stops, msgStop int
	for _, ev := range got {
		switch ev.Type {
		case ir.EventBlockStart:
			starts++
		case ir.EventContentDelta:
			deltas++
		case ir.EventBlockStop:
			stops++
		case ir.EventMessageStop:
			msgStop++
		}
	}
	if starts != 1 || deltas != 2 || stops != 1 || msgStop != 1 {
		t.Fatalf("lifecycle counts wrong: starts=%d deltas=%d stops=%d stop=%d\n%+v",
			starts, deltas, stops, msgStop, got)
	}
}

func TestParseStreamAccumulatesToolCallFragmentsByIndex(t *testing.T) {
	// OpenAI streams tool arguments as JSON string fragments indexed by
	// tool_calls[].index; each index is its own block.
	body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\"," +
		"\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0," +
		"\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)

	var frag strings.Builder
	var name, id string
	for _, ev := range got {
		if ev.Type == ir.EventContentDelta && ev.Delta != nil && ev.Delta.Type == ir.BlockToolUse {
			frag.WriteString(ev.Delta.ToolInput)
			if ev.Delta.ToolName != "" {
				name = ev.Delta.ToolName
			}
			if ev.Delta.ToolID != "" {
				id = ev.Delta.ToolID
			}
		}
	}
	if frag.String() != `{"a":1}` || name != "f" || id != "c1" {
		t.Fatalf("fragments=%q name=%q id=%q", frag.String(), name, id)
	}
}

func TestParseStreamEmitsUsageChunk(t *testing.T) {
	body := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)
	for _, ev := range got {
		if ev.Type == ir.EventMessageDelta && ev.Usage != nil && ev.Usage.InputTokens == 5 {
			return
		}
	}
	t.Fatalf("no usage event in %+v", got)
}

func TestParseStreamIgnoresKeepaliveComments(t *testing.T) {
	body := ": OPENROUTER PROCESSING\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n"
	got := collect(t, body)
	if len(got) == 0 {
		t.Fatal("keepalive comment broke the stream")
	}
}

func TestParseStreamSurfacesUpstreamErrorPayload(t *testing.T) {
	body := "data: {\"error\":{\"message\":\"overloaded\",\"type\":\"server_error\"}}\n\n"
	var sawErr bool
	for _, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected an in-stream error to surface as a sequence error")
	}
}

func TestReasoningAndTextDoNotShareABlockIndex(t *testing.T) {
	// toolBlockBase exists because a colliding index is a bug. Reasoning was
	// left at the zero value, which is the text block's index.
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"answer"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	var thinkIdx, textIdx = -1, -1
	for ev, err := range New().ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type != ir.EventContentDelta || ev.Delta == nil {
			continue
		}
		switch ev.Delta.Type {
		case ir.BlockThinking:
			thinkIdx = ev.Index
		case ir.BlockText:
			textIdx = ev.Index
		}
	}
	if thinkIdx < 0 || textIdx < 0 {
		t.Fatalf("missing deltas: thinking at %d, text at %d", thinkIdx, textIdx)
	}
	if thinkIdx == textIdx {
		t.Errorf("reasoning and text both at index %d; a consumer keying on the index "+
			"cannot tell the two blocks apart", thinkIdx)
	}
}

func TestAReasoningBlockIsOpenedAndClosed(t *testing.T) {
	// A consumer that opens an item on block_start and finalizes it on
	// block_stop gets neither for reasoning, so the item is never closed.
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	var start, stop bool
	var startIdx, stopIdx int
	for ev, err := range New().ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case ir.EventBlockStart:
			if ev.Delta != nil && ev.Delta.Type == ir.BlockThinking {
				start, startIdx = true, ev.Index
			}
		case ir.EventBlockStop:
			stop, stopIdx = true, ev.Index
		}
	}
	if !start {
		t.Error("no content_block_start for the reasoning block")
	}
	if !stop || stopIdx != startIdx {
		t.Errorf("reasoning block opened at %d was not closed (stop=%v at %d)", startIdx, stop, stopIdx)
	}
}
