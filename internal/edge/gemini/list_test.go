package gemini

import (
	"encoding/json"
	"testing"
)

func TestListModelsUsesGeminiResourceNames(t *testing.T) {
	raw, err := json.Marshal(ListModels([]string{"gemini-2.0-flash", "groq/llama-3.3-70b"}))
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
