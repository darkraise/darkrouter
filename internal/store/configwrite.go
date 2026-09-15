package store

import (
	"context"
	"fmt"
	"slices"
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
// What is judged is the table this save would leave behind, and the loader
// itself does the judging. A key this save touched that the loader would
// revert is refused, because a person is waiting and can be told; a key it did
// not touch was already being reverted before the save ran, and refusing for
// it would leave an operator unable to fix anything at all.
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
	} else if p.AliasesRevisions != nil {
		// Read inside the write transaction rather than trusted from the
		// caller's own snapshot: a revision matched against the table before
		// the transaction opened could still be stale by the time this write
		// lands, and the whole point of the check is to close that gap.
		current, err := aliasesTx(ctx, tx)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(p.AliasesRevisions, config.AliasesRevision(current)) {
			return nil, config.ConflictError{
				Msg: "aliases changed since you loaded them; reload and try again",
			}
		}
	}
	// Checked before the table is judged. buildConfig reverts keys until the
	// configuration validates and no key can fix a broken alias chain, so an
	// unchecked bad set exhausts the loop and comes back as "compiled defaults
	// do not validate", which names nothing an operator can act on.
	if err := config.ValidateAliases(aliases); err != nil {
		return nil, config.RejectedError{Msg: err.Error()}
	}

	// The table this save would leave behind.
	next := make(map[string]string, len(rows)+len(set))
	for k, v := range rows {
		if _, replaced := set[k]; replaced || del[k] {
			continue
		}
		next[k] = v
	}
	for k, v := range set {
		next[k] = v
	}

	// Judged by the loader itself, over the rows this save would leave. That is
	// what makes one validator literal rather than approximate: the write is
	// refused exactly when the next start would throw the operator's value
	// away. Judging the patch against a base that excluded its own keys used
	// the compiled default for the key being written, so a value that broke a
	// cross-key rule with its stored neighbour could pass here and be reverted
	// on the next load, with nothing said to the person who wrote it.
	//
	// Unwrapped: buildConfig fails only when the compiled defaults themselves
	// do not validate, which is a bug in this binary rather than anything the
	// operator wrote. It must reach the caller as a server fault, not as a
	// refusal they could act on.
	_, warnings, skipped, err := buildConfig(next, boot, aliases)
	if err != nil {
		return nil, err
	}
	// A key this save touched that the loader would revert is a refusal: a
	// person is waiting and can be told. A key it did not touch was already
	// being reverted before this save ran, and refusing for it would leave an
	// operator unable to fix anything at all.
	touched := make(map[string]bool, len(set)+len(del))
	for k := range set {
		touched[k] = true
	}
	for k := range del {
		touched[k] = true
	}
	for _, k := range skipped {
		if touched[k] {
			return nil, config.RejectedError{Msg: refusalFor(k, warnings)}
		}
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

	// Sorted, so a patch naming two unknown keys reports the same one every run.
	for _, key := range sortedKeys(p.Set) {
		value := p.Set[key]
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

// refusalFor turns the loader's revert warning into a refusal. The loader
// explains why a key cannot be used and then says it fell back to the default;
// a write has a person waiting, so the reason is kept and the fallback dropped.
func refusalFor(key string, warnings []string) string {
	trim := func(w string) string {
		if i := strings.Index(w, "; "); i >= 0 {
			w = w[:i]
		}
		return strings.TrimPrefix(w, "stored ")
	}
	// The loader's own shape, matched the loader's own way: a prefix, because
	// a stored value quoted into another key's message would otherwise claim
	// this one's refusal.
	for _, w := range warnings {
		if strings.HasPrefix(w, "stored "+key+" ") {
			return trim(w)
		}
	}
	// A rule warning carries its keys inside brackets rather than at the
	// front, so the prefix cannot reach it.
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return trim(w)
		}
	}
	return key + " cannot be used with the rest of the configuration"
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
