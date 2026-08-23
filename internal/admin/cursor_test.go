package admin

import (
	"strings"
	"testing"
)

func TestACursorRoundTrips(t *testing.T) {
	f := RequestFilters{Provider: "groq", Status: "success"}
	c := encodeCursor(1700000000123, "01ABCDEF", f)
	if c == "" {
		t.Fatal("empty cursor")
	}
	ts, id, err := decodeCursor(c, f)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 1700000000123 || id != "01ABCDEF" {
		t.Errorf("decoded (%d, %q)", ts, id)
	}
}

func TestACursorIsRejectedWhenTheFiltersChange(t *testing.T) {
	// Spec §4.2: a cursor is a position in ONE ordered result set. Presented
	// against a different set it names a row that may not be in it, and
	// returning a page from there is nonsense the client cannot detect.
	c := encodeCursor(1700000000123, "01ABCDEF", RequestFilters{Provider: "groq"})
	if _, _, err := decodeCursor(c, RequestFilters{Provider: "openai"}); err == nil {
		t.Error("a cursor from one filter set decoded under another")
	}
}

func TestACursorIsRejectedWhenAFilterIsAdded(t *testing.T) {
	c := encodeCursor(1, "a", RequestFilters{Provider: "groq"})
	if _, _, err := decodeCursor(c, RequestFilters{Provider: "groq", Surface: "llm"}); err == nil {
		t.Error("adding a filter did not invalidate the cursor")
	}
}

func TestAnEmptyFilterSetStillHashes(t *testing.T) {
	// The unfiltered case is the common one and must round-trip.
	var f RequestFilters
	c := encodeCursor(5, "x", f)
	ts, id, err := decodeCursor(c, f)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 5 || id != "x" {
		t.Errorf("decoded (%d, %q)", ts, id)
	}
}

func TestGarbageIsRejected(t *testing.T) {
	var f RequestFilters
	for _, c := range []string{"", "!!!", "YWJj", strings.Repeat("A", 200)} {
		if _, _, err := decodeCursor(c, f); err == nil {
			t.Errorf("cursor %q decoded", c)
		}
	}
}

func TestTheCursorIsOpaque(t *testing.T) {
	// Nothing in the SPA should be able to read a timestamp out of it and do
	// arithmetic. Opacity is what keeps the encoding free to change.
	c := encodeCursor(1700000000123, "01ABCDEF", RequestFilters{})
	if strings.Contains(c, "1700000000123") || strings.Contains(c, "01ABCDEF") {
		t.Errorf("cursor = %q; it leaks its contents", c)
	}
}

func TestFilterHashIsOrderIndependentButValueSensitive(t *testing.T) {
	// Two filter sets that mean the same thing must hash the same, or a page
	// boundary rejects a cursor that is in fact valid.
	a := RequestFilters{Provider: "groq", Model: "m", Status: "success"}
	b := RequestFilters{Status: "success", Model: "m", Provider: "groq"}
	if a.Hash() != b.Hash() {
		t.Error("identical filters hashed differently")
	}
	c := RequestFilters{Provider: "groq", Model: "m", Status: "error"}
	if a.Hash() == c.Hash() {
		t.Error("different filters hashed the same")
	}
}

func TestAFieldSeparatorPreventsHashCollisions(t *testing.T) {
	// Without the separator {Provider:"ab"} and {Provider:"a", Model:"b"}
	// would hash identically, and a cursor would survive a filter change that
	// genuinely altered the result set.
	if (RequestFilters{Provider: "ab"}).Hash() == (RequestFilters{Provider: "a", Model: "b"}).Hash() {
		t.Error("two different filter sets collided")
	}
}
