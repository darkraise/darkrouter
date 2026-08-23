package admin

import "net/http"

// handleConfig renders the parsed configuration read-only with its validation
// status. Spec §2 keeps editing out of the UI: aliases and policy live in
// darkrouter.yaml, which is the structural reason the API stays small.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "warnings": []string{}})
		return
	}
	cfg := s.deps.Config.Current()
	// Read once: two calls could straddle a reload and report a valid config
	// with an error attached, or an invalid one with none.
	cfgErr := s.deps.Config.LastError()

	body := map[string]any{
		"valid":    cfgErr == nil,
		"warnings": append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"aliases":  cfg.Aliases,
		"policy":   cfg.Policy,
		"server":   cfg.Server,
	}
	if cfgErr != nil {
		// Stated alongside the error, because a config that failed validation
		// is not a config that stopped serving: the previous one is still live.
		body["error"] = cfgErr.Error()
		body["serving"] = "the previous configuration is still serving"
	}
	writeJSON(w, http.StatusOK, body)
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
