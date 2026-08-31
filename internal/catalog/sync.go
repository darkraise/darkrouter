package catalog

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// ModelsDevURL is the published document. Spec §4.
const ModelsDevURL = "https://models.dev/api.json"

// maxDocBytes bounds the fetch. The real document is around four megabytes; a
// cap two orders above that stops a compromised or broken CDN exhausting
// memory on a worker nobody is watching.
const maxDocBytes = 64 << 20

// SyncOptions configures the worker. The zero value is the shipped default.
type SyncOptions struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
	// Presets overrides the embedded set. Tests use it; production does not.
	Presets Presets
}

func (o SyncOptions) withDefaults() SyncOptions {
	if o.URL == "" {
		o.URL = ModelsDevURL
	}
	if o.Interval <= 0 {
		o.Interval = 12 * time.Hour
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Presets == nil {
		o.Presets = Embedded()
	}
	return o
}

// Syncer refreshes model metadata from models.dev.
type Syncer struct {
	db   *store.DB
	src  provider.Source
	cat  *Store
	opts SyncOptions

	client *http.Client
	// doc is the newest document successfully parsed, starting at the embedded
	// fallback. It is replaced wholesale and read without a lock, which is what
	// makes a failed fetch a no-op rather than a window where it is empty.
	doc atomic.Pointer[Doc]
}

func NewSyncer(db *store.DB, src provider.Source, cat *Store, opts SyncOptions) *Syncer {
	opts = opts.withDefaults()
	s := &Syncer{
		db: db, src: src, cat: cat, opts: opts,
		client: &http.Client{Timeout: opts.Timeout},
	}
	fb := FallbackDoc()
	s.doc.Store(&fb)
	// The store prices against this rather than against the snapshot compiled
	// into the binary. Registered here rather than at the call site so a
	// syncer and the store it rebuilds cannot be wired up half way.
	if cat != nil {
		cat.SetDoc(s.Doc)
	}
	return s
}

// Doc returns the newest metadata the syncer holds — the embedded snapshot
// until a fetch succeeds. It never returns nil, so a gateway with no outbound
// network still has prices and context windows.
func (s *Syncer) Doc() Doc {
	if d := s.doc.Load(); d != nil {
		return *d
	}
	return Doc{}
}

// Run syncs on a jittered interval until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) error {
	// An immediate first sync, so a fresh install is priced without waiting
	// half a day. A failure here is expected on an offline install and must
	// not stop the worker.
	if err := s.SyncOnce(ctx); err != nil {
		log.Printf("models.dev sync: %v (serving embedded metadata)", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(s.opts.Interval)):
			if err := s.SyncOnce(ctx); err != nil {
				log.Printf("models.dev sync: %v (previous metadata retained)", err)
			}
		}
	}
}

// SyncOnce fetches, maps, and writes.
//
// Every failure path returns before touching the database. That is the whole
// contract from spec §4: a fetch error leaves the cache alone and logs a
// warning, because a gateway that loses its prices when a CDN has a bad minute
// is worse than one running on yesterday's numbers.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	doc, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	s.doc.Store(&doc)

	providers, err := s.src.Providers(ctx)
	if err != nil {
		return fmt.Errorf("models.dev sync: providers: %w", err)
	}
	presetOf := make(map[string]Preset, len(providers))
	for _, p := range providers {
		presetOf[p.ID] = s.opts.Presets[p.Preset]
	}

	rows, err := s.db.Models(ctx)
	if err != nil {
		return fmt.Errorf("models.dev sync: models: %w", err)
	}

	updates := make([]store.MetadataRow, 0, len(rows))
	for _, r := range rows {
		preset, ok := presetOf[r.ProviderID]
		if !ok {
			continue // an orphan row; the merge drops it anyway
		}
		meta, joined := Join(preset, doc, r.ModelID)
		if !joined {
			// Spec §4.1: a model that fails to join is not an error. Leaving
			// its row untouched is what keeps its inferred metadata inferred
			// rather than acquiring somebody else's numbers.
			continue
		}

		// A runtime that read its own capabilities, or an operator who set
		// them, outranks a directory's entry for a model of the same name. The
		// row then keeps both its label and its values — keeping only the
		// label would be worse than either.
		source := sourceAfterSync(r.CapabilitiesSource)
		caps := r.Capabilities
		if source == string(SourceModelsDev) {
			caps = store.ModelCapabilities{
				Tools:     meta.Capabilities.Tools,
				Vision:    meta.Capabilities.Vision,
				Reasoning: meta.Capabilities.Reasoning,
			}
		}

		updates = append(updates, store.MetadataRow{
			ProviderID:      r.ProviderID,
			ModelID:         r.ModelID,
			Publisher:       r.Publisher,
			Surfaces:        r.Surfaces,
			ContextWindow:   meta.ContextWindow,
			MaxOutputTokens: meta.MaxOutputTokens,
			// Limits and pricing always come from models.dev when the join
			// succeeded: precedence is per field, not per record.
			Capabilities:            caps,
			CapabilitiesSource:      source,
			InputMicrosPerMTok:      meta.InputMicrosPerMTok,
			OutputMicrosPerMTok:     meta.OutputMicrosPerMTok,
			CacheReadMicrosPerMTok:  meta.CacheReadMicrosPerMTok,
			CacheWriteMicrosPerMTok: meta.CacheWriteMicrosPerMTok,
		})
	}

	if len(updates) > 0 {
		if err := s.db.UpsertMetadata(ctx, updates); err != nil {
			return fmt.Errorf("models.dev sync: write: %w", err)
		}
	}
	if err := s.cat.Rebuild(ctx); err != nil {
		return fmt.Errorf("models.dev sync: rebuild: %w", err)
	}
	return nil
}

// sourceAfterSync keeps a discovered source discovered. Capabilities a runtime
// reported about itself are better evidence than a directory's entry for a
// model of the same name, and the merge encodes the same order.
func sourceAfterSync(current string) string {
	if current == string(SourceDiscovered) || current == string(SourceOverride) {
		return current
	}
	return string(SourceModelsDev)
}

func (s *Syncer) fetch(ctx context.Context) (Doc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: fetch: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models.dev sync: fetch returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocBytes))
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: read: %w", err)
	}
	return ParseModelsDev(body)
}
