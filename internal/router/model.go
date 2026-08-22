package router

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// resolveModel expands a requested name into ordered targets, and reports the
// alias it matched if any. An empty result means the name is unresolvable.
//
// Order is: exact alias, then provider/model, then bare name. First match wins.
func resolveModel(name string, aliases map[string][]string,
	providers map[string]bool, cat catalog.Reader) ([]target, string) {

	if entries, ok := aliases[name]; ok {
		var out []target
		for _, entry := range entries {
			// Alias targets go through rules 2 and 3 only. Following a nested
			// alias would need cycle detection for a feature nobody asked for.
			out = append(out, resolveDirect(entry, providers, cat)...)
		}
		return out, name
	}
	return resolveDirect(name, providers, cat), ""
}

// resolveDirect applies rules 2 and 3.
func resolveDirect(name string, providers map[string]bool, cat catalog.Reader) []target {
	// Rule 2: split on the FIRST slash, and only when the prefix names a
	// configured provider. Model identifiers legitimately contain slashes
	// (meta-llama/Llama-3.3-70B-Instruct-Turbo), so a non-matching prefix must
	// fall through with the full string intact.
	if prefix, rest, found := strings.Cut(name, "/"); found && providers[prefix] {
		return []target{{ProviderID: prefix, ModelID: rest}}
	}

	// Rule 3: every enabled provider offering it, in the order the provider set
	// gave them — which is priority order.
	ids := cat.Offering(name)
	out := make([]target, 0, len(ids))
	for _, id := range ids {
		out = append(out, target{ProviderID: id, ModelID: name})
	}
	return out
}
