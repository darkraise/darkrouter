package gemini

// generationMethods is what Darkrouter serves for every chat model. Clients
// filter on this list, and one that omits it shows no models at all.
var generationMethods = []string{"generateContent", "streamGenerateContent", "countTokens"}

// ListModels renders Gemini's listing shape.
//
// inputTokenLimit and outputTokenLimit are omitted rather than zeroed: the
// catalog does not know them until Phase 6, and a client reading a zero limit
// refuses to send anything at all.
func ListModels(models []string) map[string]any {
	out := []any{}
	for _, m := range models {
		out = append(out, map[string]any{
			"name":                       "models/" + m,
			"baseModelId":                m,
			"displayName":                m,
			"supportedGenerationMethods": generationMethods,
		})
	}
	return map[string]any{"models": out}
}
