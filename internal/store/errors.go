package store

import (
	"errors"
	"strings"
)

// ErrNotFound marks a lookup or mutation that named a row which does not
// exist. Callers test with errors.Is; the wrapping message names the row.
var ErrNotFound = errors.New("not found")

// ErrConflict marks a write refused because a unique value is already taken.
var ErrConflict = errors.New("conflict")

// isUniqueViolation reports whether err is SQLite refusing a duplicate key.
// The driver's error text is the stable part of its contract; its numeric
// codes live in a sub-package this file would otherwise import for one
// constant.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed")
}
