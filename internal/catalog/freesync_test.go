package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const freeCatalogSample = `
export const FREE_CATALOG_CURATED_AT = "2026-08-26";

export const FREE_MODEL_BUDGETS: FreeModelBudget[] = [
  { provider: "groq", modelId: "openai/gpt-oss-120b", displayName: "GPT-OSS 120B", monthlyTokens: 15000000, creditTokens: 0, freeType: "recurring-daily", poolKey: "groq", tos: "caution", hardStopGuaranteed: true },
  { provider: "groq", modelId: "whisper-large-v3", displayName: "Whisper", monthlyTokens: 0, creditTokens: 0, freeType: "recurring-uncapped", poolKey: "groq", tos: "caution" },
  { provider: "acme", modelId: "gone", displayName: "Gone", monthlyTokens: 0, creditTokens: 0, freeType: "discontinued", poolKey: null, tos: "unknown" },
];
`

func TestParseFreeCatalogReadsTheUpstreamSource(t *testing.T) {
	c, err := ParseFreeCatalog([]byte(freeCatalogSample))
	if err != nil {
		t.Fatal(err)
	}
	if c.CuratedAt != "2026-08-26" {
		t.Errorf("curated at %q", c.CuratedAt)
	}
	if !c.Covers("groq", "openai/gpt-oss-120b") || !c.Covers("groq", "whisper-large-v3") {
		t.Error("a documented free model did not read as covered")
	}
	if c.Covers("acme", "gone") {
		t.Error("a discontinued tier read as covered")
	}
}

func TestParseFreeCatalogRefusesADocumentItCannotRead(t *testing.T) {
	// The shape upstream publishes is generated, so a change to it must fail
	// loudly rather than quietly yield a catalogue with nothing in it — which
	// would drop every curated model from the next import.
	if _, err := ParseFreeCatalog([]byte("export const FREE_MODEL_BUDGETS = [];")); err == nil {
		t.Error("an empty catalogue parsed cleanly")
	}
}

func TestFreeSyncerServesTheFetchedCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(freeCatalogSample))
	}))
	defer srv.Close()

	s := NewFreeSyncer(FreeSyncOptions{URL: srv.URL})
	if s.Catalog().CuratedAt == "2026-08-26" {
		t.Fatal("the embedded catalogue already matches the sample; the test proves nothing")
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Catalog(); got.CuratedAt != "2026-08-26" || !got.Covers("groq", "whisper-large-v3") {
		t.Errorf("after a sync the catalogue is %+v", got.CuratedAt)
	}
}

func TestFreeSyncerKeepsWhatItHasWhenTheFetchFails(t *testing.T) {
	// A gateway that lost its free-tier list because GitHub had a bad minute
	// would start dropping models the operator asked to keep.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(freeCatalogSample))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s := NewFreeSyncer(FreeSyncOptions{URL: srv.URL})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("a 502 was reported as a successful sync")
	}
	if !s.Catalog().Covers("groq", "whisper-large-v3") {
		t.Error("a failed fetch emptied the catalogue it already had")
	}
}

func TestFreeSyncerStartsOnTheEmbeddedCatalogue(t *testing.T) {
	// Before any fetch, and forever on a gateway with no outbound access, the
	// filter reads the list the release shipped with.
	s := NewFreeSyncer(FreeSyncOptions{URL: "https://example.invalid/nope"})
	if !s.Catalog().Covers("groq", "openai/gpt-oss-120b") {
		t.Error("the embedded catalogue is not what an unsynced gateway reads")
	}
}

func TestFreeSyncOptionsDefaultToDaily(t *testing.T) {
	o := FreeSyncOptions{}.withDefaults()
	if o.Interval != 24*time.Hour {
		t.Errorf("interval = %v, want 24h", o.Interval)
	}
	if o.URL != FreeCatalogURL {
		t.Errorf("url = %q", o.URL)
	}
}
