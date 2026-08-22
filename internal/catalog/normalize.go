package catalog

import "strings"

// NormalizeModelID reduces the identifier forms three ecosystems use for the
// same model to one key.
//
// Discovery reports llama3.3:70b from Ollama and
// accounts/fireworks/models/llama-v3p3-70b from Fireworks; models.dev calls the
// family llama-3.3-70b. Without a join rule the merge fails silently — nothing
// errors, prices are simply absent everywhere and every model falls back to
// inferred capabilities.
func NormalizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	// Keep only the leaf: every known prefix form (vendor/, accounts/x/models/)
	// is a path, and the leaf is what the metadata sources key on.
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	// Ollama separates a model from its tag with a colon where every other
	// source uses a dash.
	return strings.ReplaceAll(s, ":", "-")
}

// Join resolves one model's metadata through its provider's preset.
//
// The order is: explicit alias, exact id, normalized id. The alias comes first
// because it exists precisely for the forms the rule cannot reach, and a rule
// that happened to match would otherwise silently outrank the operator.
func Join(p Preset, doc Doc, modelID string) (Metadata, bool) {
	if p.NoModelsDev || p.ModelsDevID == "" {
		return Metadata{}, false
	}
	if alias, ok := p.ModelAliases[modelID]; ok {
		if m, ok := doc.Metadata(p.ModelsDevID, alias); ok {
			return m, true
		}
	}
	if m, ok := doc.Metadata(p.ModelsDevID, modelID); ok {
		return m, true
	}
	want := NormalizeModelID(modelID)
	if want == "" {
		return Metadata{}, false
	}
	for id, m := range doc[p.ModelsDevID] {
		if NormalizeModelID(id) == want {
			return m, true
		}
	}
	return Metadata{}, false
}
