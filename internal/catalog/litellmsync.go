package catalog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// LiteLLMURL is the community price index, read from the default branch. It
// prices models models.dev does not cover, and it moves whenever a vendor
// changes a rate, so staying current with it means re-reading what is
// published.
const LiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// maxLiteLLMBytes bounds the fetch. The index is around 2 MB; the cap is eight
// times that, for the same reason the models.dev fetch has one — a compromised
// or broken CDN must not exhaust memory on a worker nobody is watching.
const maxLiteLLMBytes = 16 << 20

// LiteLLMSyncOptions configures the worker. The zero value is the shipped
// default.
type LiteLLMSyncOptions struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
	// OnUpdate is called after a fetch replaces the index. Prices are joined
	// at merge time, so without it a refreshed index sits unread until
	// something else happens to rebuild the snapshot.
	OnUpdate func(context.Context)
}

func (o LiteLLMSyncOptions) withDefaults() LiteLLMSyncOptions {
	if o.URL == "" {
		o.URL = LiteLLMURL
	}
	if o.Interval <= 0 {
		// Daily. Rate changes are announced days ahead of taking effect, so
		// polling harder would be traffic that cannot find news.
		o.Interval = 24 * time.Hour
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	return o
}

// LiteLLMSyncer keeps the LiteLLM price index current.
//
// Unlike Syncer it holds no *store.DB, and deliberately so: these prices are
// joined in memory when a model's price is resolved, never written to a row.
// Persisting them would freeze one precedence decision into the data, and the
// point of the in-memory join is that a change to precedence re-resolves every
// model without a migration.
type LiteLLMSyncer struct {
	opts   LiteLLMSyncOptions
	client *http.Client
	// doc is the newest index successfully parsed. It is replaced wholesale
	// and read without a lock, which is what makes a failed fetch a no-op
	// rather than a window where it is empty.
	doc atomic.Pointer[LiteLLMDoc]
}

func NewLiteLLMSyncer(opts LiteLLMSyncOptions) *LiteLLMSyncer {
	opts = opts.withDefaults()
	return &LiteLLMSyncer{opts: opts, client: &http.Client{Timeout: opts.Timeout}}
}

// Doc returns the newest index the syncer holds, which is empty until a fetch
// succeeds. There is no embedded fallback: a stale price is billed against and
// is worse than no price, which merely lets another source answer.
func (s *LiteLLMSyncer) Doc() LiteLLMDoc {
	if d := s.doc.Load(); d != nil {
		return *d
	}
	return nil
}

// Run refreshes on a jittered interval until ctx is cancelled.
func (s *LiteLLMSyncer) Run(ctx context.Context) error {
	// An immediate first fetch, so a gateway that has been off for a month is
	// current within seconds of starting rather than a day later.
	if err := s.SyncOnce(ctx); err != nil {
		slog.Warn("litellm price sync failed; no litellm prices available", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(s.opts.Interval)):
			if err := s.SyncOnce(ctx); err != nil {
				slog.Warn("litellm price sync failed; previous index retained", "err", err)
			}
		}
	}
}

// SyncOnce fetches and parses one refresh. Every failure path returns before
// the stored index is touched.
func (s *LiteLLMSyncer) SyncOnce(ctx context.Context) error {
	fetched, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	previous := s.Doc()
	s.doc.Store(&fetched)
	slog.Info("litellm prices synced", "providers", len(fetched), "models", fetched.count(), "was_providers", len(previous), "was_models", previous.count())
	if s.opts.OnUpdate != nil {
		s.opts.OnUpdate(ctx)
	}
	return nil
}

func (s *LiteLLMSyncer) fetch(ctx context.Context) (LiteLLMDoc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("litellm price sync: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm price sync: fetch: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("litellm price sync: fetch returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLiteLLMBytes))
	if err != nil {
		return nil, fmt.Errorf("litellm price sync: read: %w", err)
	}
	doc, err := ParseLiteLLM(body)
	if err != nil {
		return nil, fmt.Errorf("litellm price sync: parse: %w", err)
	}
	// An index that parses to nothing is indistinguishable from a fetch that
	// failed — a truncated body, a moved repository, a renamed provider field
	// all produce it — so it takes the failure path and the previous document
	// survives. Storing it would discard prices that are still good.
	if len(doc) == 0 {
		return nil, fmt.Errorf("litellm price sync: parsed index priced no provider")
	}
	return doc, nil
}

func (d LiteLLMDoc) count() int {
	n := 0
	for _, models := range d {
		n += len(models)
	}
	return n
}
