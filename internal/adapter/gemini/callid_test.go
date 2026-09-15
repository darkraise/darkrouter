package gemini

import (
	"regexp"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Anthropic and Bedrock both constrain a tool-use id to this alphabet.
var toolIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func TestParseResponseNamesCallsGeminiLeftUnidentified(t *testing.T) {
	body := `{"responseId":"r/1","candidates":[{"content":{"parts":[
	  {"functionCall":{"name":"lookup","args":{"city":"Oslo"}}},
	  {"functionCall":{"name":"lookup","args":{"city":"Rome"}}},
	  {"functionCall":{"id":"own-id","name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`
	resp, err := parseBody(t, body)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 3)
	for _, b := range resp.Content {
		if b.Type == ir.BlockToolUse {
			ids = append(ids, b.ToolUse.ID)
		}
	}
	if len(ids) != 3 || ids[2] != "own-id" {
		t.Fatalf("ids = %q; a supplied id is kept", ids)
	}
	if ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("ids = %q; each unidentified call needs its own id", ids)
	}
	for _, id := range ids[:2] {
		if !toolIDPattern.MatchString(id) {
			t.Errorf("id %q is outside the alphabet Anthropic and Bedrock accept", id)
		}
	}

	// Without a response id, two turns must still not reuse one call id: a
	// conversation replayed to Anthropic would name two tool uses alike.
	anon := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`
	first, err := parseBody(t, anon)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseBody(t, anon)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content[0].ToolUse.ID == "" || first.Content[0].ToolUse.ID == second.Content[0].ToolUse.ID {
		t.Errorf("ids = %q, %q; want distinct ids for distinct responses",
			first.Content[0].ToolUse.ID, second.Content[0].ToolUse.ID)
	}
}

func TestParseStreamNamesCallsGeminiLeftUnidentified(t *testing.T) {
	body := data(`{"responseId":"r1","candidates":[{"content":{"parts":[{"functionCall":{"name":"a","args":{}}}]}}]}`) +
		data(`{"responseId":"r1","candidates":[{"content":{"parts":[{"functionCall":{"name":"b","args":{}}}]},"finishReason":"STOP"}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	startID := map[int]string{}
	var deltaIDs []string
	for _, ev := range evs {
		if ev.Delta == nil || ev.Delta.Type != ir.BlockToolUse {
			continue
		}
		switch ev.Type {
		case ir.EventBlockStart:
			startID[ev.Index] = ev.Delta.ToolID
		case ir.EventContentDelta:
			if ev.Delta.ToolID != startID[ev.Index] {
				t.Errorf("index %d: delta id %q differs from its start's %q", ev.Index, ev.Delta.ToolID, startID[ev.Index])
			}
			deltaIDs = append(deltaIDs, ev.Delta.ToolID)
		}
	}
	if len(deltaIDs) != 2 || deltaIDs[0] == "" || deltaIDs[0] == deltaIDs[1] {
		t.Fatalf("ids = %q; each call needs its own id", deltaIDs)
	}
}

// OpenAI rejects an assistant tool call id over 40 characters, so a long
// response id must not produce one, nor may two long ids sharing a prefix
// collide once shortened.
func TestParseResponseCapsGeneratedCallIDs(t *testing.T) {
	long := strings.Repeat("a", 60)
	idOf := func(responseID string) string {
		t.Helper()
		resp, err := parseBody(t, `{"responseId":"`+responseID+`","candidates":[{"content":{"parts":[`+
			`{"functionCall":{"name":"f","args":{}}}]},"finishReason":"STOP"}]}`)
		if err != nil {
			t.Fatal(err)
		}
		return resp.Content[0].ToolUse.ID
	}
	a, b := idOf(long+"x"), idOf(long+"y")
	for _, id := range []string{a, b} {
		if len(id) > 40 || !toolIDPattern.MatchString(id) {
			t.Errorf("id %q is %d characters; want at most 40 in [A-Za-z0-9_-]", id, len(id))
		}
	}
	if a == b {
		t.Errorf("ids %q and %q collide for distinct response ids", a, b)
	}
	if short := idOf("r1"); short != "call_r1_1" {
		t.Errorf("id = %q; a short response id is used as it is", short)
	}
}
