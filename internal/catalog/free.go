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
//  2. models.dev prices it at zero on both input and output.
//  3. Nothing else. An unpriced model is NOT free: `Known` is what separates
//     a model priced at zero from one nobody has priced, and importing the
//     second under a free-only filter is how an operator on a free tier gets
//     a bill.
//
// The `meta` and `known` pair is the same shape Doc.Metadata returns, so a
// caller passes its lookup straight through.
func IsFreeModel(modelID string, meta Metadata, known bool) bool {
	if strings.HasSuffix(modelID, ":free") {
		return true
	}
	if !known || !meta.PriceKnown {
		return false
	}
	return meta.InputMicrosPerMTok == 0 && meta.OutputMicrosPerMTok == 0
}

// SelectModelsForImport decides which of a sweep's models to keep, and reports
// how many it dropped.
//
// When freeOnly is false the list passes through untouched and nothing is
// dropped, which is the normal case: a provider that has never been narrowed
// imports exactly what it listed.
//
// The dropped count is the difference between "this provider has no models"
// and "this provider has no FREE models", and those need different fixes —
// one is a broken listing endpoint, the other is a filter doing its job. A
// sweep that silently records nothing leaves an operator unable to tell them
// apart.
//
// `price` looks a model id up in whatever metadata source the caller has —
// models.dev for a catalogued provider, nothing at all for an uncatalogued
// one, where every model falls to the `:free` suffix or is dropped.
func SelectModelsForImport(
	models []store.DiscoveredModel,
	freeOnly bool,
	price func(modelID string) (Metadata, bool),
) (kept []store.DiscoveredModel, dropped int) {
	if !freeOnly {
		return models, 0
	}
	out := make([]store.DiscoveredModel, 0, len(models))
	for _, m := range models {
		meta, known := Metadata{}, false
		if price != nil {
			meta, known = price(m.ModelID)
		}
		if IsFreeModel(m.ModelID, meta, known) {
			out = append(out, m)
		}
	}
	return out, len(models) - len(out)
}
