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

	// At most two passes are needed: the first reports every key that would
	// not parse, the second retries without them. A rule failure then reverts
	// its own keys and is retried once more, and a rule that still fails with
	// every one of its keys at the compiled default is a bug in the defaults,
	// not in the stored data, so it is returned.
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

	for attempt := 0; attempt < 2; attempt++ {
		err := config.Validate(c)
		if err == nil {
			c.Warnings = append(c.Warnings, warnings...)
			return c, nil
		}
		var re config.RuleError
		if !errors.As(err, &re) {
			// A single-key rule. Nothing distinguishes which stored row caused
			// it, so every stored key reverts and the process runs on
			// defaults rather than refusing to start.
			c, _ = build(allKeys())
			warnings = append(warnings,
				fmt.Sprintf("stored configuration is unusable (%v); every key reverted to its default", err))
			continue
		}
		for _, k := range re.Keys {
			skip[k] = true
		}
		warnings = append(warnings,
			fmt.Sprintf("stored %v broke the %s rule; all of them reverted to their defaults", re.Keys, re.Rule))
		c, _ = build(skip)
	}

	if err := config.Validate(c); err != nil {
		return nil, fmt.Errorf("compiled defaults do not validate: %w", err)
	}
	c.Warnings = append(c.Warnings, warnings...)
	return c, nil
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
