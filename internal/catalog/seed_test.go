package catalog

import (
	"testing"
)

func TestEveryVertexPresetDeclaresAPublisher(t *testing.T) {
	// A vertex preset with no publisher seeds nothing and routes to the Google
	// builder by default, which is a silent wrong answer for a Claude model.
	for id, p := range Embedded() {
		if p.Kind != "vertex" {
			continue
		}
		if p.Publisher != "publishers/google" && p.Publisher != "publishers/anthropic" {
			t.Errorf("%s: publisher = %q", id, p.Publisher)
		}
	}
}

func TestOnlyVertexPresetsDeclareAPublisher(t *testing.T) {
	// A publisher on a discoverable kind would make SeedFromPreset fight the
	// discovery worker: seeded rows and probed rows would overwrite each other
	// every tick.
	for id, p := range Embedded() {
		if p.Kind != "vertex" && p.Publisher != "" {
			t.Errorf("%s is kind %q but declares publisher %q", id, p.Kind, p.Publisher)
		}
	}
}

func TestSeedTakesOnlyThePresetsPublisher(t *testing.T) {
	preset := Embedded()["vertex-anthropic"]
	got := SeedFromPreset(preset, FallbackDoc())
	if len(got) == 0 {
		t.Fatal("seeding produced nothing; the models.dev join key may be wrong")
	}
	for _, m := range got {
		if m.Publisher != "publishers/anthropic" {
			t.Errorf("%s carries publisher %q", m.ModelID, m.Publisher)
		}
	}
}

func TestSeedCarriesMetadataFromModelsDev(t *testing.T) {
	// The whole point: Vertex has no listing, so everything the router needs
	// to filter on has to come from the document.
	got := SeedFromPreset(Embedded()["vertex"], FallbackDoc())
	if len(got) == 0 {
		t.Fatal("seeding produced nothing")
	}
	withWindow := 0
	for _, m := range got {
		if m.ContextWindow > 0 {
			withWindow++
		}
	}
	if withWindow == 0 {
		t.Error("no seeded model carries a context window; the router cannot size a request")
	}
}

func TestSeedIsEmptyForADiscoverableKind(t *testing.T) {
	// A preset that is not a vertex one must not be seeded: its models come
	// from a real listing endpoint, and seeding would fight discovery.
	if got := SeedFromPreset(Embedded()["groq"], FallbackDoc()); len(got) != 0 {
		t.Errorf("seeded %d models for a discoverable kind", len(got))
	}
}

func TestSeedIsStable(t *testing.T) {
	// Two runs must agree, or every discovery tick rewrites the same rows in a
	// different order and the last_seen bookkeeping churns.
	a := SeedFromPreset(Embedded()["vertex"], FallbackDoc())
	b := SeedFromPreset(Embedded()["vertex"], FallbackDoc())
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ModelID != b[i].ModelID {
			t.Fatalf("order differs at %d: %q vs %q", i, a[i].ModelID, b[i].ModelID)
		}
	}
}
