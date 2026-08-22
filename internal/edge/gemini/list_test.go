package gemini

import (
	"encoding/json"
	"testing"
)

func TestListModelsUsesGeminiResourceNames(t *testing.T) {
	raw, err := json.Marshal(ListModels([]ModelEntry{{ID: "gemini-2.0-flash"}, {ID: "groq/llama-3.3-70b"}}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Name                       string   `json:"name"`
			BaseModelID                string   `json:"baseModelId"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			InputTokenLimit            *int     `json:"inputTokenLimit"`
		} `json:"models"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %+v", got.Models)
	}
	if got.Models[0].Name != "models/gemini-2.0-flash" {
		t.Errorf("name = %q; Gemini names are resource paths", got.Models[0].Name)
	}
	if got.Models[0].BaseModelID != "gemini-2.0-flash" {
		t.Errorf("baseModelId = %q", got.Models[0].BaseModelID)
	}
	if len(got.Models[0].SupportedGenerationMethods) == 0 {
		t.Error("clients filter on supportedGenerationMethods and hide models without it")
	}
	if got.Models[0].InputTokenLimit != nil {
		t.Error("an unknown limit is omitted; a zero limit makes clients refuse to send")
	}
	if got.Models[1].Name != "models/groq/llama-3.3-70b" {
		t.Errorf("name = %q", got.Models[1].Name)
	}
}

func TestListModelsEmitsAnEmptyArray(t *testing.T) {
	raw, err := json.Marshal(ListModels(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == `{"models":null}` || !json.Valid(raw) {
		t.Fatalf("body = %s; clients range over models unconditionally", raw)
	}
}

func TestListModelsCarriesLimitsWhenKnown(t *testing.T) {
	raw, err := json.Marshal(ListModels([]ModelEntry{
		{ID: "gemini-2.5-pro", ContextWindow: 1048576, MaxOutputTokens: 65536},
		{ID: "mystery"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Name             string `json:"name"`
			InputTokenLimit  *int   `json:"inputTokenLimit"`
			OutputTokenLimit *int   `json:"outputTokenLimit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %+v", got.Models)
	}
	if got.Models[0].InputTokenLimit == nil || *got.Models[0].InputTokenLimit != 1048576 {
		t.Errorf("input limit = %v", got.Models[0].InputTokenLimit)
	}
	if got.Models[0].OutputTokenLimit == nil || *got.Models[0].OutputTokenLimit != 65536 {
		t.Errorf("output limit = %v", got.Models[0].OutputTokenLimit)
	}
	// Absent rather than zero: a client reading a zero limit refuses to send
	// anything at all.
	if got.Models[1].InputTokenLimit != nil || got.Models[1].OutputTokenLimit != nil {
		t.Errorf("an unknown limit was emitted: %+v", got.Models[1])
	}
}
