package provider

import (
	"fmt"
	"regexp"
	"strings"
)

// accountPlaceholder is what a preset writes where the operator's own account
// identifier belongs. One spelling for every provider that needs one: the
// value comes from the same credential field either way, and a second name
// would be a second thing to keep in step.
const accountPlaceholder = "{account_id}"

// accountPattern is what may be substituted in. Deliberately narrow, because
// the value lands in a path for Cloudflare and in the hostname for Snowflake:
// a separator, a userinfo marker or whitespace there does not produce a broken
// URL, it produces a working one pointing somewhere the operator never
// configured.
var accountPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NeedsAccount reports whether this base URL cannot be used without a
// credential's account identifier.
func NeedsAccount(baseURL string) bool {
	return strings.Contains(baseURL, accountPlaceholder)
}

// ResolveBaseURL substitutes a credential's account identifier into the
// provider's base URL.
//
// A base URL with no placeholder is returned unchanged, account or not: the
// field is per-credential, and a provider that stopped needing one should not
// start failing for the credentials that still carry it.
func ResolveBaseURL(baseURL, account string) (string, error) {
	if !NeedsAccount(baseURL) {
		return baseURL, nil
	}
	if account == "" {
		return "", fmt.Errorf("this provider's endpoint needs an account identifier, and this credential carries none")
	}
	if !accountPattern.MatchString(account) {
		return "", fmt.Errorf("account identifier %q is not one: letters, digits, dot, dash and underscore only", account)
	}
	return strings.ReplaceAll(baseURL, accountPlaceholder, account), nil
}
