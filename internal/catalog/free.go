package catalog

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/store"
)

// IsFreeModel reports whether one discovered model qualifies as free.
//
// Three ways, in the order they are cheap to check:
//
//  1. The id carries the OpenRouter-style `:free` suffix. A gateway that
//     publishes both tiers of a model distinguishes them exactly here.
//  2. The provider's free tier documents it, per the curated catalogue.
//  3. models.dev prices it at zero on both input and output.
//
// Nothing else. An unpriced model that no catalogue covers is NOT free:
// `Known` is what separates a model priced at zero from one nobody has priced,
// and importing the second under a free-only filter is how an operator on a
// free tier gets a bill.
//
// The curated rule is second rather than last because it outranks the price:
// a model on a vendor's free tier is free to its holder whatever the paid tier
// charges, and Groq's whole catalogue is exactly that shape. A price index
// answers "what does this cost to buy", never "is this covered by my tier".
//
// The `meta` and `known` pair is the same shape Doc.Metadata returns, so a
// caller passes its lookup straight through. `curated` is nil for a provider
// no catalogue covers.
func IsFreeModel(modelID string, meta Metadata, known bool, curated func(string) bool) bool {
	if strings.HasSuffix(modelID, ":free") {
		return true
	}
	if curated != nil && curated(modelID) {
		return true
	}
	if !known || !meta.PriceKnown {
		return false
	}
	return meta.InputMicrosPerMTok == 0 && meta.OutputMicrosPerMTok == 0
}

// SelectModelsForImport decides which of a sweep's models to keep, and names
// the ones it dropped.
//
// When freeOnly is false the list passes through untouched and nothing is
// dropped, which is the normal case: a provider that has never been narrowed
// imports exactly what it listed.
//
// The dropped ids are returned rather than counted because the catalogue has
// to act on them, not just report them: a model the operator's filter excluded
// is one the provider still lists, so leaving it to the omission sweep would
// retire it as removed_upstream — a claim about the provider that is false,
// and one that leaves the row on screen as a model this provider offers. Their
// number is also the difference between "this provider has no models" and
// "this provider has no FREE models", which need different fixes.
//
// The two rules a caller supplies are in FreeRules, both optional: a provider
// with neither has every model fall to the `:free` suffix or be dropped.
func SelectModelsForImport(
	models []store.DiscoveredModel,
	freeOnly bool,
	rules FreeRules,
) (kept []store.DiscoveredModel, dropped []string) {
	if !freeOnly {
		return models, nil
	}
	out := make([]store.DiscoveredModel, 0, len(models))
	for _, m := range models {
		meta, known := Metadata{}, false
		if rules.Price != nil {
			meta, known = rules.Price(m.ModelID)
		}
		if IsFreeModel(m.ModelID, meta, known, rules.Curated) {
			out = append(out, m)
			continue
		}
		dropped = append(dropped, m.ModelID)
	}

	// A keyless provider that matched nothing keeps everything.
	//
	// The three rules above all ask a question about money: does a price list
	// say zero, or does somebody's curated list name this model. Against a
	// provider reached with no credential there is no account to bill and no
	// price to look up — UncloseAI serves one model whose id rotates, and AI
	// Horde's roster changes as volunteers come and go, so an id-exact
	// catalogue is always behind. Dropping every model there does not protect
	// the operator from a charge that cannot happen; it just leaves them a
	// provider that routes nothing.
	//
	// Narrow on purpose: only when the filter matched nothing at all. A
	// provider whose free tier the catalogue does describe — OpenCode, whose
	// premium models answer 401 without a key — keeps being filtered, because
	// there the rules had something true to say.
	if len(out) == 0 && rules.Keyless && len(models) > 0 {
		return models, nil
	}
	return out, dropped
}

// FreeRules is what one provider's free-only import decides on.
type FreeRules struct {
	// Price looks a model id up in whatever metadata source the caller has —
	// models.dev for a catalogued provider, nil for an uncatalogued one.
	Price func(modelID string) (Metadata, bool)
	// Curated reports whether the provider's own free tier documents the
	// model. Nil for a provider the curated catalogue does not cover.
	Curated func(modelID string) bool
	// Keyless says the provider is reached with no credential of the
	// operator's. It decides only the empty case — see SelectModelsForImport.
	Keyless bool
}
