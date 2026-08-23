package catalog

import (
	"github.com/darkraise/darkrouter/internal/store"
)

// SeedFromPreset builds catalog rows for a kind with no listing endpoint.
//
// Spec §4.3: Vertex offers no practical API for listing which models a project
// may actually call — Model Garden enumeration is noisy and partner entitlement
// is not cleanly queryable. Seeding from models.dev, filtered by the publisher
// the provider's preset declares, is the honest alternative to pretending
// discovery works.
//
// A preset with no publisher seeds nothing. That is what keeps this from
// fighting the discovery worker on every kind that does have a listing.
func SeedFromPreset(preset Preset, doc Doc) []store.DiscoveredModel {
	if preset.Publisher == "" {
		return nil
	}
	ids := doc.ModelsFor(preset.ModelsDevID)
	out := make([]store.DiscoveredModel, 0, len(ids))
	for _, id := range ids {
		meta, ok := doc.Metadata(preset.ModelsDevID, id)
		if !ok {
			continue
		}
		out = append(out, store.DiscoveredModel{
			ModelID:         id,
			Publisher:       preset.Publisher,
			ContextWindow:   meta.ContextWindow,
			MaxOutputTokens: meta.MaxOutputTokens,
		})
	}
	return out
}
