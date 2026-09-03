package main

import (
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
	meta := manifestMeta{OmniRouteSHA: "a1b2c3d", NineRouterSHA: "e4f5a6b", GeneratedAt: "2026-09-03"}
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

// Two runs on identical input must produce identical bytes, or the weekly PR
// diffs for nothing.
func TestProvenanceIsByteStable(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"b": {{Field: "base_url", Source: "omniroute"}},
		"a": {{Field: "website", Source: "9router"}, {Field: "base_url", Source: "omniroute"}},
		"c": {{Field: "website", Source: "9router"}, {Field: "api_key_url", Source: "omniroute"}},
		"d": {{Field: "base_url", Source: "omniroute"}, {Field: "website", Source: "9router"}},
		"e": {{Field: "website", Source: "omniroute"}, {Field: "base_url", Source: "9router"}},
	}}
	meta := manifestMeta{OmniRouteSHA: "x", NineRouterSHA: "y", GeneratedAt: "2026-09-03"}
	dir := t.TempDir()
	one, two := filepath.Join(dir, "1.yaml"), filepath.Join(dir, "2.yaml")
	if err := writeProvenance(one, m, meta); err != nil {
		t.Fatal(err)
	}
	if err := writeProvenance(two, m, meta); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(one)
	b, _ := os.ReadFile(two)
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n---\n%s", a, b)
	}
}
