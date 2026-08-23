package catalog

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Health is the slice of the breaker discovery needs. Narrow rather than the
// whole Breaker so a test can supply four methods instead of a live one.
type Health interface {
	Record(k health.Key, s health.Signal)
	Available(k health.Key) bool
	LastUsedSnapshot() map[health.CredKey]time.Time
	MarkUsed(ck health.CredKey, at time.Time)
}

// DiscoveryOptions configures the worker. The zero value is the shipped
// default.
type DiscoveryOptions struct {
	// Interval between sweeps. Spec §5 fixes fifteen minutes.
	Interval time.Duration
	// Concurrency is the cap across the whole fleet, not per provider. A
	// per-provider cap would not stop forty providers opening forty
	// simultaneous connections on boot, which was the stated goal.
	Concurrency int
	// Timeout bounds one probe.
	Timeout time.Duration

	// Listers supplies a per-kind discoverer for a kind with no listing
	// endpoint. A kind absent here keeps phase 6's behavior: undiscoverable
	// kinds are a silent skip, not a failure.
	Listers map[string]KindLister

	// Auth resolves a non-static credential into an authorizer, so a signed
	// control-plane listing can be made. Nil serves static styles only.
	Auth AuthResolver
}

// AuthResolver mirrors exec.AuthResolver. Declared here rather than imported so
// catalog does not depend on exec, which depends on catalog.
type AuthResolver interface {
	For(ctx context.Context, t auth.Target, c auth.Credential) (auth.Authorizer, error)
}

func (o DiscoveryOptions) withDefaults() DiscoveryOptions {
	if o.Interval <= 0 {
		o.Interval = 15 * time.Minute
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	return o
}

// Discoverer probes each enabled provider's listing endpoint.
type Discoverer struct {
	db     *store.DB
	src    provider.Source
	cat    *Store
	health Health
	opts   DiscoveryOptions

	client *http.Client
	// sem is the global cap. One buffered channel shared by every probe is
	// what makes the cap fleet-wide rather than per provider.
	sem     chan struct{}
	trigger chan string
}

// authorizerFor resolves a provider's non-static credential for the discovery
// path. A static style resolves to nil, which is the generic listing's normal
// state.
func (d *Discoverer) authorizerFor(ctx context.Context, p provider.Provider,
	cred provider.Credential) (auth.Authorizer, error) {

	style := p.AuthStyle
	if style == "" {
		style = Embedded()[p.Preset].Auth.Style
	}
	if auth.IsStatic(style) {
		return nil, nil
	}
	if d.opts.Auth == nil {
		return nil, fmt.Errorf("provider %q needs the %s strategy, which is not wired", p.ID, style)
	}
	return d.opts.Auth.For(ctx, auth.Target{
		ProviderID: p.ID, Style: style, Preset: p.Preset,
		Region: p.Region, Project: p.Project, Location: p.Location,
	}, auth.Credential{ID: cred.ID, Kind: cred.Kind, Secret: cred.Secret})
}

func NewDiscoverer(db *store.DB, src provider.Source, cat *Store,
	h Health, opts DiscoveryOptions) *Discoverer {

	opts = opts.withDefaults()
	return &Discoverer{
		db: db, src: src, cat: cat, health: h, opts: opts,
		client: &http.Client{
			Timeout: opts.Timeout,
			// A listing endpoint that redirects is misconfigured; following it
			// would send the credential to whatever host it names.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sem: make(chan struct{}, opts.Concurrency),
		// Buffered so a caller creating a provider never blocks on the worker,
		// and a full buffer drops the request rather than stalling the UI — the
		// next tick covers it either way.
		trigger: make(chan string, 32),
	}
}

// Trigger asks for one provider to be probed now. It never blocks: a dropped
// trigger costs at most one interval, and blocking the admin handler that
// created a provider would cost more.
func (d *Discoverer) Trigger(providerID string) {
	select {
	case d.trigger <- providerID:
	default:
	}
}

// Run sweeps on an interval until ctx is cancelled.
func (d *Discoverer) Run(ctx context.Context) error {
	// An immediate first sweep, so a fresh install shows models without
	// waiting a quarter of an hour.
	d.SweepOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-d.trigger:
			d.probeProvider(ctx, id)
		case <-time.After(jitter(d.opts.Interval)):
			d.SweepOnce(ctx)
		}
	}
}

// SweepOnce probes every enabled provider, bounded by the global cap.
func (d *Discoverer) SweepOnce(ctx context.Context) {
	ps, err := d.src.Providers(ctx)
	if err != nil {
		log.Printf("discovery: providers: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			select {
			case d.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-d.sem }()
			d.probe(ctx, p)
		}(p)
	}
	wg.Wait()
	d.rebuild(ctx)
}

// probeProvider is the on-demand path: one named provider, then a rebuild.
func (d *Discoverer) probeProvider(ctx context.Context, providerID string) {
	ps, err := d.src.Providers(ctx)
	if err != nil {
		log.Printf("discovery: providers: %v", err)
		return
	}
	for _, p := range ps {
		if p.ID != providerID {
			continue
		}
		select {
		case d.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		d.probe(ctx, p)
		<-d.sem
		d.rebuild(ctx)
		return
	}
}

func (d *Discoverer) rebuild(ctx context.Context) {
	if err := d.cat.Rebuild(ctx); err != nil {
		log.Printf("discovery: rebuild: %v", err)
	}
}

// probe runs one provider's listing and applies the result.
func (d *Discoverer) probe(ctx context.Context, p provider.Provider) {
	preset := Embedded()[p.Preset]

	cred, ok := d.pickCredential(p)
	if !ok {
		// Every credential cooling is not a discovery failure. Recording one
		// would walk the provider to stale for a reason that has nothing to do
		// with its listing endpoint.
		return
	}

	pr, err := ProbeForKind(p, preset, cred.Secret, d.opts.Listers)
	if err != nil {
		// An undiscoverable kind is a permanent, known fact rather than a
		// failure. Counting it would retire Vertex's catalogue on the third
		// tick and cool its credential for a call it never made.
		return
	}

	now := time.Now().UTC()

	// A kind with no listing endpoint is seeded from models.dev rather than
	// probed. Spec §4.3: no credential is spent and no request is made, which
	// is what "discovery is not pretended" means in practice. The credential
	// probe confirms reachability separately, on the operator's schedule.
	if seeded := SeedFromPreset(preset, FallbackDoc()); len(seeded) > 0 {
		if err := d.db.RecordDiscoverySuccess(context.WithoutCancel(ctx), p.ID, seeded, now); err != nil {
			log.Printf("discovery: %s: seed: %v", p.ID, err)
		}
		return
	}

	// A signed listing needs the credential turned into a signature. Unlike an
	// undiscoverable kind, a strategy that cannot be resolved is a
	// misconfiguration the operator can fix, so it is recorded rather than
	// skipped in silence.
	if az, aerr := d.authorizerFor(ctx, p, cred); aerr != nil {
		if rerr := d.db.RecordDiscoveryFailure(context.WithoutCancel(ctx), p.ID, now, aerr.Error()); rerr != nil {
			log.Printf("discovery: %s: record failure: %v", p.ID, rerr)
		}
		return
	} else {
		pr.Authorize = az
	}
	d.health.MarkUsed(health.CredKey{ProviderID: p.ID, KeyID: cred.ID}, now)

	models, err := d.list(ctx, pr, p.ID, cred.ID)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown is not a provider failure.
			return
		}
		if rerr := d.db.RecordDiscoveryFailure(context.WithoutCancel(ctx), p.ID, now, err.Error()); rerr != nil {
			log.Printf("discovery: %s: record failure: %v", p.ID, rerr)
		}
		return
	}

	seen := make([]store.DiscoveredModel, 0, len(models))
	for _, m := range models {
		dm := store.DiscoveredModel{
			ModelID:         m.ModelID,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		}
		if preset.CapabilityProbe == "ollama" {
			if caps, ok := d.showCapabilities(ctx, pr, m.ModelID); ok {
				dm.Capabilities = &caps
			}
		}
		seen = append(seen, dm)
	}
	if err := d.db.RecordDiscoverySuccess(context.WithoutCancel(ctx), p.ID, seen, now); err != nil {
		log.Printf("discovery: %s: record success: %v", p.ID, err)
	}
}

// list performs the request and classifies the response.
func (d *Discoverer) list(ctx context.Context, pr Probe, providerID, keyID string) ([]Discovered, error) {
	if pr.Lister != nil {
		// A kind whose model list does not come from one GET. Bedrock needs
		// two signed calls against the control-plane host.
		return pr.Lister.List(ctx, pr)
	}
	req, err := BuildListRequest(ctx, pr)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// A rejected key on a probe is the same evidence as a rejected key on
		// a request, so it cools the credential across every model it serves.
		d.health.Record(
			health.Key{ProviderID: providerID, KeyID: keyID},
			health.Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: resp.StatusCode},
		)
		return nil, fmt.Errorf("listing rejected the credential: %s", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("listing returned %s", resp.Status)
	}

	// Bounded: a listing endpoint that streams unbounded data must not be able
	// to exhaust memory on a background worker nobody is watching.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParseList(pr.Kind, body)
}

// showCapabilities asks a local runtime about one model. A failure is silent:
// the listing already succeeded, and turning "this one model did not answer"
// into a provider-wide discovery failure would retire a working catalogue.
func (d *Discoverer) showCapabilities(ctx context.Context, pr Probe, modelID string) (store.ModelCapabilities, bool) {
	req, err := BuildCapabilityRequest(ctx, pr, modelID)
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.ModelCapabilities{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	return ParseOllamaShow(body)
}

// pickCredential returns the least-recently-used credential that is not
// cooling. Least-recently-used is what spreads probes across quotas instead of
// spending the first key's budget on listing.
func (d *Discoverer) pickCredential(p provider.Provider) (provider.Credential, bool) {
	lastUsed := d.health.LastUsedSnapshot()

	usable := make([]provider.Credential, 0, len(p.Credentials))
	for _, c := range p.Credentials {
		if !c.Enabled {
			continue
		}
		if !d.health.Available(health.Key{ProviderID: p.ID, KeyID: c.ID}) {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) == 0 {
		return provider.Credential{}, false
	}
	sort.SliceStable(usable, func(i, j int) bool {
		ti := lastUsed[health.CredKey{ProviderID: p.ID, KeyID: usable[i].ID}]
		tj := lastUsed[health.CredKey{ProviderID: p.ID, KeyID: usable[j].ID}]
		if ti.Equal(tj) {
			// A total order, so two never-used credentials do not swap on
			// every sweep and produce a different probe each time.
			return usable[i].ID < usable[j].ID
		}
		return ti.Before(tj)
	})
	return usable[0], true
}

// jitter spreads sweeps so a fleet restarted together does not resynchronize
// onto the same instant every interval.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int63n(int64(d)))
}
