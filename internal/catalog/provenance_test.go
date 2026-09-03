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
// reason Embedded does: a gateway that refuses to boot over a provenance label
// is a worse outcome than one that shows none.
func TestProvenanceDegradesToEmpty(t *testing.T) {
	if Provenance().Presets == nil {
		t.Error("Provenance() returned a nil map; want an empty one")
	}
}
