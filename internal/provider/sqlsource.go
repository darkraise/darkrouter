package provider

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"

	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/store"
)

// SQLSource serves providers from SQLite. The set is loaded and decrypted once
// by Reload and held in memory, because Providers is on the request path and
// running decryption per request would be absurd.
type SQLSource struct {
	db  *store.DB
	key *crypto.Key

	mu        sync.RWMutex
	providers []Provider
	rev       uint64
}

func NewSQLSource(db *store.DB, key *crypto.Key) *SQLSource {
	// providers starts as an empty slice rather than nil so Providers has a
	// sane answer before the first Reload.
	return &SQLSource{db: db, key: key, providers: []Provider{}}
}

// Reload re-reads the provider set. It replaces the cache only on full success,
// so a failure leaves the previous set live — the same rule the config store
// applies to a broken edit.
func (s *SQLSource) Reload(ctx context.Context) error {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, kind, base_url, priority
		   FROM providers
		  WHERE enabled = 1
		  ORDER BY priority DESC, id`)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	type row struct {
		id, kind, baseURL string
		priority          int
	}
	var raw []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.kind, &r.baseURL, &r.priority); err != nil {
			return fmt.Errorf("scan provider: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate providers: %w", err)
	}

	out := make([]Provider, 0, len(raw))
	for _, r := range raw {
		creds, err := s.db.Credentials(ctx, s.key, r.id)
		if err != nil {
			return fmt.Errorf("provider %q: %w", r.id, err)
		}
		enabled := enabledOnly(creds)
		if len(enabled) == 0 {
			// A provider with no usable credential cannot serve. Skipping it
			// here is what stops every request against it failing at the
			// transport layer instead.
			continue
		}
		models, err := s.models(ctx, r.id)
		if err != nil {
			return err
		}
		out = append(out, Provider{
			ID: r.id, Kind: r.kind, BaseURL: r.baseURL,
			Credentials: enabled,
			Priority:    r.priority, Models: models,
		})
	}

	rev := revisionOf(out)
	s.mu.Lock()
	s.providers, s.rev = out, rev
	s.mu.Unlock()
	return nil
}

func (s *SQLSource) models(ctx context.Context, providerID string) ([]string, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT model_id FROM models WHERE provider_id = ? AND state = 'active' ORDER BY model_id`,
		providerID)
	if err != nil {
		return nil, fmt.Errorf("list models for %q: %w", providerID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// enabledOnly keeps the enabled credentials in id order.
//
// That order is total and deterministic, which is all credential rotation needs
// as a starting point — the least-recently-used ordering supplies recency. It
// is deliberately not described as insertion order: two ULIDs minted in the
// same millisecond share a timestamp prefix and are separated only by their
// random component.
func enabledOnly(creds []store.Credential) []Credential {
	out := make([]Credential, 0, len(creds))
	for _, c := range creds {
		if !c.Enabled {
			continue
		}
		out = append(out, Credential{ID: c.ID, Secret: c.Secret, Enabled: true})
	}
	return out
}

// Providers returns the cached set. The slice is never mutated after Reload
// builds it, so returning it directly is safe.
func (s *SQLSource) Providers(context.Context) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.providers, nil
}

// Revision changes when the provider set changes, so callers can cache.
func (s *SQLSource) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rev
}

// revisionOf hashes the identity of the set rather than a counter, so two
// reloads that read the same rows produce the same revision and consumer caches
// survive a no-op reload.
func revisionOf(ps []Provider) uint64 {
	sorted := make([]Provider, len(ps))
	copy(sorted, ps)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].ID < sorted[b].ID })

	h := fnv.New64a()
	for _, p := range sorted {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		_, _ = h.Write([]byte(strconv.Itoa(p.Priority)))
		for _, c := range p.Credentials {
			_, _ = h.Write([]byte(c.ID))
		}
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

var _ Source = (*SQLSource)(nil)
