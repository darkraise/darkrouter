package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// fieldMeta annotates one config value with where it came from and whether a
// reload can apply it. Spec §8.2: the settings screen shows both, and a value
// with neither is one an operator has to guess about.
type fieldMeta struct {
	Source        string `json:"source"`
	HotReloadable bool   `json:"hot_reloadable"`
}

// databaseOwned names the blocks that live in SQLite after the first-run
// import. Editing either in the file has no effect, and §8.1 requires the
// config view to say so at the point of display.
var databaseOwned = []string{"aliases", "policy"}

// configFields is every key the settings screen can show. Listed rather than
// reflected: reflection would expose whatever the struct happens to carry,
// including server.proxy_token, and phase 7 §4.1 forbids returning credential
// material from any endpoint.
var configFields = []string{
	"server.proxy_listen",
	"server.admin_listen",
	"server.max_body_bytes",
	"server.shutdown_grace",
	"server.sse.max_line_bytes",
	"server.sse.max_precommit_bytes",
	"log.retention",
	"capture.bodies",
	"capture.max_bytes",
	"capture.retention",
	"catalog.models_dev_url",
	"catalog.sync_interval",
	"catalog.sync_timeout",
	"catalog.discovery.enabled",
	"catalog.discovery.interval",
	"policy.cooldown.trip_after",
	"policy.cooldown.max",
	"policy.retry.max_attempts",
	"policy.timeout.connect",
	"policy.timeout.first_byte",
	"policy.timeout.total",
	"policy.timeout.idle",
	"aliases",
}

func sourceOf(field string, cfg *config.Config) string {
	for _, owned := range databaseOwned {
		if field == owned || strings.HasPrefix(field, owned+".") {
			return "database"
		}
	}
	if slices.Contains(cfg.FileKeys, field) {
		return "file"
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
		})
		return
	}
	cfg := s.deps.Config.Current()
	// Read once: two calls could straddle a reload and report a valid config
	// with an error attached, or an invalid one with none.
	cfgErr := s.deps.Config.LastError()

	fields := make(map[string]fieldMeta, len(configFields))
	for _, f := range configFields {
		fields[f] = fieldMeta{
			Source:        sourceOf(f, cfg),
			HotReloadable: !slices.Contains(config.RestartOnly, f),
		}
	}

	body := map[string]any{
		"valid":    cfgErr == nil,
		"warnings": append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"fields":   fields,
		"blocks": map[string]any{
			// server.proxy_token is deliberately absent: it is a shared secret
			// and no endpoint returns credential material.
			"server": map[string]any{
				"proxy_listen":   cfg.Server.ProxyListen,
				"admin_listen":   cfg.Server.AdminListen,
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
			"aliases": cfg.Aliases,
			"policy":  policyBlock(cfg.Policy),
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

// configWrite is the accepted shape of PUT /api/config. Only the two blocks
// that live in SQLite are writable; everything else is the file's, and an
// endpoint that pretended otherwise would accept a write it cannot keep.
type configWrite struct {
	Aliases map[string][]string `json:"aliases"`
	Policy  *policyWrite        `json:"policy"`
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cold := restartOnlyIn(body.Policy); len(cold) > 0 {
		writeError(w, http.StatusBadRequest,
			joinFields(cold)+" takes effect on restart and cannot be written here")
		return
	}

	ctx := r.Context()
	if body.Aliases != nil {
		if err := config.ValidateAliases(body.Aliases); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.aliasTargetsExist(ctx, body.Aliases); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.deps.DB.PutAliases(ctx, body.Aliases); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if body.Policy != nil {
		next := s.deps.Config.Current().Policy
		if err := applyPolicyWrite(&next, body.Policy); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.deps.DB.PutPolicy(ctx, next); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// Republish so the next snapshot a request takes carries the write. The
	// overlay is what pulls it back out of SQLite.
	if err := s.deps.Config.Reload(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": err.Error(),
			"serving": "the previous configuration is still serving",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
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
