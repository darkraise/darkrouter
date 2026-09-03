package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
	_ "modernc.org/sqlite"
)

// TestPriceCoverage reports how many rows of a real catalogue carry a price,
// with and without the LiteLLM index. It is a measurement rather than an
// assertion — the number depends on which providers an operator configured, so
// there is nothing to assert — and it skips unless both inputs are named:
//
//	DARKROUTER_COVERAGE_DB=/copy/of/darkrouter.db \
//	DARKROUTER_LITELLM_JSON=/tmp/litellm.json \
//	go test ./internal/catalog/ -run TestPriceCoverage -v -count=1
//
// Point it at a copy, never the live file: it migrates the database to head.
func TestPriceCoverage(t *testing.T) {
	path := os.Getenv("DARKROUTER_COVERAGE_DB")
	indexPath := os.Getenv("DARKROUTER_LITELLM_JSON")
	if path == "" || indexPath == "" {
		t.Skip("set DARKROUTER_COVERAGE_DB and DARKROUTER_LITELLM_JSON")
	}
	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := db.ModelOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	pr, err := raw.QueryContext(ctx, "SELECT id, preset FROM providers")
	if err != nil {
		t.Fatal(err)
	}
	var provs []provider.Provider
	for pr.Next() {
		var id string
		var preset sql.NullString
		if err := pr.Scan(&id, &preset); err != nil {
			t.Fatal(err)
		}
		provs = append(provs, provider.Provider{ID: id, Preset: preset.String})
	}
	pr.Close()

	llRaw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	ll, err := ParseLiteLLM(llRaw)
	if err != nil {
		t.Fatal(err)
	}

	merge := func(doc LiteLLMDoc) map[string]Pricing {
		out := map[string]Pricing{}
		for _, m := range Merge(MergeInput{
			Providers: provs, Presets: Embedded(), Doc: FallbackDoc(),
			LiteLLM: doc, Rows: rows, Overrides: overrides,
		}) {
			out[m.ProviderID+"|"+m.ModelID] = m.Pricing
		}
		return out
	}
	report := func(label string, priced map[string]Pricing) {
		n := 0
		bySrc := map[Source]int{}
		for _, p := range priced {
			if p.Known {
				n++
				bySrc[p.Source]++
			}
		}
		fmt.Printf("%s: rows=%d priced=%d dist=%v\n", label, len(priced), n, bySrc)
	}
	before, after := merge(nil), merge(ll)
	report("BEFORE", before)
	report("AFTER ", after)
	for k, a := range after {
		if b := before[k]; b != a {
			fmt.Printf("CHANGED %s: %+v -> %+v\n", k, b, a)
		}
	}
	joinable, joined := 0, 0
	for _, row := range rows {
		var preset Preset
		for _, pv := range provs {
			if pv.ID == row.ProviderID {
				preset = Embedded()[pv.Preset]
			}
		}
		if preset.NoLiteLLM || preset.LiteLLMID == "" {
			continue
		}
		joinable++
		if p, ok := JoinLiteLLM(preset, ll, row.ModelID); ok && p.Known {
			joined++
		}
	}
	fmt.Printf("rows whose preset carries a litellm key: %d; of those, joined to a price: %d\n", joinable, joined)
	fmt.Printf("litellm index: providers=%d models=%d\n", len(ll), ll.count())
}
