package anthropic

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func parseBody(t *testing.T, body string) *ir.Response {
	t.Helper()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	out, err := ParseResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseResponseReadsBlocksAndUsage(t *testing.T) {
	got := parseBody(t, `{"id":"msg_1","model":"claude-x","stop_reason":"tool_use",
		"content":[
			{"type":"thinking","thinking":"hmm","signature":"sig-1"},
			{"type":"text","text":"calling"},
			{"type":"tool_use","id":"call_a","name":"f","input":{"x":1}}],
		"usage":{"input_tokens":10,"output_tokens":4,
			"cache_creation_input_tokens":7,"cache_read_input_tokens":3}}`)

	if len(got.Content) != 3 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Thinking == nil || got.Content[0].Thinking.Signature != "sig-1" {
		t.Errorf("thinking = %+v; the signature must survive", got.Content[0].Thinking)
	}
	if got.Content[2].ToolUse == nil || string(got.Content[2].ToolUse.Input) != `{"x":1}` {
		t.Errorf("tool_use = %+v", got.Content[2].ToolUse)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop_reason = %q", got.StopReason)
	}
	if got.Usage.CacheWriteTokens != 7 || got.Usage.CacheReadTokens != 3 {
		t.Errorf("usage = %+v; creation is a write and read is a read", got.Usage)
	}
}

func TestParseResponseReadsRedactedThinking(t *testing.T) {
	got := parseBody(t, `{"id":"m","model":"c","stop_reason":"end_turn",
		"content":[{"type":"redacted_thinking","data":"enc"}],"usage":{}}`)
	if got.Content[0].Type != ir.BlockRedactedThinking || got.Content[0].Thinking.Data != "enc" {
		t.Errorf("block = %+v", got.Content[0])
	}
}

func TestStopReasonTable(t *testing.T) {
	cases := []struct {
		in   string
		want ir.StopReason
		ok   bool
	}{
		{"end_turn", ir.StopEndTurn, true},
		{"max_tokens", ir.StopMaxTokens, true},
		{"model_context_window_exceeded", ir.StopMaxTokens, true},
		{"stop_sequence", ir.StopStopSequence, true},
		{"tool_use", ir.StopToolUse, true},
		{"refusal", ir.StopContentFilter, true},
		{"pause_turn", ir.StopPauseTurn, true},
		{"", ir.StopEndTurn, true},
		{"something_new", ir.StopEndTurn, false},
	}
	for _, tc := range cases {
		got, ok := stopReason(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("stopReason(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseResponseWarnsOnAnUnknownStopReason(t *testing.T) {
	got := parseBody(t, `{"id":"m","model":"c","stop_reason":"something_new",
		"content":[{"type":"text","text":"hi"}],"usage":{}}`)
	if got.StopReason != ir.StopEndTurn {
		t.Errorf("stop_reason = %q; an unknown value degrades rather than failing", got.StopReason)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Field != "stop_reason" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
}

func TestClassifyUsesTheSharedLadder(t *testing.T) {
	if got := Classify(&http.Response{StatusCode: 529}, nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("529 = %q; overloaded_error is retryable", got)
	}
	if got := Classify(&http.Response{StatusCode: 404}, nil); got != adapter.OutcomeRetryableModel {
		t.Errorf("404 = %q", got)
	}
}

func TestParseResponseCarriesServerToolBlocks(t *testing.T) {
	got := parseBody(t, `{"id":"msg_1","model":"claude-x","stop_reason":"end_turn",
		"content":[
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://a"}]},
			{"type":"text","text":"found it"}],
		"usage":{"input_tokens":1,"output_tokens":1}}`)
	if len(got.Content) != 3 {
		t.Fatalf("content = %+v; server tool blocks must be carried, not dropped", got.Content)
	}
	if got.Content[0].Type != "server_tool_use" || string(got.Content[0].Extra["name"]) != `"web_search"` {
		t.Errorf("server_tool_use = %+v", got.Content[0])
	}
	if got.Content[1].Type != "web_search_tool_result" || len(got.Content[1].Extra["content"]) == 0 {
		t.Errorf("web_search_tool_result = %+v", got.Content[1])
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %+v; a carried block is not a loss", got.Warnings)
	}
}
