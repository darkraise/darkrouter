package config

import "fmt"

// Patch is one save. A key carries one of three intents -- set to a value,
// reset to its compiled default, or untouched -- and the third is why this is
// two collections rather than one map: a map can say "this key is now X" and
// "this key is gone", but not "leave this key alone".
//
// The distinction is not decoration. Every deployment that has ever started
// materialised all seven policy rows, so without it a reset could never take
// and every one of them would read as a value the operator chose.
type Patch struct {
	// Set carries a new value per key, in the string form the registry parses.
	// An empty value is a reset: `value TEXT NOT NULL` would store "" happily
	// and it would read back as "set".
	Set map[string]string
	// Reset names keys whose stored row is deleted, which is what returns a
	// key to its compiled default. Writing the default instead would leave a
	// row that pins the value if a later release changes that default.
	Reset []string
	// Aliases replaces the whole alias set. Nil leaves the alias table alone;
	// an empty non-nil map deletes every alias, which is what an operator who
	// removed the last one meant.
	Aliases map[string][]string
}

// RejectedError is a write refused for what it says rather than for a failure
// to store it. The admin API answers 400 for it and 500 for everything else:
// one is the operator's to fix, the other is not.
type RejectedError struct{ Msg string }

func (e RejectedError) Error() string { return e.Msg }

// Rejected builds a RejectedError. The write path returns several of these and
// a constructor keeps them from drifting into wrapped or unwrapped variants.
func Rejected(format string, args ...any) error {
	return RejectedError{Msg: fmt.Sprintf(format, args...)}
}

// PublishError is a reload that failed after its write committed. The rows are
// durable and the previous configuration is still serving, which is a
// different answer from "the write was refused" and has to reach the operator
// as one.
type PublishError struct{ Err error }

func (e PublishError) Error() string { return e.Err.Error() }
func (e PublishError) Unwrap() error { return e.Err }

// bootstrapVars names the settings the environment owns. A save that names one
// is told where the value actually lives rather than that the key is unknown,
// which would send an operator looking for a typo they did not make.
var bootstrapVars = map[string]string{
	"server.proxy_listen": "DARKROUTER_PROXY_LISTEN",
	"server.admin_listen": "DARKROUTER_ADMIN_LISTEN",
	"server.proxy_token":  "DARKROUTER_PROXY_TOKEN",
	"log.level":           "DARKROUTER_LOG_LEVEL",
	"log.format":          "DARKROUTER_LOG_FORMAT",
}

// BootstrapVar names the environment variable that owns a key, if one does.
func BootstrapVar(key string) (string, bool) {
	name, ok := bootstrapVars[key]
	return name, ok
}
