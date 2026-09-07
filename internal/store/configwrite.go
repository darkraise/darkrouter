package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/config"
)

// WriteConfig commits one save: the whole of it or none of it. It returns the
// keys the save touched, sorted, so the caller can say which of them wait for
// a restart.
//
// The base configuration comes from the rows inside this transaction rather
// than from the running snapshot. Two saves that are each valid against the
// same snapshot can together break a cross-key rule -- total >= connect +
// first_byte is the live example -- and validating against a snapshot lets
// both commit a state the next load would refuse.
//
// The untouched rows are assembled with the loader's reverting discipline, so
// a row that was already unusable cannot refuse a save that has nothing to do
// with it. The save's own values go on strictly: a value this write introduces
// must be judged, never quietly reverted, or the row it commits is one every
// later start throws away.
func WriteConfig(ctx context.Context, d *DB, boot config.Bootstrap, p config.Patch) ([]string, error) {
	set, del, err := effective(p)
	if err != nil {
		return nil, err
	}

	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin config write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := configRows(ctx, tx)
	if err != nil {
		return nil, err
	}

	aliases := p.Aliases
	if aliases == nil {
		if aliases, err = aliasesTx(ctx, tx); err != nil {
			return nil, err
		}
	}
	// Checked before the base is built. buildConfig reverts keys until the
	// configuration validates and no key can fix a broken alias chain, so an
	// unchecked bad set exhausts the loop and comes back as "compiled defaults
	// do not validate", which names nothing an operator can act on.
	if err := config.ValidateAliases(aliases); err != nil {
		return nil, config.RejectedError{Msg: err.Error()}
	}

	base := make(map[string]string, len(rows))
	for k, v := range rows {
		if _, replaced := set[k]; replaced || del[k] {
			continue
		}
		base[k] = v
	}
	cfg, _, _, err := buildConfig(base, boot, aliases)
	if err != nil {
		return nil, err
	}

	// Sorted, so a patch with two bad values always names the same one first.
	for _, key := range sortedKeys(set) {
		f := configByKey[key]
		if err := f.set(cfg, set[key]); err != nil {
			return nil, config.Rejected("%s: %v", key, err)
		}
		if f.validate != nil {
			if err := f.validate(cfg); err != nil {
				return nil, config.RejectedError{Msg: err.Error()}
			}
		}
	}
	if err := config.Validate(cfg); err != nil {
		return nil, config.RejectedError{Msg: err.Error()}
	}

	for _, key := range sortedKeys(set) {
		if err := putSetting(ctx, tx, key, set[key]); err != nil {
			return nil, err
		}
	}
	for _, key := range sortedFlags(del) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return nil, fmt.Errorf("clear setting %q: %w", key, err)
		}
	}
	if p.Aliases != nil {
		if err := putAliasesTx(ctx, tx, p.Aliases); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit config write: %w", err)
	}

	written := append(sortedKeys(set), sortedFlags(del)...)
	sort.Strings(written)
	return written, nil
}

// effective reduces a patch to the rows to write and the rows to delete, and
// refuses anything that cannot become either. It runs before the transaction
// opens: a patch naming a key that does not exist has nothing to read rows for.
func effective(p config.Patch) (map[string]string, map[string]bool, error) {
	set := map[string]string{}
	del := map[string]bool{}

	known := func(key string) error {
		if ConfigKeyKnown(key) {
			return nil
		}
		if name, ok := config.BootstrapVar(key); ok {
			return config.Rejected("%s is set by %s and cannot be written here", key, name)
		}
		return config.Rejected("unknown setting %q", key)
	}

	for key, value := range p.Set {
		if err := known(key); err != nil {
			return nil, nil, err
		}
		// An empty value is a delete. The column is NOT NULL and would store
		// "" happily, which then reads back as a value the operator chose.
		if strings.TrimSpace(value) == "" {
			del[key] = true
			continue
		}
		set[key] = value
	}
	for _, key := range p.Reset {
		if err := known(key); err != nil {
			return nil, nil, err
		}
		if _, both := set[key]; both {
			return nil, nil, config.Rejected("%s is both set and reset in one save", key)
		}
		del[key] = true
	}
	return set, del, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFlags(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// aliasesTx reads the alias table inside the write transaction, so a save that
// does not carry aliases still validates against the set that will be live
// beside it rather than against one another write may have replaced.
func aliasesTx(ctx context.Context, q rowsQueryer) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, target FROM aliases ORDER BY name, seq`)
	if err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var name, target string
		if err := rows.Scan(&name, &target); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		out[name] = append(out[name], target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	return out, nil
}
