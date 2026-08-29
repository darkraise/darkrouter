package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// RequestFilters are spec §4.2's filter set: provider, model, status, alias,
// surface, source, and a time range.
//
// A struct rather than a map so the hash below cannot depend on iteration order,
// and so a new filter is a compile error at every call site rather than a
// silently ignored query parameter.
type RequestFilters struct {
	Provider  string
	Model     string
	Status    string
	Alias     string
	Surface   string
	ErrorCode string
	// Source separates console traffic from a client's. Part of the hash like
	// every other filter: a cursor minted while looking at test requests must
	// not silently page into production ones.
	Source  string
	SinceMs int64
	UntilMs   int64
}

// Hash identifies the filter set a cursor was minted under.
//
// Field order is fixed by the code rather than by iteration, so two structs with
// the same values always hash the same — a hash that varied would reject cursors
// that are in fact valid, which reads to the operator as the table refusing to
// scroll.
func (f RequestFilters) Hash() string {
	h := sha256.New()
	for _, s := range []string{f.Provider, f.Model, f.Status, f.Alias, f.Surface, f.ErrorCode, f.Source} {
		h.Write([]byte(s))
		// A separator, so {Provider:"ab"} and {Provider:"a", Model:"b"} differ.
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "%d\x00%d", f.SinceMs, f.UntilMs)
	// Eight bytes is plenty: this is a mismatch detector, not a MAC. A client
	// forging one gets a page of its own filter set, which it could have asked
	// for directly.
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:8])
}

// encodeCursor packs a position and the filter set it belongs to.
//
// Opaque base64 so nothing in the SPA is tempted to read a timestamp out of it
// and do arithmetic, which is what keeps this encoding free to change.
func encodeCursor(ts int64, id string, f RequestFilters) string {
	raw := strconv.FormatInt(ts, 10) + "\x00" + id + "\x00" + f.Hash()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var errCursorMismatch = errors.New(
	"this cursor belongs to a different filter set; start from the first page")

// decodeCursor unpacks a cursor and rejects one minted under different filters.
//
// Spec §4.2: rejected rather than silently returning nonsense. A cursor is a
// position in one ordered result set; presented against a different set it names
// a row that may not be in it, and the page that comes back would look valid.
func decodeCursor(s string, f RequestFilters) (int64, string, error) {
	if s == "" {
		return 0, "", errors.New("empty cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, "", fmt.Errorf("malformed cursor: %w", err)
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return 0, "", errors.New("malformed cursor")
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed cursor: %w", err)
	}
	if parts[2] != f.Hash() {
		return 0, "", errCursorMismatch
	}
	return ts, parts[1], nil
}
