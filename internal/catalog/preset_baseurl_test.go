package catalog

import (
	"regexp"
	"strings"
	"testing"
)

// placeholder matches any {…} left in a shipped base URL.
var placeholder = regexp.MustCompile(`\{[^}]*\}`)

// Upstream registries carry base URLs their own bespoke executors complete --
// Cloudflare's stops at /accounts, Snowflake's is a bare {account} hostname.
// Transcribed verbatim they produce a provider that 404s from a URL nobody can
// explain, so the only placeholder a shipped preset may carry is the one the
// credential's account fills.
func TestNoPresetShipsAPlaceholderNothingFills(t *testing.T) {
	for id, p := range Embedded() {
		for _, m := range placeholder.FindAllString(p.BaseURL, -1) {
			if m != "{account_id}" {
				t.Errorf("preset %q base_url carries %s, which nothing substitutes: %s", id, m, p.BaseURL)
			}
		}
	}
}

// The two the catalogue knows about. Named so that losing the substitution on
// a regeneration is a failure here rather than a support thread.
func TestTheAccountTemplatedPresetsAreComplete(t *testing.T) {
	want := map[string]string{
		"cloudflare-ai": "https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1",
		"snowflake":     "https://{account_id}.snowflakecomputing.com/api/v2",
	}
	for id, url := range want {
		got, ok := Embedded()[id]
		if !ok {
			t.Errorf("preset %q is gone", id)
			continue
		}
		if got.BaseURL != url {
			t.Errorf("preset %q base_url = %q, want %q", id, got.BaseURL, url)
		}
	}
}

// A provider whose endpoint needs an account is useless without a credential
// that carries one, and the console decides whether to ask by looking for the
// placeholder. A preset that needs one but hides it behind a different
// spelling would silently stop the console asking.
func TestATemplatedPresetIsDetectable(t *testing.T) {
	for _, id := range []string{"cloudflare-ai", "snowflake"} {
		if !strings.Contains(Embedded()[id].BaseURL, "{account_id}") {
			t.Errorf("preset %q is not detectable as needing an account", id)
		}
	}
}
