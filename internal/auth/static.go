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
	StyleSigV4      = "sigv4"
	StyleGCPSA      = "gcp-sa"
	StyleOAuth      = "oauth"
)

// IsStatic reports whether the style is served by a bare credential the adapter
// writes itself.
//
// The empty string is static. A provider row that never declared a style falls
// back to bearer everywhere else, and treating the gap as non-static would
// route every unconfigured provider into a signer.
func IsStatic(style string) bool {
	switch style {
	case "", StyleBearer, StyleXAPIKey, StyleAPIKey, StyleQueryParam, StyleNone:
		return true
	}
	return false
}
