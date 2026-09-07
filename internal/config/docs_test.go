package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The documented restart-only list has drifted from this one twice: once when
// the listen addresses left it and became environment-owned, and once when
// catalog.seed_free_providers joined it. Both times the document went on
// saying the old thing, because nothing compared them.
//
// This is the one list in the documentation a test can hold: it is exactly the
// contents of RestartOnly, and an operator reads it to decide whether a change
// needs a restart.
func TestDocumentedRestartOnlyListMatchesTheCode(t *testing.T) {
	const doc = "../../docs/design/configuration.md"
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}

	documented, err := restartOnlyBlock(string(raw), doc)
	if err != nil {
		t.Fatalf("%s: %v", doc, err)
	}

	want := slices.Clone(RestartOnly)
	slices.Sort(want)
	got := slices.Clone(documented)
	slices.Sort(got)

	for _, key := range got {
		if !slices.Contains(want, key) {
			t.Errorf("%s documents %s as restart-only; the code does not", doc, key)
		}
	}
	for _, key := range want {
		if !slices.Contains(got, key) {
			t.Errorf("%s is restart-only in the code and not documented in %s", key, doc)
		}
	}
}

// restartOnlyBlock reads the fenced list that follows the restart-only
// sentence. Anchored on that sentence rather than on "the first fence in the
// file", so an unrelated code block added above it cannot silently become the
// thing under test.
func restartOnlyBlock(md, doc string) ([]string, error) {
	const anchor = "Restart-only, because each is captured once"
	switch n := strings.Count(md, anchor); {
	case n == 0:
		return nil, fmt.Errorf("the sentence %q is missing from %s; this test anchors its search on it", anchor, doc)
	case n > 1:
		return nil, fmt.Errorf("the sentence %q appears %d times in %s; this test anchors its search on it and cannot tell which fence is the list", anchor, n, doc)
	}
	i := strings.Index(md, anchor)
	fence := regexp.MustCompile("(?s)```[A-Za-z]*\n(.*?)```")
	m := fence.FindStringSubmatch(md[i:])
	if m == nil {
		return nil, errors.New("no fenced block follows the restart-only sentence")
	}
	var keys []string
	for _, line := range strings.Split(m[1], "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("the fenced block after the restart-only sentence is empty")
	}
	return keys, nil
}
