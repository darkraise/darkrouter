package gemini

// generationMethods is what Darkrouter serves for every chat model. Clients
// filter on this list, and one that omits it shows no models at all.
var generationMethods = []string{"generateContent", "streamGenerateContent", "countTokens"}

// ModelEntry is one model to list. Zero limits mean the catalog does not know
// them.
type ModelEntry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
}

// ListModels renders Gemini's listing shape.
//
// A limit the catalog does not know is omitted rather than zeroed: a client
// reading inputTokenLimit: 0 refuses to send anything at all.
func ListModels(models []ModelEntry) map[string]any {
	out := []any{}
	for _, m := range models {
		entry := map[string]any{
			"name":                       "models/" + m.ID,
			"baseModelId":                m.ID,
			"displayName":                m.ID,
			"supportedGenerationMethods": generationMethods,
		}
		if m.ContextWindow > 0 {
			entry["inputTokenLimit"] = m.ContextWindow
		}
		if m.MaxOutputTokens > 0 {
			entry["outputTokenLimit"] = m.MaxOutputTokens
		}
		out = append(out, entry)
	}
	return map[string]any{"models": out}
}
