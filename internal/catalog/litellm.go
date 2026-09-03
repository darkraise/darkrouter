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

	// Upstream keys that reduce to the same model id under one provider are
	// competing listings for one model, so they are resolved together rather
	// than by last-write-wins.
	type group struct {
		provider, id string
		keys         []string
	}
	var groups []*group
	index := map[string]*group{}
	for _, key := range keys {
		e := entries[key]
		if e.Provider == "" {
			continue
		}
		id := modelIDFromKey(key)
		g, seen := index[e.Provider+"\x00"+id]
		if !seen {
			g = &group{provider: e.Provider, id: id}
			index[e.Provider+"\x00"+id] = g
			groups = append(groups, g)
		}
		g.keys = append(g.keys, key)
	}

	out := LiteLLMDoc{}
	for _, g := range groups {
		if out[g.provider] == nil {
			out[g.provider] = map[string]Pricing{}
		}
		out[g.provider][g.id] = resolveLiteLLMGroup(entries, g.keys)
	}
	return out, nil
}

// resolveLiteLLMGroup picks the price for one model from every upstream key
// that names it. Candidates are narrowed to the most-preferred cohort, and if
// that cohort still disagrees the model is left unpriced: with no canonical key
// to prefer, the index has several contradictory things to say, which is the
// same as having nothing to say. A plausible-but-wrong rate fails silently at
// billing time, while an absent one is visible and lets another source fill it.
func resolveLiteLLMGroup(entries map[string]litellmEntry, keys []string) Pricing {
	best := keys[0]
	for _, key := range keys[1:] {
		if preferredKey(key, best) {
			best = key
		}
	}
	winner := pricingOf(entries[best])
	for _, key := range keys {
		// Only the rates this parser reads decide disagreement; a difference in
		// a field it ignores is not a conflict.
		if sameCohort(key, best) {
			if p := pricingOf(entries[key]); p != winner {
				return Pricing{Source: SourceLiteLLM}
			}
		}
	}
	return winner
}

// pricingOf reads an entry only when it quotes both sides.
//
// Half an entry is not half a price: 26 upstream entries carry one cost field
// and not the other — an embedding model priced on input alone, an image model
// priced per image — and taking the missing side as zero would assert that a
// side is free when the index simply did not say. An entry with nothing to say
// contributes no candidate at all, which leaves another source free to price
// the model.
func pricingOf(e litellmEntry) Pricing {
	p := Pricing{Source: SourceLiteLLM}
	if e.InputPerToken != nil && e.OutputPerToken != nil {
		p.Known = true
		p.InputMicrosPerMTok = perMTok(*e.InputPerToken)
		p.OutputMicrosPerMTok = perMTok(*e.OutputPerToken)
	}
	return p
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

// sameCohort reports whether two keys are equally qualified — same depth, and
// alike in carrying a region. preferredKey is a total order because of its
// lexicographic tiebreak, so it can never report two keys as equal; the cohort
// is the set the tiebreak had to choose between arbitrarily, and that is the
// set whose disagreement matters.
func sameCohort(a, b string) bool {
	return strings.Count(a, "/") == strings.Count(b, "/") &&
		hasRegionSegment(a) == hasRegionSegment(b)
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
