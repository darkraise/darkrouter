package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildModerationRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildModeration(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "omni-moderation-latest"},
		&ir.ModerationRequest{Input: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/moderations" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "omni-moderation-latest" {
		t.Errorf("model = %v; the target's name must be sent", body["model"])
	}
	if in, ok := body["input"].([]any); !ok || len(in) != 2 {
		t.Errorf("input = %v", body["input"])
	}
}

func TestParseModerationKeepsUnknownCategories(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"modr-1","model":"m","results":[
		  {"flagged":true,
		   "categories":{"hate":false,"invented-later":true},
		   "category_scores":{"hate":0.01,"invented-later":0.99}}]}`)),
	}
	out, err := New().ParseModeration(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "modr-1" || len(out.Results) != 1 || !out.Results[0].Flagged {
		t.Fatalf("response = %+v", out)
	}
	if !out.Results[0].Categories["invented-later"] {
		t.Errorf("categories = %v", out.Results[0].Categories)
	}
	if out.Results[0].Scores["invented-later"] != 0.99 {
		t.Errorf("scores = %v", out.Results[0].Scores)
	}
}

func TestParseModerationKeepsAppliedInputTypes(t *testing.T) {
	// Without it a client cannot tell a flagged image from flagged text.
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"m","model":"m","results":[
		  {"flagged":true,"categories":{"violence":true},"category_scores":{"violence":0.9},
		   "category_applied_input_types":{"violence":["image"]}}]}`)),
	}
	out, err := New().ParseModeration(resp)
	if err != nil {
		t.Fatal(err)
	}
	got := out.Results[0].AppliedInputTypes["violence"]
	if len(got) != 1 || got[0] != "image" {
		t.Errorf("applied input types = %v", out.Results[0].AppliedInputTypes)
	}
}

func TestParseModerationRejectsAnEmptyResultSet(t *testing.T) {
	// A 200 with no results is a provider fault: the client asked about input
	// it got no verdict on, and returning it as success hides that.
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"id":"m","model":"m","results":[]}`)),
	}
	if _, err := New().ParseModeration(resp); err == nil {
		t.Fatal("a verdict-free 200 parsed cleanly")
	}
}
