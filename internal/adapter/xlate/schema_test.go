package xlate

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestJSONSchemaConvertsGeminisOpenAPISubset(t *testing.T) {
	in := `{
	  "type": "OBJECT",
	  "propertyOrdering": ["name", "tags", "kind", "when", "size", "choice", "any"],
	  "example": {"name": "x"},
	  "required": ["name"],
	  "properties": {
	    "name": {"type": "STRING", "nullable": true, "description": "who"},
	    "tags": {"type": "ARRAY", "minItems": "1", "maxItems": "5", "items": {"type": "STRING", "maxLength": "10"}},
	    "kind": {"type": "STRING", "format": "enum", "enum": ["a", "b"], "nullable": true},
	    "when": {"type": "STRING", "format": "date-time", "nullable": false},
	    "size": {"type": "INTEGER", "format": "int64", "minimum": 0},
	    "choice": {"anyOf": [{"type": "NUMBER"}, {"type": "BOOLEAN"}], "nullable": true},
	    "any": {"type": "TYPE_UNSPECIFIED"}
	  }
	}`
	want := `{
	  "type": "object",
	  "required": ["name"],
	  "properties": {
	    "name": {"type": ["string", "null"], "description": "who"},
	    "tags": {"type": "array", "minItems": 1, "maxItems": 5, "items": {"type": "string", "maxLength": 10}},
	    "kind": {"type": ["string", "null"], "enum": ["a", "b", null]},
	    "when": {"type": "string", "format": "date-time"},
	    "size": {"type": "integer", "minimum": 0},
	    "choice": {"anyOf": [{"type": "number"}, {"type": "boolean"}, {"type": "null"}]},
	    "any": {}
	  }
	}`
	got := JSONSchema(json.RawMessage(in), ir.SchemaOpenAPI)
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSONSchema =\n%s\nwant\n%s", got, want)
	}
}

func TestJSONSchemaLeavesJSONSchemaAlone(t *testing.T) {
	in := json.RawMessage(`{"type":"OBJECT","nullable":true}`)
	if got := JSONSchema(in, ""); string(got) != string(in) {
		t.Errorf("JSONSchema = %s; a schema not marked OpenAPI is passed through", got)
	}
	if got := JSONSchema(nil, ir.SchemaOpenAPI); got != nil {
		t.Errorf("JSONSchema(nil) = %s", got)
	}
}
