package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// OpenAI rejects a function whose parameters use OpenAPI's uppercase type
// names ("'STRING' is not valid under any of the given schemas"), which is how
// a Gemini client writes them.
func TestOpenAPISchemasReachOpenAIAsJSONSchema(t *testing.T) {
	const schema = `{"type":"OBJECT","properties":{"q":{"type":"STRING","nullable":true}}}`
	body, _ := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Tools:    []ir.Tool{{Name: "f", Schema: json.RawMessage(schema), SchemaDialect: ir.SchemaOpenAPI}},
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Schema: json.RawMessage(schema), SchemaDialect: ir.SchemaOpenAPI,
		},
	})
	check := func(where string, s map[string]any) {
		t.Helper()
		q, _ := s["properties"].(map[string]any)["q"].(map[string]any)
		qt, _ := q["type"].([]any)
		if s["type"] != "object" || len(qt) != 2 || qt[0] != "string" || qt[1] != "null" {
			t.Errorf("%s = %v, want JSON Schema", where, s)
		}
	}
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	check("parameters", fn["parameters"].(map[string]any))
	rf := body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	check("response_format.json_schema.schema", rf["schema"].(map[string]any))
}
