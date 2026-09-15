package config

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// AliasesRevision is an opaque fingerprint of an alias table's exact content
// and per-chain order. Two reads of the same table produce the same
// revision; any write in between changes it. It is the ETag PUT /api/aliases
// checks a save's If-Match against.
func AliasesRevision(aliases map[string][]string) string {
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		// NUL-delimited: it cannot appear in a name or target, so a chain
		// boundary can never be produced by concatenating adjacent fields.
		h.Write([]byte(name))
		h.Write([]byte{0})
		for _, target := range aliases[name] {
			h.Write([]byte(target))
			h.Write([]byte{0})
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
