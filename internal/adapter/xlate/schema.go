package xlate

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// JSONSchema returns a tool or response schema as JSON Schema, for a target
// that takes nothing else. A schema in Gemini's OpenAPI 3.0 subset is
// converted; any other schema, or one that does not parse, is returned as it
// is.
func JSONSchema(schema json.RawMessage, dialect string) json.RawMessage {
	if dialect != ir.SchemaOpenAPI || len(schema) == 0 {
		return schema
	}
	var v any
	if json.Unmarshal(schema, &v) != nil {
		return schema
	}
	out, err := json.Marshal(openAPIToJSONSchema(v, true))
	if err != nil {
		return schema
	}
	return out
}

// openAPIOnlyFormats are format values Google's Schema defines with an OpenAPI
// meaning JSON Schema does not share: "enum" marks a string enum, and the rest
// are OpenAPI numeric widths. A target with a format allowlist, such as
// OpenAI's strict mode, would reject them.
var openAPIOnlyFormats = map[string]bool{
	"enum": true, "int32": true, "int64": true, "float": true, "double": true,
}

// countKeywords are the Schema fields Google types as int64, which proto JSON
// may carry as strings; JSON Schema requires integers.
var countKeywords = []string{
	"minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties",
}

// openAPIToJSONSchema converts one schema object and every subschema in it.
//
// Type names are lowercased, as JSON Schema spells them; nullable becomes a
// null type (or a null branch of anyOf, or null in an enum); and the fields
// with no JSON Schema meaning — propertyOrdering, Google's own, and example,
// OpenAPI's singular form of examples — are dropped.
//
// On the root, nullable is dropped rather than rewritten: Anthropic and
// Bedrock Converse both require a tool's input schema to have type object at
// the top level, and refuse ["object", "null"].
func openAPIToJSONSchema(v any, root bool) any {
	in, ok := v.(map[string]any)
	if !ok {
		return v
	}
	s := make(map[string]any, len(in))
	for k, val := range in {
		s[k] = val
	}
	nullable, _ := s["nullable"].(bool)
	delete(s, "nullable")
	delete(s, "propertyOrdering")
	delete(s, "example")

	if t, ok := s["type"].(string); ok {
		if t == "" || t == "TYPE_UNSPECIFIED" {
			delete(s, "type")
		} else {
			s["type"] = strings.ToLower(t)
		}
	}
	if f, ok := s["format"].(string); ok && openAPIOnlyFormats[f] {
		delete(s, "format")
	}
	for _, k := range countKeywords {
		if str, ok := s[k].(string); ok {
			if n, err := strconv.ParseInt(str, 10, 64); err == nil {
				s[k] = n
			}
		}
	}

	// Google's ref and defs are JSON Schema's $ref and $defs without the
	// sigil, and a ref points into defs as "#/defs/Name". JSON Schema ignores
	// unknown keywords, so an unrenamed ref leaves its property unconstrained.
	if ref, ok := s["ref"].(string); ok {
		delete(s, "ref")
		if name, found := strings.CutPrefix(ref, "#/defs/"); found {
			ref = "#/$defs/" + name
		}
		s["$ref"] = ref
	}
	if defs, ok := s["defs"]; ok {
		delete(s, "defs")
		s["$defs"] = defs
	}

	for _, k := range []string{"properties", "$defs"} {
		if m, ok := s[k].(map[string]any); ok {
			conv := make(map[string]any, len(m))
			for name, sub := range m {
				conv[name] = openAPIToJSONSchema(sub, false)
			}
			s[k] = conv
		}
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if sub, ok := s[k]; ok {
			s[k] = openAPIToJSONSchema(sub, false)
		}
	}
	if list, ok := s["anyOf"].([]any); ok {
		conv := make([]any, 0, len(list)+1)
		for _, sub := range list {
			conv = append(conv, openAPIToJSONSchema(sub, false))
		}
		s["anyOf"] = conv
	}

	if nullable && !root {
		switch t := s["type"].(type) {
		case string:
			s["type"] = []any{t, "null"}
		case nil:
			if list, ok := s["anyOf"].([]any); ok {
				s["anyOf"] = append(list, map[string]any{"type": "null"})
			}
		}
		if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, nil) {
			s["enum"] = append(enum, nil)
		}
	}
	return s
}
