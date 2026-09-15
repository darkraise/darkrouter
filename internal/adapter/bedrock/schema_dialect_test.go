package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Converse requires a JSON Schema whose top-level type is object; a Gemini
// client's OpenAPI schema says OBJECT.
func TestOpenAPISchemasReachConverseAsJSONSchema(t *testing.T) {
	req := simple()
	req.Tools = []ir.Tool{{
		Name: "f", SchemaDialect: ir.SchemaOpenAPI,
		Schema: json.RawMessage(`{"type":"OBJECT","properties":{"q":{"type":"STRING","nullable":true}}}`),
	}}
	body, _, _ := build(t, anthropicTarget(req.Model), req)
	spec := body["toolConfig"].(map[string]any)["tools"].([]any)[0].(map[string]any)["toolSpec"].(map[string]any)
	schema := spec["inputSchema"].(map[string]any)["json"].(map[string]any)
	q, _ := schema["properties"].(map[string]any)["q"].(map[string]any)
	qt, _ := q["type"].([]any)
	if schema["type"] != "object" || len(qt) != 2 || qt[0] != "string" || qt[1] != "null" {
		t.Errorf("inputSchema.json = %v, want JSON Schema", schema)
	}
}
