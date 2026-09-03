package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
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
// Every field upstream publishes that darkrouter has a reader for is kept. The
// three decided on before — provider, model, freeType — answered "is this model
// free"; the rest answer "free how much, on whose terms, out of which shared
// bucket", which is what an operator actually needs before routing production
// traffic through it. Upstream's displayName has no reader and is dropped.
type FreeTier struct {
	// FreeType is the shape of the allowance: recurring-daily,
	// recurring-monthly, recurring-uncapped, recurring-credit, one-time-initial,
	// keyless, or discontinued.
	FreeType string `json:"free_type"`
	// MonthlyTokens is the recurring allowance; CreditTokens a one-time grant.
	// Zero in both means the allowance is uncapped or unquantified -- except on
	// a discontinued tier, where it is the plain fact that nothing is granted.
	MonthlyTokens int64 `json:"monthly_tokens,omitempty"`
	CreditTokens  int64 `json:"credit_tokens,omitempty"`
	// PoolKey names a quota shared across models. Empty where upstream
	// publishes a null, which it does for a handful of rows.
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

// Vetoed reports whether this tier is one darkrouter refuses without the
// operator's opt-in: a grading the vendor has not sanctioned, on a tier that
// still exists. A withdrawn tier grades terms that no longer govern access.
//
// Both gates ask this one question. When the import filter and the routing
// filter disagree, a model darkrouter itself imported is refused at request
// time by an error naming a free tier the catalogue already withdrew.
func (t FreeTier) Vetoed() bool { return t.Live() && t.Unsanctioned() }

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

// ModelsFor lists the models documented free for one preset, in no particular
// order and with discontinued tiers excluded. Nil for a provider the catalog
// has never covered, which is a different fact from one whose free tier has
// been withdrawn.
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
	return out
}

// freeEntry matches one line of OmniRoute's FREE_MODEL_BUDGETS. Every field is
// matched so the pattern stays positional, but displayName is only matched:
// nothing downstream shows upstream's label, and darkrouter names a model by
// the id it routes to. poolKey alternates because some rows carry a literal
// null rather than a string.
var freeEntry = regexp.MustCompile(
	`\{ provider: "([^"]+)", modelId: "([^"]+)", displayName: "(?:[^"]*)", ` +
		`monthlyTokens: (\d+), creditTokens: (\d+), freeType: "([^"]+)", ` +
		`poolKey: (?:"([^"]+)"|null), tos: "([^"]+)"`)

var curatedAt = regexp.MustCompile(`FREE_CATALOG_CURATED_AT = "([^"]+)"`)

// freeEntryStart marks where a row begins, whatever the rest of it looks like.
// Counting these is how a partial parse is caught: freeEntry is positional
// across all eight fields, so a shape change confined to some rows -- or a
// displayName carrying an escaped quote -- drops exactly those rows and leaves
// a catalogue that still looks populated.
var freeEntryStart = regexp.MustCompile(`\{ provider: "`)

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
	matches := freeEntry.FindAllSubmatch(raw, -1)
	var dupProvider, dupModel string
	for _, m := range matches {
		provider, model := string(m[1]), string(m[2])
		if out.Providers[provider] == nil {
			out.Providers[provider] = map[string]FreeTier{}
		}
		if _, collides := out.Providers[provider][model]; collides && dupProvider == "" {
			dupProvider, dupModel = provider, model
		}
		monthly, _ := strconv.ParseInt(string(m[3]), 10, 64)
		credit, _ := strconv.ParseInt(string(m[4]), 10, 64)
		out.Providers[provider][model] = FreeTier{
			MonthlyTokens: monthly,
			CreditTokens:  credit,
			FreeType:      string(m[5]),
			PoolKey:       string(m[6]),
			ToS:           string(m[7]),
		}
	}
	// Every row this file holds must survive into the catalogue, exactly. The
	// rows are template-generated and uniform, so a stored count short of the
	// row count is never ordinary variation: it is the entry pattern failing on
	// a shape it no longer fits, or two rows colliding on the same provider and
	// model. Both silently shrink the catalogue, every caller stores what it is
	// handed, and a dropped row means a real model refused on the next import.
	// Stopping loudly is the cheaper failure.
	if rows := len(freeEntryStart.FindAll(raw, -1)); len(matches) != rows {
		return FreeCatalog{}, fmt.Errorf(
			"free catalogue: read %d of %d entries; the row shape no longer matches the pattern",
			len(matches), rows)
	}
	if out.count() != len(matches) {
		return FreeCatalog{}, fmt.Errorf(
			"free catalogue: %d rows stored as %d entries; upstream lists provider %q model %q twice",
			len(matches), out.count(), dupProvider, dupModel)
	}
	if len(out.Providers) == 0 {
		return FreeCatalog{}, fmt.Errorf("free catalogue held no entries")
	}
	return out, nil
}
