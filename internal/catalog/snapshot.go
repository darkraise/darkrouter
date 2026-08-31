package catalog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Snapshot is the catalog frozen at one instant.
//
// It is built once and never mutated, which is what lets router.Resolve stay a
// pure function of its arguments while two background workers rewrite the
// catalog underneath it. Updating one in place would be a data race the router
// has no way to defend against.
type Snapshot struct {
	byProvider map[string]map[string]Model
	offering   map[string][]string
	all        []Model
}

// Filter narrows a Search. The zero value returns every routable model.
type Filter struct {
	Surface    ir.Surface
	ProviderID string
	// Query is a case-insensitive substring match on the model id.
	Query string
	// IncludeRemoved admits models discovery has retired. The UI needs them to
	// show provenance and offer a purge; routing never does.
	IncludeRemoved bool
}

// NewSnapshot indexes a merged model set. providerOrder is the provider set's
// own order, which is priority order — it decides which provider a bare model
// name is attempted against first, so it cannot be recovered by sorting.
func NewSnapshot(models []Model, providerOrder []string) *Snapshot {
	s := &Snapshot{
		byProvider: make(map[string]map[string]Model),
		offering:   make(map[string][]string),
		all:        models,
	}
	for _, m := range models {
		byModel, ok := s.byProvider[m.ProviderID]
		if !ok {
			byModel = make(map[string]Model)
			s.byProvider[m.ProviderID] = byModel
		}
		byModel[m.ModelID] = m
	}
	// Walking providerOrder rather than models is what puts the offering list
	// in priority order.
	for _, pid := range providerOrder {
		for _, m := range models {
			if m.ProviderID != pid || !m.Routable() {
				continue
			}
			s.offering[m.ModelID] = append(s.offering[m.ModelID], pid)
		}
	}
	return s
}

// Lookup returns the model as offered by one provider, retired ones included.
// The trace view and the catalog screen both show a retired model with its
// provenance; only routing excludes it, through Routable.
func (s *Snapshot) Lookup(providerID, modelID string) (Model, bool) {
	byModel, ok := s.byProvider[providerID]
	if !ok {
		return Model{}, false
	}
	m, ok := byModel[modelID]
	return m, ok
}

// Offering returns the provider ids offering modelID in priority order,
// excluding the ones where it has been retired upstream.
func (s *Snapshot) Offering(modelID string) []string { return s.offering[modelID] }

// Search is the catalog screen's query. It is not on the request path.
func (s *Snapshot) Search(f Filter) []Model {
	q := strings.ToLower(f.Query)
	out := make([]Model, 0, len(s.all))
	for _, m := range s.all {
		if !f.IncludeRemoved && !m.Routable() {
			continue
		}
		if f.ProviderID != "" && m.ProviderID != f.ProviderID {
			continue
		}
		if f.Surface != "" && !m.DeclaresSurface(f.Surface) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.ModelID), q) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// All returns every model including retired ones, in merge order.
func (s *Snapshot) All() []Model { return s.all }

var _ Reader = (*Snapshot)(nil)

// Store holds the live snapshot. The zero value is usable and serves an empty
// snapshot, so a request arriving before the first rebuild gets an answer
// rather than a nil dereference.
type Store struct {
	db  *store.DB
	src provider.Source
	cur atomic.Pointer[Snapshot]
	// rebuilding serializes Rebuild against itself. Reading every source and
	// then publishing is not atomic, so two overlapping rebuilds let the one
	// that read older data publish last — and that stale snapshot then serves
	// routing until something rebuilds again. The request path never takes
	// this: Snapshot stays a lock-free atomic load.
	rebuilding sync.Mutex
	// doc supplies the newest models.dev document. Nil means the embedded
	// snapshot, which is what a cold start with no network has.
	doc atomic.Pointer[func() Doc]
}

func NewStore(db *store.DB, src provider.Source) *Store {
	return &Store{db: db, src: src}
}

// SetDoc names the source of the live models.dev document.
func (s *Store) SetDoc(fn func() Doc) { s.doc.Store(&fn) }

// liveDoc returns the newest document available, or the embedded one. An empty
// live document is treated as absent: the syncer serves one before its first
// fetch, and pricing every model as unknown would be worse than being stale.
func (s *Store) liveDoc() Doc {
	if fn := s.doc.Load(); fn != nil && *fn != nil {
		if d := (*fn)(); len(d) > 0 {
			return d
		}
	}
	return FallbackDoc()
}

// empty is shared: it is immutable, so one instance serves every Store that
// has not rebuilt yet.
var empty = NewSnapshot(nil, nil)

// Snapshot returns the live view. The router takes exactly one per request,
// which is what keeps a request's routing decision internally consistent even
// when a worker swaps the catalog mid-flight.
func (s *Store) Snapshot() *Snapshot {
	if cur := s.cur.Load(); cur != nil {
		return cur
	}
	return empty
}

// Set replaces the live snapshot. Exported for tests and for callers that
// build a snapshot from something other than the database.
func (s *Store) Set(snap *Snapshot) { s.cur.Store(snap) }

// Rebuild reads every source and swaps in a new snapshot.
//
// It swaps only on full success: a failed read leaves the previous snapshot
// serving, which is the same rule the config store applies to a broken edit and
// the sync worker applies to an unreachable CDN.
func (s *Store) Rebuild(ctx context.Context) error {
	if s.db == nil || s.src == nil {
		return nil
	}
	s.rebuilding.Lock()
	defer s.rebuilding.Unlock()

	providers, err := s.src.Providers(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: providers: %w", err)
	}
	rows, err := s.db.Models(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: models: %w", err)
	}
	overrides, err := s.db.ModelOverrides(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: overrides: %w", err)
	}

	order := make([]string, 0, len(providers))
	for _, p := range providers {
		order = append(order, p.ID)
	}
	merged := Merge(MergeInput{
		Providers: providers,
		Presets:   Embedded(),
		// The newest document the syncer holds, falling back to the snapshot
		// compiled into the binary. The fallback is what makes a cold start
		// with no network produce real prices rather than waiting twelve hours
		// for the first sync; preferring the live one is what stops a
		// successful sync being invisible, since mergeOne reads the document
		// rather than the row whenever the join succeeds.
		Doc:       s.liveDoc(),
		Rows:      rows,
		Overrides: overrides,
	})
	s.Set(NewSnapshot(merged, order))
	return nil
}
