package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
)

// freeCatalogJSON is the curated free-tier catalog transcribed from OmniRoute
// by tools/presetgen.
//
//go:embed free_models.json
var freeCatalogJSON []byte

// FreeCatalog is the list of models a provider's free tier documents, keyed by
// preset id then model id, with the kind of free each one is.
//
// It exists because free-tier membership cannot be derived. A price index
// records what a model costs on the paid tier; whether a vendor's free tier
// covers it is a property of the account, published in the vendor's own docs
// and nowhere in its API. Groq is the case that proves it: every model it
// serves carries a per-token price, and thirteen of them are on its free tier.
type FreeCatalog struct {
	// CuratedAt is the day the upstream catalog was last checked against
	// provider documentation. Printed rather than inferred from a file
	// timestamp, which a rebuild would reset to today.
	CuratedAt string `json:"curated_at"`
	// Providers maps preset id -> model id -> the tier's whole record.
	Providers map[string]map[string]FreeTier `json:"providers"`
}

// FreeTier is one row of the upstream free-model catalogue.
//
// Every field upstream publishes is kept. The three darkrouter decided on
// before — provider, model, freeType — answered "is this model free"; the rest
// answer "free how much, on whose terms, out of which shared bucket", which is
// what an operator actually needs before routing production traffic through it.
type FreeTier struct {
	// FreeType is the shape of the allowance: recurring-daily,
	// recurring-monthly, recurring-uncapped, recurring-credit, one-time-initial,
	// keyless, or discontinued.
	FreeType string `json:"free_type"`
	// DisplayName is upstream's label for the model, kept for the console.
	DisplayName string `json:"display_name,omitempty"`
	// MonthlyTokens is the recurring allowance; CreditTokens a one-time grant.
	// Zero in both means uncapped or unquantified, never "no allowance".
	MonthlyTokens int64 `json:"monthly_tokens,omitempty"`
	CreditTokens  int64 `json:"credit_tokens,omitempty"`
	// PoolKey names a quota shared across models. Empty for the seven rows
	// upstream publishes as null.
	PoolKey string `json:"pool_key,omitempty"`
	// ToS is upstream's verdict on how the vendor regards this access: ok,
	// caution, ambiguous, avoid, unknown.
	ToS string `json:"tos"`
}

// tosAvoid is the verdict that keeps a tier out of automatic use. It largely
// means access the vendor has not sanctioned; a gateway that silently routes
// production traffic through it exposes its operator to a risk they never
// agreed to.
const tosAvoid = "avoid"

// Unsanctioned reports whether the vendor has not sanctioned this access.
func (t FreeTier) Unsanctioned() bool { return t.ToS == tosAvoid }

// Live reports whether the tier still exists. A withdrawn tier stays in the
// catalogue for its history and is not one an import filter may count on.
func (t FreeTier) Live() bool { return t.FreeType != freeTypeDiscontinued }

// freeTypeDiscontinued marks a free tier the provider has withdrawn. The
// upstream catalog keeps those rows for the history; a withdrawn tier is not
// one an import filter may count on.
const freeTypeDiscontinued = "discontinued"

var (
	freeOnce    sync.Once
	freeCatalog FreeCatalog
)

// FreeModels is the embedded curated catalog.
//
// A parse failure degrades to an empty catalog rather than panicking, for the
// same reason FallbackDoc does: the file is generated, its shape is asserted
// by this package's tests, and a gateway that refuses to boot over it would be
// a worse outcome than one whose free filter falls back to prices alone.
func FreeModels() FreeCatalog {
	freeOnce.Do(func() {
		if err := json.Unmarshal(freeCatalogJSON, &freeCatalog); err != nil {
			freeCatalog = FreeCatalog{}
		}
	})
	return freeCatalog
}

// Covers reports whether the provider's free tier documents this model.
func (c FreeCatalog) Covers(presetID, modelID string) bool {
	tier, ok := c.Providers[presetID][modelID]
	return ok && tier.Live()
}

// Tier returns the whole record for one model, if the catalogue has it.
func (c FreeCatalog) Tier(presetID, modelID string) (FreeTier, bool) {
	tier, ok := c.Providers[presetID][modelID]
	return tier, ok
}

// ModelsFor lists the models documented free for one preset, discontinued
// tiers excluded. Nil for a provider the catalog has never covered, which is a
// different fact from one whose free tier has been withdrawn.
func (c FreeCatalog) ModelsFor(presetID string) []string {
	models := c.Providers[presetID]
	if len(models) == 0 {
		return nil
	}
	out := make([]string, 0, len(models))
	for id, tier := range models {
		if tier.Live() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// freeEntry matches one line of OmniRoute's FREE_MODEL_BUDGETS. Every field
// upstream publishes is captured; poolKey alternates because seven rows carry
// a literal null rather than a string.
var freeEntry = regexp.MustCompile(
	`\{ provider: "([^"]+)", modelId: "([^"]+)", displayName: "([^"]*)", ` +
		`monthlyTokens: (\d+), creditTokens: (\d+), freeType: "([^"]+)", ` +
		`poolKey: (?:"([^"]+)"|null), tos: "([^"]+)"`)

var curatedAt = regexp.MustCompile(`FREE_CATALOG_CURATED_AT = "([^"]+)"`)

// ParseFreeCatalog reads the upstream catalogue's TypeScript source.
//
// A regex over source rather than a JSON parse because that is the form
// upstream publishes: the list is a literal in a .ts file, and asking them for
// a machine-readable export is not a dependency darkrouter can create. The
// shape is uniform -- one object literal per line, generated by their own
// tooling -- and a change to it fails loudly here rather than silently
// yielding a catalogue with nothing in it.
//
// Shared with tools/presetgen so the file embedded at build time and the one
// fetched at runtime are read by the same code.
func ParseFreeCatalog(raw []byte) (FreeCatalog, error) {
	out := FreeCatalog{Providers: map[string]map[string]FreeTier{}}
	if m := curatedAt.FindSubmatch(raw); m != nil {
		out.CuratedAt = string(m[1])
	}
	for _, m := range freeEntry.FindAllSubmatch(raw, -1) {
		provider, model := string(m[1]), string(m[2])
		if out.Providers[provider] == nil {
			out.Providers[provider] = map[string]FreeTier{}
		}
		monthly, _ := strconv.ParseInt(string(m[4]), 10, 64)
		credit, _ := strconv.ParseInt(string(m[5]), 10, 64)
		out.Providers[provider][model] = FreeTier{
			DisplayName:   string(m[3]),
			MonthlyTokens: monthly,
			CreditTokens:  credit,
			FreeType:      string(m[6]),
			PoolKey:       string(m[7]),
			ToS:           string(m[8]),
		}
	}
	if len(out.Providers) == 0 {
		return FreeCatalog{}, fmt.Errorf("free catalogue held no entries")
	}
	return out, nil
}
