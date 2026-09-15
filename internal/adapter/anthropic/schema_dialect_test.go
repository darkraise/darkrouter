package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

const openAPISchema = `{"type":"OBJECT","properties":{"q":{"type":"STRING","nullable":true}},"propertyOrdering":["q"]}`

// A Gemini client's schema is OpenAPI: Anthropic validates input_schema as
// JSON Schema, whose type names are lowercase.
func TestOpenAPISchemasReachAnthropicAsJSONSchema(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Tools:    []ir.Tool{{Name: "f", Schema: json.RawMessage(openAPISchema), SchemaDialect: ir.SchemaOpenAPI}},
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Schema: json.RawMessage(openAPISchema), SchemaDialect: ir.SchemaOpenAPI,
		},
	})
	check := func(where string, schema map[string]any) {
		t.Helper()
		q, _ := schema["properties"].(map[string]any)["q"].(map[string]any)
		qt, _ := q["type"].([]any)
		if schema["type"] != "object" || len(qt) != 2 || qt[0] != "string" || qt[1] != "null" {
			t.Errorf("%s = %v, want JSON Schema", where, schema)
		}
		if _, ok := schema["propertyOrdering"]; ok {
			t.Errorf("%s = %v; propertyOrdering is Gemini's own", where, schema)
		}
	}
	check("input_schema", body["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any))
	check("output_config.format.schema",
		body["output_config"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any))
}
