package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkrouter/internal/config"
)

// LoadConfig builds the running configuration: compiled defaults, then the
// bootstrap environment, then the stored rows, then validation.
//
// It never fails on settings content. A row that will not parse, or a rule no
// single key broke, reverts the keys involved to their defaults and appends a
// warning. Only a database that cannot be read is an error, because that is
// not something an operator can fix from the console either way.
func LoadConfig(ctx context.Context, d *DB, boot config.Bootstrap) (*config.Config, error) {
	rows, err := configRows(ctx, d)
	if err != nil {
		return nil, err
	}

	build := func(skip map[string]bool) (*config.Config, []string) {
		c := &config.Config{}
		config.ApplyDefaults(c)
		c.Server.ProxyListen = boot.ProxyListen
		c.Server.AdminListen = boot.AdminListen
		c.Server.ProxyToken = boot.ProxyToken
		use := make(map[string]string, len(rows))
		for k, v := range rows {
			if !skip[k] {
				use[k] = v
			}
		}
		return c, ApplyConfigRows(c, use)
	}

	skip := map[string]bool{}
	c, warnings := build(skip)

	// Parse failures first: the pass above reported every key that would not
	// parse, and this one rebuilds without them. Rule failures are handled
	// below, where a key can only be identified from the message.
	if len(warnings) > 0 {
		for _, w := range warnings {
			for _, k := range ConfigKeys() {
				// ApplyConfigRows builds each warning from the key itself, so
				// naming it is what identifies the key to drop.
				if strings.Contains(w, k) {
					skip[k] = true
				}
			}
		}
		c, _ = build(skip)
	}

	// The bound is one iteration per key plus one. Each iteration retires at
	// least one key -- a rule's whole set, or the single key its message names
	// -- and the last iteration validates the compiled defaults, which must
	// pass. A fixed two would have been enough only while a single-key failure
	// reverted everything at once.
	for attempt := 0; attempt <= len(configRegistry); attempt++ {
		err := config.Validate(c)
		if err == nil {
			c.Warnings = append(c.Warnings, warnings...)
			return c, nil
		}

		var re config.RuleError
		if errors.As(err, &re) && addAny(skip, re.Keys) {
			warnings = append(warnings,
				fmt.Sprintf("stored %v broke the %s rule; all of them reverted to their defaults", re.Keys, re.Rule))
			c, _ = build(skip)
			continue
		}
		// A single-key rule. Every message validate produces for one names the
		// setting it is about, so the key to revert can be read out of it
		// rather than guessed.
		if k, ok := keyNamedIn(err.Error(), skip); ok {
			skip[k] = true
			warnings = append(warnings,
				fmt.Sprintf("stored %s is unusable (%v); using the default", k, err))
			c, _ = build(skip)
			continue
		}
		// Nothing identifiable, or a rule whose keys are all reverted already.
		// Everything goes rather than the process refusing to start.
		skip = allKeys()
		c, _ = build(skip)
		warnings = append(warnings,
			fmt.Sprintf("stored configuration is unusable (%v); every key reverted to its default", err))
	}

	// Reaching here means the compiled defaults themselves do not validate,
	// which is a bug in the defaults rather than in anything an operator
	// stored. Only that is allowed to fail a start.
	if err := config.Validate(c); err != nil {
		return nil, fmt.Errorf("compiled defaults do not validate: %w", err)
	}
	c.Warnings = append(c.Warnings, warnings...)
	return c, nil
}

// addAny marks every key not already skipped, reporting whether it changed
// anything. A pass that retires no new key would loop until the bound with the
// same failure, so it is the caller's signal to stop narrowing.
func addAny(skip map[string]bool, keys []string) bool {
	added := false
	for _, k := range keys {
		if !skip[k] {
			skip[k] = true
			added = true
		}
	}
	return added
}

// keyNamedIn finds the registry key a validation message is about. The longest
// match wins, so a message naming policy.timeout.total is not attributed to a
// key whose name is a prefix of it. A key already reverted is not a candidate:
// returning it would revert nothing and the retry would fail identically.
func keyNamedIn(msg string, skip map[string]bool) (string, bool) {
	best := ""
	for _, f := range configRegistry {
		if skip[f.key] || !strings.Contains(msg, f.key) {
			continue
		}
		if len(f.key) > len(best) {
			best = f.key
		}
	}
	return best, best != ""
}

func allKeys() map[string]bool {
	m := make(map[string]bool, len(configRegistry))
	for _, f := range configRegistry {
		m[f.key] = true
	}
	return m
}

// configRows reads only the keys this binary knows. The settings table is
// shared with the keyring, the CSRF secret and the import markers, so a
// SELECT * would hand foreign rows to the registry.
func configRows(ctx context.Context, d *DB) (map[string]string, error) {
	out := map[string]string{}
	rows, err := d.Read.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("read stored configuration: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan stored configuration: %w", err)
		}
		if ConfigKeyKnown(k) {
			out[k] = v
		}
	}
	return out, rows.Err()
}
