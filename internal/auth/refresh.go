package auth

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"
)

// RefreshOptions configures the background worker.
type RefreshOptions struct {
	// Interval is how often the worker looks for expiring credentials, before
	// jitter.
	Interval time.Duration
	// Ahead is how far into the future a credential counts as expiring. Wider
	// than the per-request delta so the worker normally wins the race and no
	// request ever pays for a refresh.
	Ahead time.Duration
	// PerCredential bounds one credential's renewal.
	PerCredential time.Duration
}

func (o RefreshOptions) withDefaults() RefreshOptions {
	if o.Interval <= 0 {
		o.Interval = 5 * time.Minute
	}
	if o.Ahead <= 0 {
		o.Ahead = 15 * time.Minute
	}
	if o.PerCredential <= 0 {
		o.PerCredential = 30 * time.Second
	}
	return o
}

// jitter spreads a duration over half to one and a half times its base.
//
// Spec §5.2: without it, a fleet of accounts connected in the same session
// refreshes in the same second, every time, forever.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}

// ExpiringSource lists credentials due for renewal. It is the narrow half of
// *store.DB the worker needs, bound to a keyring by the caller.
type ExpiringSource interface {
	Expiring(ctx context.Context, kind string, before int64) ([]StoredCredential, error)
}

// StoredCredential is one row the worker may renew.
type StoredCredential struct {
	ID         string
	ProviderID string
	Kind       string
	Secret     string
	// Preset and Style come from the provider the credential belongs to; the
	// worker needs both to resolve a strategy.
	Preset string
	Style  string
}

// RefreshWorker renews OAuth tokens ahead of expiry.
//
// It drives the same authorizer a request would, under the same per-account
// mutex, rather than reimplementing the refresh. That is the point: one refresh
// path exercised two ways, so the worker cannot drift from what serving does.
type RefreshWorker struct {
	mgr  *Manager
	src  ExpiringSource
	opts RefreshOptions
}

func NewRefreshWorker(m *Manager, src ExpiringSource, opts RefreshOptions) *RefreshWorker {
	return &RefreshWorker{mgr: m, src: src, opts: opts.withDefaults()}
}

func (w *RefreshWorker) Run(ctx context.Context) error {
	if w.src == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(w.opts.Interval)):
			w.Once(ctx)
		}
	}
}

// Once renews everything due now. Exported so a test can drive one pass without
// waiting on a timer.
func (w *RefreshWorker) Once(ctx context.Context) {
	before := time.Now().Add(w.opts.Ahead).Unix()
	rows, err := w.src.Expiring(ctx, "oauth", before)
	if err != nil {
		slog.Error("token refresh: list expiring failed", "err", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		w.renew(ctx, row)
	}
}

// renew runs one credential's refresh under a bound of its own.
//
// The pass is sequential and its context lives as long as the process, so a
// token endpoint that accepts the connection and never answers would otherwise
// stop every credential behind it — and every later tick with it, since Run
// calls Once synchronously.
func (w *RefreshWorker) renew(ctx context.Context, row StoredCredential) {
	ctx, cancel := context.WithTimeout(ctx, w.opts.PerCredential)
	defer cancel()

	style := row.Style
	if style == "" {
		style = StyleOAuth
	}
	az, err := w.mgr.For(ctx, Target{
		ProviderID: row.ProviderID, Style: style, Preset: row.Preset,
	}, Credential{ID: row.ID, Kind: row.Kind, Secret: row.Secret})
	if err != nil || az == nil {
		if err != nil {
			slog.Warn("token refresh failed", "credential", row.ID, "err", err)
		}
		return
	}
	// A throwaway request: the authorizer's only observable effect here is
	// the refresh it performs on the way to setting a header nobody reads.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://refresh.invalid", nil)
	if err != nil {
		return
	}
	if err := az(ctx, req); err != nil {
		// Already classified: a terminal refusal disabled the credential
		// inside the authorizer, and a transient one is retried next tick.
		slog.Warn("token refresh failed", "credential", row.ID, "err", err)
	}
}
