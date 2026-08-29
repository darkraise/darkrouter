package auth

// The closed style vocabulary, matching catalog.AuthStyles. Duplicating the
// names rather than importing catalog is deliberate: catalog imports provider,
// which imports store, and a credential strategy that pulled that in would make
// internal/auth unusable from anywhere near the bottom of the tree.
const (
	StyleBearer     = "bearer"
	StyleXAPIKey    = "x-api-key"
	StyleAPIKey     = "api-key"
	StyleQueryParam = "query-param"
	StyleNone       = "none"
	// StyleOptional is a provider that serves an unauthenticated request and
	// serves a credentialled one better — a free gateway whose limits rise
	// when it knows who is calling. Static like the rest: the adapter writes
	// the key when there is one and writes nothing when there is not, which is
	// the whole of the difference from bearer.
	StyleOptional = "optional"
	// StyleAnonymous is a provider that demands a credential but publishes one
	// for everybody: AI Horde documents the literal 0000000000 as its
	// anonymous key. The preset ships that string, so an operator adds the
	// provider and nothing else. Static like the rest, and keyless in the only
	// sense the console cares about — there is nothing to paste.
	StyleAnonymous = "anonymous"
	StyleSigV4     = "sigv4"
	StyleGCPSA     = "gcp-sa"
	StyleOAuth     = "oauth"
)

// IsStatic reports whether the style is served by a bare credential the adapter
// writes itself.
//
// The empty string is static. A provider row that never declared a style falls
// back to bearer everywhere else, and treating the gap as non-static would
// route every unconfigured provider into a signer.
func IsStatic(style string) bool {
	switch style {
	case "", StyleBearer, StyleXAPIKey, StyleAPIKey, StyleQueryParam, StyleNone,
		StyleOptional, StyleAnonymous:
		return true
	}
	return false
}

// IsKeyless reports whether a provider in this style can serve a request with
// no credential at all.
//
// The router, the discovery sweep and the console's state vocabulary all turn
// on this one question, and answering it in three places is how they drift.
func IsKeyless(style string) bool {
	return style == StyleNone || style == StyleOptional || style == StyleAnonymous
}
