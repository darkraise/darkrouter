package catalog

import "testing"

func TestFieldOriginReadsTheManifest(t *testing.T) {
	// Every preset the manifest names must exist, or the two files have drifted.
	for id := range Provenance().Presets {
		if _, ok := Embedded()[id]; !ok {
			t.Errorf("manifest names preset %q, which presets.yaml does not carry", id)
		}
	}
}

// A parse failure degrades to an empty manifest rather than panicking, for the
// reason FallbackDoc does: a gateway that refuses to boot over a provenance
// label is a worse outcome than one that shows none.
func TestProvenanceDegradesToEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"truncated flow map", "presets: [not a map"},
		{"presets is a scalar", "presets: 7"},
		{"tab indentation", "presets:\n\tgroq: {}"},
		{"not yaml at all", "\x00\x01binary"},
		{"empty file", ""},
		{"no presets key", "omniroute_sha: abc\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProvenance([]byte(tc.raw))
			if got.Presets == nil {
				t.Fatal("parseProvenance returned a nil Presets map; want an empty one")
			}
			if len(got.Presets) != 0 {
				t.Errorf("want no presets from malformed input, got %d", len(got.Presets))
			}
		})
	}

	if Provenance().Presets == nil {
		t.Error("Provenance() returned a nil map; want an empty one")
	}
}

func TestFieldOriginMissesReturnFalse(t *testing.T) {
	if src, ok := FieldOrigin("nope", "base_url"); ok || src != "" {
		t.Errorf("FieldOrigin on an unknown preset = (%q, %v); want (\"\", false)", src, ok)
	}
}
