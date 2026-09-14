package gemini

import (
	"regexp"
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
