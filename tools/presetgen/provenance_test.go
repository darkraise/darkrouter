package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceRecordsSourceAndUpstreamSHAs(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"groq": {{Field: "api_key_url", Source: "9router"}, {Field: "base_url", Source: "omniroute"}},
	}}
	path := filepath.Join(t.TempDir(), "provenance.yaml")
	meta := manifestMeta{OmniRouteSHA: "a1b2c3d", NineRouterSHA: "e4f5a6b"}
	if err := writeProvenance(path, m, meta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a1b2c3d", "e4f5a6b", "api_key_url: 9router", "base_url: omniroute"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("manifest missing %q:\n%s", want, got)
		}
	}
}

// Repeated runs on identical input must produce identical bytes, or the weekly
// PR diffs for nothing. Two runs would not settle it: Go's map iteration
// coincides often enough at this fixture size that a map-order implementation
// would still pass roughly one run in three.
func TestProvenanceIsByteStable(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"b": {{Field: "base_url", Source: "omniroute"}},
		"a": {{Field: "website", Source: "9router"}, {Field: "base_url", Source: "omniroute"}},
		"c": {{Field: "website", Source: "9router"}, {Field: "api_key_url", Source: "omniroute"}},
		"d": {{Field: "base_url", Source: "omniroute"}, {Field: "website", Source: "9router"}},
		"e": {{Field: "website", Source: "omniroute"}, {Field: "base_url", Source: "9router"}},
	}}
	meta := manifestMeta{OmniRouteSHA: "x", NineRouterSHA: "y"}
	dir := t.TempDir()

	const runs = 20
	var first string
	for i := 0; i < runs; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%d.yaml", i))
		if err := writeProvenance(path, m, meta); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(got)
			continue
		}
		if string(got) != first {
			t.Fatalf("run %d differs from run 0:\n%s\n---\n%s", i, first, got)
		}
	}
}

// The per-preset field sort has its own guard: slice order is stable run to
// run, so the byte-stability test above cannot see it.
func TestProvenanceSortsFieldsWithinAPreset(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"groq": {
			{Field: "website", Source: "9router"},
			{Field: "base_url", Source: "omniroute"},
			{Field: "api_key_url", Source: "9router"},
		},
	}}
	path := filepath.Join(t.TempDir(), "provenance.yaml")
	meta := manifestMeta{OmniRouteSHA: "x", NineRouterSHA: "y"}
	if err := writeProvenance(path, m, meta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "  groq:\n    api_key_url: 9router\n    base_url: omniroute\n    website: 9router\n"
	if !strings.Contains(string(got), want) {
		t.Errorf("fields not emitted in ascending order; want block:\n%s\ngot file:\n%s", want, got)
	}
}

// writeProvenance emits YAML by string concatenation, so an id carrying a
// YAML metacharacter would silently corrupt the manifest instead of erroring.
func TestWriteProvenanceRejectsInvalidID(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"bad: id": {{Field: "base_url", Source: "omniroute"}},
	}}
	path := filepath.Join(t.TempDir(), "provenance.yaml")
	meta := manifestMeta{OmniRouteSHA: "x", NineRouterSHA: "y"}
	if err := writeProvenance(path, m, meta); err == nil {
		t.Fatal("want an error for an id outside [a-z0-9._-]")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a corrupt manifest must not be written")
	}
}
