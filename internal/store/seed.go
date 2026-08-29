package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const settingSeededProviders = "seeded_providers"

// SeedProvider is one provider the release offers to add on the operator's
// behalf. It carries the preset's own fields rather than a preset, because
// catalog imports store and the dependency cannot run both ways.
type SeedProvider struct {
	ID        string
	Name      string
	Kind      string
	BaseURL   string
	AuthStyle string
	// FreeModelsOnly narrows what the first sweep imports.
	FreeModelsOnly bool
}

// SeedResult reports what was added. Skipped names the ids that were offered
// before and are therefore not offered again.
type SeedResult struct {
	Added   []string
	Skipped []string
}

// SeedProviders adds a provider row for each offered preset, once ever.
//
// The point is a gateway that routes something the moment it starts: a
// provider needing no credential is one the operator would have added by hand
// with the same two facts the release already holds, and making them go and
// find it in a catalogue of two hundred is a step that buys nothing.
//
// Once ever, not once per start. The marker records every id that has been
// offered, so a provider the operator deletes stays deleted — a seeder that
// re-added it on the next restart would be a gateway arguing with its
// operator. It also means a preset that becomes self-serving in a later
// release is offered then, rather than never.
func SeedProviders(ctx context.Context, d *DB, offer []SeedProvider) (SeedResult, error) {
	if len(offer) == 0 {
		return SeedResult{}, nil
	}
	seeded, err := seededProviders(ctx, d)
	if err != nil {
		return SeedResult{}, err
	}

	var todo []SeedProvider
	var result SeedResult
	for _, p := range offer {
		if seeded[p.ID] {
			result.Skipped = append(result.Skipped, p.ID)
			continue
		}
		todo = append(todo, p)
	}
	if len(todo) == 0 {
		return result, nil
	}

	now := time.Now().UTC().UnixMilli()
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return SeedResult{}, fmt.Errorf("begin seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range todo {
		// An id the operator already used for a provider of their own is
		// theirs. The marker is still written below, so this is decided once
		// rather than on every start.
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM providers WHERE id = ?`, p.ID).Scan(&exists); err != nil {
			return SeedResult{}, fmt.Errorf("check provider %q: %w", p.ID, err)
		}
		if exists > 0 {
			continue
		}
		free := 0
		if p.FreeModelsOnly {
			free = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO providers (id, name, preset, kind, base_url, auth_style,
			                        enabled, free_models_only, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			p.ID, p.Name, p.ID, p.Kind, p.BaseURL, p.AuthStyle, free, now); err != nil {
			return SeedResult{}, fmt.Errorf("seed provider %q: %w", p.ID, err)
		}
		result.Added = append(result.Added, p.ID)
	}

	// Every id that was offered is marked, including the ones skipped because
	// a provider of that id already existed: the question "has this release
	// offered this preset" has been answered either way, and asking again next
	// start would re-add it the moment the operator deleted theirs.
	for _, p := range todo {
		seeded[p.ID] = true
	}
	if err := putSetting(ctx, tx, settingSeededProviders, encodeSeeded(seeded)); err != nil {
		return SeedResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SeedResult{}, fmt.Errorf("commit seed: %w", err)
	}
	return result, nil
}

func seededProviders(ctx context.Context, d *DB) (map[string]bool, error) {
	raw, ok, err := getSetting(ctx, d.Read, settingSeededProviders)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if !ok || raw == "" {
		return out, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		// A marker nobody can read is worse than no marker: the seeder would
		// re-add every provider on every start. Failing loudly is the honest
		// answer, since the fix is one settings row.
		return nil, fmt.Errorf("stored seed marker is not a list of ids: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func encodeSeeded(seeded map[string]bool) string {
	ids := make([]string, 0, len(seeded))
	for id := range seeded {
		ids = append(ids, id)
	}
	// Sorted so the row does not churn between starts that changed nothing.
	sort.Strings(ids)
	b, err := json.Marshal(ids)
	if err != nil {
		// Marshalling a []string cannot fail; the branch exists so the caller
		// is not handed an empty marker if it somehow does.
		return "[]"
	}
	return string(b)
}
