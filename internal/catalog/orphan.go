package catalog

import (
	"fmt"
	"sort"

	"github.com/darkraise/darkrouter/internal/provider"
)

// OrphanedPresets reports providers whose preset this binary no longer ships.
//
// Spec §3.3: a binary upgrade can rename or remove a preset that provider rows
// reference. Such a provider degrades to its stored kind and base URL rather
// than failing to load — silently removing a working provider on upgrade is the
// failure mode to avoid.
//
// The degradation itself is free, because provider rows carry their own kind
// and base URL. What is not free is noticing: without this warning an operator
// loses the preset's quirks, surfaces and model traits on upgrade and has
// nothing to tell them why a request shape changed.
func OrphanedPresets(providers []provider.Provider, presets Presets) []string {
	var orphans []provider.Provider
	for _, p := range providers {
		if p.Preset == "" {
			continue // an uncatalogued provider references nothing
		}
		if _, ok := presets[p.Preset]; ok {
			continue
		}
		orphans = append(orphans, p)
	}
	// Sorted so two startups on the same database produce the same /healthz
	// text; unsorted, the warnings look like they are changing when nothing is.
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })

	out := make([]string, 0, len(orphans))
	for _, p := range orphans {
		out = append(out, fmt.Sprintf(
			"provider %q references preset %q, which this build no longer ships; "+
				"serving with its stored kind %q and base url %q, without the preset's "+
				"quirks, surfaces, or model traits",
			p.ID, p.Preset, p.Kind, p.BaseURL))
	}
	return out
}
