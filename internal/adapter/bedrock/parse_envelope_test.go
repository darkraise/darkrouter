package bedrock

import "testing"

func TestParseResponseRejectsABodyWithNoOutput(t *testing.T) {
	if _, err := ParseResponse(respWith(200, `{}`)); err == nil {
		t.Fatal("an object with no output message and no stop reason is not a completion")
	}
}

func TestParseResponseAcceptsAnEmptyAssistantMessage(t *testing.T) {
	body := `{"output":{"message":{"role":"assistant","content":[]}},"stopReason":"end_turn"}`
	if _, err := ParseResponse(respWith(200, body)); err != nil {
		t.Fatalf("err = %v; empty content is a legitimate answer", err)
	}
}
