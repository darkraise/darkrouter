package catalog

import (
	"encoding/json"
	"regexp"
	"sort"
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
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := LiteLLMDoc{}
	// The key that supplied each stored price, so a later collision can be
	// judged against it instead of overwriting blindly.
	from := map[string]map[string]string{}
	for _, key := range keys {
		e := entries[key]
		if e.Provider == "" {
			continue
		}
		if out[e.Provider] == nil {
			out[e.Provider] = map[string]Pricing{}
			from[e.Provider] = map[string]string{}
		}
		id := modelIDFromKey(key)
		if held, taken := from[e.Provider][id]; taken && !preferredKey(key, held) {
			continue
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
		out[e.Provider][id] = p
		from[e.Provider][id] = key
	}
	return out, nil
}

// awsRegion matches an AWS region qualifier by shape — two lowercase letters,
// one or more hyphenated words, then a trailing digit — so "us-east-1",
// "eu-west-2", "ap-northeast-1" and "us-gov-east-1" all match. Matching by
// shape rather than by an enumerated list is deliberate: a region added
// upstream would fall through a list silently and reintroduce the very bug
// this guards against.
var awsRegion = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

func hasRegionSegment(key string) bool {
	for _, seg := range strings.Split(key, "/") {
		if awsRegion.MatchString(seg) {
			return true
		}
	}
	return false
}

// preferredKey reports whether candidate should displace held when both reduce
// to the same model id. The least-qualified key wins: it is the vendor's
// canonical listing, and each extra segment qualifies it into a regional or
// endpoint variant whose rate can differ materially from the headline one.
//
// Among keys of equal depth a region qualifier loses, because a regional or
// GovCloud listing is a variant of the canonical one and is priced differently.
// Leaving that to alphabetical order would decide it on where the region name
// happens to sort, which is arbitrary rather than wrong-by-design.
//
// Lexicographic order breaks the remaining tie so the outcome is total, and
// with it the parsed document is a function of its input rather than of map
// iteration order.
func preferredKey(candidate, held string) bool {
	if c, h := strings.Count(candidate, "/"), strings.Count(held, "/"); c != h {
		return c < h
	}
	if c, h := hasRegionSegment(candidate), hasRegionSegment(held); c != h {
		return !c
	}
	return candidate < held
}

// modelIDFromKey strips the provider prefix the upstream key sometimes carries,
// so "groq/llama-3.3-70b" and "llama-3.3-70b" join the same catalog model.
func modelIDFromKey(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}
