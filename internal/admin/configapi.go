package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store"
)

// fieldMeta annotates one config value with where it came from and whether a
// reload can apply it. Spec §8.2: the settings screen shows both, and a value
// with neither is one an operator has to guess about.
type fieldMeta struct {
	Source        string `json:"source"`
	HotReloadable bool   `json:"hot_reloadable"`
}

// databaseOwned names the blocks that live in SQLite whether or not a row has
// been written for them: the console is where they are edited, and there is
// nowhere else they could have come from. Every other key is reported as
// stored or not from what the database actually carries.
//
// policy is deliberately not in here. Its seven keys are ordinary registry
// rows, and ReconcileConfig exists to delete the ones equal to the compiled
// default; claiming the whole block came from the database would report those
// deleted rows as values the operator chose.
var databaseOwned = []string{"aliases"}

// bootstrapOwned names the keys the process reads from its environment before
// the database is open. They cannot be stored, so reporting them as a database
// value would send an operator to a screen that cannot change them.
var bootstrapOwned = []string{"server.proxy_listen", "server.admin_listen"}

// configFields is every key the settings screen can show. Listed rather than
// reflected: reflection would expose whatever the struct happens to carry,
// including server.proxy_token, and phase 7 §4.1 forbids returning credential
// material from any endpoint.
var configFields = []string{
	"server.proxy_listen",
	"server.admin_listen",
	"server.public_url",
	"server.max_body_bytes",
	"server.shutdown_grace",
	"server.sse.max_line_bytes",
	"server.sse.max_precommit_bytes",
	"log.retention",
	"capture.bodies",
	"capture.max_bytes",
	"capture.retention",
	"playground.save_conversations",
	"catalog.models_dev_url",
	"catalog.sync_interval",
	"catalog.sync_timeout",
	"catalog.discovery.enabled",
	"catalog.discovery.interval",
	"media.inline",
	"policy.cooldown.trip_after",
	"policy.cooldown.max",
	"policy.retry.max_attempts",
	"policy.timeout.connect",
	"policy.timeout.first_byte",
	"policy.timeout.total",
	"policy.timeout.idle",
	"aliases",
}

// sourceOf says where one value came from. stored names the registry keys the
// database carries; a key absent from it is on its compiled default, which is
// the distinction the settings screen exists to show.
func sourceOf(field string, stored map[string]bool) string {
	for _, owned := range databaseOwned {
		if field == owned || strings.HasPrefix(field, owned+".") {
			return "database"
		}
	}
	if slices.Contains(bootstrapOwned, field) {
		return "environment"
	}
	if stored[field] {
		return "database"
	}
	return "default"
}

// handleConfig renders the whole parsed configuration read-only, with each
// value's source and whether a reload applies it.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": true, "warnings": []string{},
			"blocks": map[string]any{}, "fields": map[string]fieldMeta{},
			"pending_restart": []string{},
		})
		return
	}
	cfg := s.deps.Config.Current()
	// Read once: two calls could straddle a reload and report a valid config
	// with an error attached, or an invalid one with none.
	cfgErr := s.deps.Config.LastError()

	// A read failure degrades the label to "default" rather than failing the
	// screen: an operator who cannot see their settings at all is worse off
	// than one seeing a source annotation that is too modest.
	var stored map[string]bool
	if s.deps.DB != nil {
		var err error
		if stored, err = store.StoredConfigKeys(r.Context(), s.deps.DB); err != nil {
			slog.Warn("config view could not read which settings are stored", "err", err)
		}
	}

	fields := make(map[string]fieldMeta, len(configFields))
	for _, f := range configFields {
		fields[f] = fieldMeta{
			Source:        sourceOf(f, stored),
			HotReloadable: !slices.Contains(config.RestartOnly, f),
		}
	}

	// Never null: a client cannot tell a JSON null from a field an older build
	// did not serve. Measured against the snapshot this process booted on, so
	// an unrelated save cannot clear a notice whose old value is still live.
	pending := s.deps.Config.PendingRestart()
	if pending == nil {
		pending = []string{}
	}

	body := map[string]any{
		// The same expression /healthz keys config_valid on. A skipped key is
		// a default the operator did not choose, and the settings banner reads
		// this field: the two endpoints must not disagree about it.
		"valid":    cfgErr == nil && len(cfg.Skipped) == 0,
		"warnings": append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"fields":   fields,
		// Carried, not rendered: phase 3 owns the console surface for it.
		"pending_restart": pending,
		"blocks": map[string]any{
			// server.proxy_token is deliberately absent: it is a shared secret
			// and no endpoint returns credential material.
			"server": map[string]any{
				"proxy_listen":   cfg.Server.ProxyListen,
				"admin_listen":   cfg.Server.AdminListen,
				"public_url":     cfg.Server.PublicURL,
				"max_body_bytes": cfg.Server.MaxBodyBytes,
				"shutdown_grace": cfg.Server.ShutdownGrace.String(),
				"sse": map[string]any{
					"max_line_bytes":      cfg.Server.SSE.MaxLineBytes,
					"max_precommit_bytes": cfg.Server.SSE.MaxPrecommitBytes,
				},
			},
			"log": map[string]any{"retention": cfg.Log.Retention.String()},
			"capture": map[string]any{
				"bodies":    cfg.Capture.Bodies,
				"max_bytes": cfg.Capture.MaxBytes,
				"retention": cfg.Capture.Retention.String(),
			},
			"catalog": map[string]any{
				"models_dev_url": cfg.Catalog.ModelsDevURL,
				"sync_interval":  cfg.Catalog.SyncInterval.String(),
				"sync_timeout":   cfg.Catalog.SyncTimeout.String(),
				"discovery": map[string]any{
					"enabled":  cfg.Catalog.Discovery.Enabled == nil || *cfg.Catalog.Discovery.Enabled,
					"interval": cfg.Catalog.Discovery.Interval.String(),
				},
			},
			"media":      map[string]any{"inline": cfg.MediaInline()},
			"playground": map[string]any{"save_conversations": cfg.SaveConversations()},
			"aliases":    cfg.Aliases,
			"policy":     policyBlock(cfg.Policy),
		},
	}
	if cfgErr != nil {
		// Stated alongside the error, because a config that failed validation
		// is not a config that stopped serving: the previous one is still live.
		body["error"] = cfgErr.Error()
		body["serving"] = "the previous configuration is still serving"
	}
	writeJSON(w, http.StatusOK, body)
}

func policyBlock(p config.PolicyConfig) map[string]any {
	cooldown := map[string]any{"max": p.Cooldown.Max.String()}
	if p.Cooldown.TripAfter != nil {
		cooldown["trip_after"] = *p.Cooldown.TripAfter
	}
	return map[string]any{
		"cooldown": cooldown,
		"retry":    map[string]any{"max_attempts": p.Retry.MaxAttempts},
		"timeout": map[string]any{
			"connect":    p.Timeout.Connect.String(),
			"first_byte": p.Timeout.FirstByte.String(),
			"total":      p.Timeout.Total.String(),
			"idle":       p.Timeout.Idle.String(),
		},
	}
}

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	if err := s.deps.Config.Reload(); err != nil {
		// 200 rather than 500: the reload was performed and its outcome is the
		// answer. A 500 would read as "the request failed", when what happened
		// is that the file is invalid and the old config is still serving.
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": err.Error(),
			"serving": "the previous configuration is still serving",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

// configWrite is the accepted shape of PUT /api/config. Set and Reset are two
// collections rather than one map because a save has three intents per key and
// a map cannot express the third: a key in neither is untouched.
type configWrite struct {
	Set     map[string]string   `json:"set"`
	Reset   []string            `json:"reset"`
	Aliases map[string][]string `json:"aliases"`
}

type policyWrite struct {
	Cooldown *struct {
		TripAfter *int    `json:"trip_after"`
		Max       *string `json:"max"`
	} `json:"cooldown"`
	Retry *struct {
		MaxAttempts *int `json:"max_attempts"`
	} `json:"retry"`
	Timeout *struct {
		Connect   *string `json:"connect"`
		FirstByte *string `json:"first_byte"`
		Total     *string `json:"total"`
		Idle      *string `json:"idle"`
	} `json:"timeout"`
}

// restartOnlyIn names the fields a write touched that a running process cannot
// apply. Refused rather than accepted-with-a-warning: a file reload is an
// operator editing a file the process watches, while this is an API answering
// a request it can either honour or cannot.
func restartOnlyIn(w *policyWrite) []string {
	var out []string
	if w == nil || w.Timeout == nil {
		return nil
	}
	if w.Timeout.Connect != nil {
		out = append(out, "policy.timeout.connect")
	}
	if w.Timeout.FirstByte != nil {
		out = append(out, "policy.timeout.first_byte")
	}
	return out
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var body configWrite
	if !decodeJSON(w, r, 64<<10, &body) {
		return
	}
	s.commitConfig(w, r, config.Patch{
		Set: body.Set, Reset: body.Reset, Aliases: body.Aliases,
	})
}

// commitConfig is the one place a configuration write is committed. The alias
// and policy endpoints are shapes over it rather than paths of their own, so a
// value refused on one is refused on all three and there is a single critical
// section to reason about.
func (s *Server) commitConfig(w http.ResponseWriter, r *http.Request, p config.Patch) {
	if p.Aliases != nil {
		// Admin-side because it needs provider rows. The loader cannot make
		// this check and internal/store's write path has no business reading
		// a table that belongs to another part of the console.
		if err := s.aliasTargetsExist(r.Context(), p.Aliases); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// WithoutCancel: the write is about to become durable, and a client that
	// disconnects mid-commit must not leave the gateway serving a snapshot
	// that predates rows it now holds.
	written, err := s.deps.Config.Update(afterCommit(r), p)
	var rejected config.RejectedError
	var publish config.PublishError
	switch {
	case errors.As(err, &rejected):
		writeError(w, http.StatusBadRequest, rejected.Error())
	case errors.As(err, &publish):
		// 200 rather than 500: the write was performed and its outcome is the
		// answer. The rows are durable; what failed is the republish, and the
		// previous configuration is still serving.
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": publish.Error(),
			"serving": "the previous configuration is still serving",
		})
	case err != nil:
		internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": true, "restart_required": restartRequired(written),
		})
	}
}

// restartRequired names the written keys a running process cannot apply. They
// are accepted rather than refused: the value belongs in the database either
// way, and refusing it would leave an operator no way to set it at all.
//
// Never nil: a client cannot tell a JSON null from a field an older build did
// not serve.
func restartRequired(written []string) []string {
	out := []string{}
	for _, k := range written {
		if slices.Contains(config.RestartOnly, k) {
			out = append(out, k)
		}
	}
	return out
}

// mergedPolicy overlays a write onto the running policy and validates the
// result, so a partial write is judged as the whole it produces.
func (s *Server) mergedPolicy(w *policyWrite) (config.PolicyConfig, error) {
	next := s.deps.Config.Current().Policy
	if err := applyPolicyWrite(&next, w); err != nil {
		return config.PolicyConfig{}, err
	}
	if err := validatePolicy(next); err != nil {
		return config.PolicyConfig{}, err
	}
	return next, nil
}

// aliasTargetsExist rejects a chain naming a provider that is not configured.
// The file loader cannot make this check -- at load time the providers block
// may not have been imported yet -- but the API can, because by then the
// provider set is in the database.
func (s *Server) aliasTargetsExist(ctx context.Context, aliases map[string][]string) error {
	rows, err := s.deps.DB.ProviderRows(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(rows))
	for _, p := range rows {
		known[p.ID] = true
	}
	for name, chain := range aliases {
		for _, target := range chain {
			id, _, qualified := strings.Cut(target, "/")
			// A bare model name names no provider, so there is nothing to
			// check: the router resolves it across whatever is configured.
			if qualified && !known[id] {
				return fmt.Errorf("alias %q: no provider named %q", name, id)
			}
		}
	}
	return nil
}

func applyPolicyWrite(p *config.PolicyConfig, w *policyWrite) error {
	dur := func(dst *time.Duration, v *string, field string) error {
		if v == nil {
			return nil
		}
		d, err := time.ParseDuration(*v)
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		*dst = d
		return nil
	}
	if w.Cooldown != nil {
		if w.Cooldown.TripAfter != nil {
			p.Cooldown.TripAfter = w.Cooldown.TripAfter
		}
		if err := dur(&p.Cooldown.Max, w.Cooldown.Max, "policy.cooldown.max"); err != nil {
			return err
		}
	}
	if w.Retry != nil && w.Retry.MaxAttempts != nil {
		p.Retry.MaxAttempts = *w.Retry.MaxAttempts
	}
	if w.Timeout != nil {
		if err := dur(&p.Timeout.Total, w.Timeout.Total, "policy.timeout.total"); err != nil {
			return err
		}
		if err := dur(&p.Timeout.Idle, w.Timeout.Idle, "policy.timeout.idle"); err != nil {
			return err
		}
	}
	return nil
}
