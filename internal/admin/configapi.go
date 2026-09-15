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

// fieldMeta annotates one setting with where it came from, whether a reload
// applies it, and what it holds. The console builds its editor from kind, so
// this is the whole of what a row needs beyond its value.
type fieldMeta struct {
	Source        string `json:"source"`
	HotReloadable bool   `json:"hot_reloadable"`
	Kind          string `json:"kind"`
	// Env names the variable that owns a bootstrap key, and is empty for a
	// stored one. It is what lets the screen say why a row cannot be edited
	// instead of merely disabling it.
	Env string `json:"env,omitempty"`
}

// bootstrapShown are the environment-owned keys the settings screen displays.
// They are not registry keys and never will be -- a value needed to reach the
// console cannot live in the database the console writes -- but an operator
// still needs to see what the gateway is listening on.
var bootstrapShown = []string{"server.proxy_listen", "server.admin_listen"}

// sourceOf says where one value came from. stored names the registry keys the
// database carries; a key absent from it is on its compiled default, which is
// the distinction the settings screen exists to show.
func sourceOf(field string, stored map[string]bool) string {
	if _, ok := config.BootstrapVar(field); ok {
		return "env"
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
			"values": map[string]string{}, "fields": map[string]fieldMeta{},
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
	stored, err := store.StoredConfigKeys(r.Context(), s.deps.DB)
	if err != nil {
		slog.Warn("config view could not read which settings are stored", "err", err)
	}

	// Values and fields both come from the registry, so a key added there is on
	// the screen with no second edit. The hand-written block tree this replaces
	// is why nine catalogue keys were never visible.
	values := store.ConfigRowsFor(cfg)
	fields := make(map[string]fieldMeta, len(values)+len(bootstrapShown))
	for key := range values {
		kind, _ := store.ConfigKindOf(key)
		fields[key] = fieldMeta{
			Source:        sourceOf(key, stored),
			HotReloadable: !slices.Contains(config.RestartOnly, key),
			Kind:          string(kind),
		}
	}
	for _, key := range bootstrapShown {
		name, _ := config.BootstrapVar(key)
		fields[key] = fieldMeta{
			Source: "env",
			// An environment value is technically hot -- nothing captures it at
			// construction -- but a variable cannot change under a running
			// process, so calling it hot would promise a live edit that is
			// impossible.
			HotReloadable: false,
			Kind:          string(store.KindString),
			Env:           name,
		}
	}
	values["server.proxy_listen"] = cfg.Server.ProxyListen
	values["server.admin_listen"] = cfg.Server.AdminListen

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
		"valid":           cfgErr == nil && len(cfg.Skipped) == 0,
		"warnings":        append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"values":          values,
		"fields":          fields,
		"pending_restart": pending,
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
		// is that the new configuration is invalid and the old one is serving.
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

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
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
		if err := s.aliasTargetsExist(r.Context(), p.Aliases); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Detached from the request so a client that disconnects mid-commit cannot
	// abort a write about to become durable, but bounded: BeginTx waits on the
	// single write connection with no deadline of its own, and this call holds
	// the reload lock while it waits. Without a ceiling, one stuck write would
	// queue every later save and reload behind it forever.
	ctx, cancel := context.WithTimeout(afterCommit(r), 30*time.Second)
	defer cancel()
	written, err := s.deps.Config.Update(ctx, p)
	var rejected config.RejectedError
	var conflict config.ConflictError
	var publish config.PublishError
	switch {
	case errors.As(err, &rejected):
		writeError(w, http.StatusBadRequest, rejected.Error())
	case errors.As(err, &conflict):
		// 409, not 400: the save itself is fine, it was computed against a
		// table that has since moved. A retry that reloads first can succeed
		// where repeating this exact body never would.
		writeError(w, http.StatusConflict, conflict.Error())
	case errors.As(err, &publish):
		// 200 rather than 500: the write was performed and its outcome is the
		// answer. The rows are durable; what failed is the republish, and the
		// previous configuration is still serving.
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "routing_updated": false, "error": publish.Error(),
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

// aliasTargetsExist rejects a chain naming a provider that is not configured.
// It stays admin-side because it needs provider rows: WriteConfig is a
// registry-level write rather than a domain validator, so a check that needs
// provider rows belongs to the API.
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
