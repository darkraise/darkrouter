package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parse(t *testing.T, body string) (*ir.Request, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req, _, err := ParseRequest(r, 1<<20)
	return req, err
}

func TestParseSimpleTextRequest(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "m" || len(req.Messages) != 1 {
		t.Fatalf("got %+v", req)
	}
	if req.Messages[0].Content[0].Text != "hi" {
		t.Fatalf("content = %+v", req.Messages[0].Content)
	}
}

func TestParseTreatsDeveloperRoleAsSystem(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"developer","content":"be brief"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Role != ir.RoleSystem {
		t.Fatalf("role = %q", req.Messages[0].Role)
	}
}

func TestParseMultiPartContentWithImage(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"what is this"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 || blocks[1].Type != ir.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !req.Needs().Vision {
		t.Fatal("expected Needs().Vision")
	}
}

func TestParseTools(t *testing.T) {
	body := `{"model":"m","messages":[],"tools":[{"type":"function","function":
		{"name":"get_weather","description":"d","parameters":{"type":"object"}}}],
		"tool_choice":"required"}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Fatalf("tool_choice = %+v", req.ToolChoice)
	}
	if !req.Needs().Tools {
		t.Fatal("expected Needs().Tools")
	}
}

func TestParseCapturesPassthroughBody(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt.Body) != body || pt.ModelField != "model" {
		t.Fatalf("passthrough = %+v", pt)
	}
}

func TestParseRejectsOversizedBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 100)))
	if _, _, err := ParseRequest(r, 10); err == nil {
		t.Fatal("expected an oversized-body error")
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := parse(t, `{"model":`); err == nil {
		t.Fatal("expected a parse error")
	}
}
