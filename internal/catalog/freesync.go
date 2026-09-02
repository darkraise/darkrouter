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

// FreeCatalogURL is OmniRoute's curated free-tier catalogue, read from the
// default branch. Free-tier membership is a fact somebody maintains by hand
// against provider documentation, so the only way to stay current with it is
// to re-read what they publish.
const FreeCatalogURL = "https://raw.githubusercontent.com/diegosouzapw/OmniRoute/HEAD/open-sse/config/freeModelCatalog.data.ts"

// maxFreeCatalogBytes bounds the fetch. The file is around 150 KB; the cap is
// two orders above that, for the same reason the models.dev fetch has one.
const maxFreeCatalogBytes = 16 << 20

// FreeSyncOptions configures the worker. The zero value is the shipped default.
type FreeSyncOptions struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
}

func (o FreeSyncOptions) withDefaults() FreeSyncOptions {
	if o.URL == "" {
		o.URL = FreeCatalogURL
	}
	if o.Interval <= 0 {
		// Daily. The upstream list changes when someone does a research pass,
		// which is weeks apart -- polling it harder would be traffic that
		// cannot find news.
		o.Interval = 24 * time.Hour
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	return o
}

// FreeSyncer keeps the curated free-model catalogue current.
//
// The catalogue is embedded at build time, which freezes it at the release. A
// provider that opens a free tier next month is invisible to a binary built
// today, and the import filter would keep dropping models an operator can use
// for nothing. This re-reads the published list on an interval and serves the
// newest one it has parsed.
type FreeSyncer struct {
	opts   FreeSyncOptions
	client *http.Client
	// catalog is the newest catalogue successfully parsed, starting at the
	// embedded one. Replaced wholesale and read without a lock, which is what
	// makes a failed fetch a no-op rather than a window where it is empty.
	catalog atomic.Pointer[FreeCatalog]
}

func NewFreeSyncer(opts FreeSyncOptions) *FreeSyncer {
	opts = opts.withDefaults()
	s := &FreeSyncer{opts: opts, client: &http.Client{Timeout: opts.Timeout}}
	embedded := FreeModels()
	s.catalog.Store(&embedded)
	return s
}

// Catalog returns the newest catalogue the syncer holds — the embedded one
// until a fetch succeeds. It never returns an empty catalogue on account of a
// failed fetch, so a gateway with no outbound access still filters on the list
// its release shipped with.
func (s *FreeSyncer) Catalog() FreeCatalog {
	if c := s.catalog.Load(); c != nil {
		return *c
	}
	return FreeModels()
}

// Run refreshes on a jittered interval until ctx is cancelled.
func (s *FreeSyncer) Run(ctx context.Context) error {
	// An immediate first fetch, so a gateway that has been off for a month is
	// current within seconds of starting rather than a day later.
	if err := s.SyncOnce(ctx); err != nil {
		slog.Warn("free catalogue sync failed; serving the embedded catalogue", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(s.opts.Interval)):
			if err := s.SyncOnce(ctx); err != nil {
				slog.Warn("free catalogue sync failed; previous catalogue retained", "err", err)
			}
		}
	}
}

// SyncOnce fetches and parses one refresh.
//
// Every failure path returns before the stored catalogue is touched: a gateway
// that lost its free-tier list because GitHub had a bad minute would start
// dropping models the operator asked to keep, which is worse than running on
// last week's list.
func (s *FreeSyncer) SyncOnce(ctx context.Context) error {
	fetched, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	previous := s.Catalog()
	s.catalog.Store(&fetched)
	// Logged rather than silent: this is a background job changing what the
	// gateway imports, and an operator looking at a catalogue that grew by
	// forty models needs somewhere to see why.
	slog.Info("free catalogue synced", "curated_at", fetched.CuratedAt, "models", fetched.count(), "providers", len(fetched.Providers), "was_curated_at", previous.CuratedAt, "was_models", previous.count())
	return nil
}

func (s *FreeSyncer) fetch(ctx context.Context) (FreeCatalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.URL, nil)
	if err != nil {
		return FreeCatalog{}, fmt.Errorf("free catalogue sync: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return FreeCatalog{}, fmt.Errorf("free catalogue sync: fetch: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FreeCatalog{}, fmt.Errorf("free catalogue sync: fetch returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFreeCatalogBytes))
	if err != nil {
		return FreeCatalog{}, fmt.Errorf("free catalogue sync: read: %w", err)
	}
	return ParseFreeCatalog(body)
}

func (c FreeCatalog) count() int {
	n := 0
	for _, models := range c.Providers {
		n += len(models)
	}
	return n
}
