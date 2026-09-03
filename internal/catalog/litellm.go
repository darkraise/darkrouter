package catalog

import (
	"encoding/json"
	"strings"
)

// LiteLLMDoc is the price index keyed by litellm_provider then model id. The
// key in the upstream file is a display string that varies in shape, so the
// provider field is the join, not the key.
type LiteLLMDoc map[string]map[string]Pricing

type litellmEntry struct {
	Provider       string   `json:"litellm_provider"`
	InputPerToken  *float64 `json:"input_cost_per_token"`
	OutputPerToken *float64 `json:"output_cost_per_token"`
}

// perMTok converts a per-token dollar rate to micro-dollars per million
// tokens, which is how every other price in the catalog is stored.
func perMTok(rate float64) int64 { return int64(rate*1e12 + 0.5) }

func ParseLiteLLM(raw []byte) (LiteLLMDoc, error) {
	var entries map[string]litellmEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := LiteLLMDoc{}
	for key, e := range entries {
		if e.Provider == "" {
			continue
		}
		if out[e.Provider] == nil {
			out[e.Provider] = map[string]Pricing{}
		}
		p := Pricing{Source: SourceLiteLLM}
		if e.InputPerToken != nil || e.OutputPerToken != nil {
			p.Known = true
			if e.InputPerToken != nil {
				p.InputMicrosPerMTok = perMTok(*e.InputPerToken)
			}
			if e.OutputPerToken != nil {
				p.OutputMicrosPerMTok = perMTok(*e.OutputPerToken)
			}
		}
		out[e.Provider][modelIDFromKey(key)] = p
	}
	return out, nil
}

// modelIDFromKey strips the provider prefix the upstream key sometimes carries,
// so "groq/llama-3.3-70b" and "llama-3.3-70b" join the same catalog model.
func modelIDFromKey(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}
