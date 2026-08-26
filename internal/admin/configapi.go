package admin

import (
	"net/http"
	"slices"
	"strings"

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
