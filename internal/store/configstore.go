package store

import (
	"context"
	"fmt"
	"sort"
)

// Aliases returns every alias chain, each in its stored order.
//
// An empty result is not the same as "never configured": an operator may have
// deleted every alias through the console. ConfigImported is what separates
// the two.
func (d *DB) Aliases(ctx context.Context) (map[string][]string, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT name, target FROM aliases ORDER BY name, seq`)
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

// PutAliases replaces the whole set in one transaction.
//
// Replace rather than merge: a chain the operator deleted has to disappear,
// and a partial write would leave one chain half-rewritten with its fallback
// order silently changed.
func (d *DB) PutAliases(ctx context.Context, aliases map[string][]string) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin alias write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM aliases`); err != nil {
		return fmt.Errorf("clear aliases: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO aliases (name, seq, target) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare alias insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	// Sorted so a write produces the same rows in the same order every time,
	// which keeps a diff of the database readable.
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for i, target := range aliases[name] {
			if _, err := stmt.ExecContext(ctx, name, i, target); err != nil {
				return fmt.Errorf("insert alias %q: %w", name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alias write: %w", err)
	}
	return nil
}
