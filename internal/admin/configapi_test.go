package admin

import (
	"encoding/json"
	"strings"
	"testing"
)

// configBody is the shape GET /api/config returns: every block, each value
// annotated with where it came from and whether changing it does anything.
type configBody struct {
	Valid    bool                 `json:"valid"`
	Warnings []string             `json:"warnings"`
	Blocks   map[string]any       `json:"blocks"`
	Fields   map[string]fieldMeta `json:"fields"`
}

func getConfig(t *testing.T, s *Server) configBody {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/config", "")
	if w.Code != 200 {
		t.Fatalf("GET /api/config = %d: %s", w.Code, w.Body.String())
	}
	var body configBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return body
}

func TestConfigReturnsEveryBlock(t *testing.T) {
	// server was the only block served, so log, capture and catalog had no
	// data source at all behind the settings screen.
	s, _ := testServerFull(t)
	body := getConfig(t, s)
	for _, block := range []string{
		"server", "log", "capture", "catalog", "aliases", "policy",
	} {
		if _, ok := body.Blocks[block]; !ok {
			t.Errorf("block %q missing from the response", block)
		}
	}
	catalog, ok := body.Blocks["catalog"].(map[string]any)
	if !ok {
		t.Fatalf("catalog is %T, want an object", body.Blocks["catalog"])
	}
	if _, ok := catalog["discovery"]; !ok {
		t.Error("catalog.discovery missing; the settings screen reads it")
	}
}

func TestConfigMarksRestartOnlyFieldsAsCold(t *testing.T) {
	s, _ := testServerFull(t)
	body := getConfig(t, s)
	for _, field := range []string{
		"server.proxy_listen",
		"policy.timeout.connect",
		"catalog.sync_interval",
		"catalog.discovery.interval",
	} {
		meta, ok := body.Fields[field]
		if !ok {
			t.Errorf("field %q is not annotated", field)
			continue
		}
		if meta.HotReloadable {
			t.Errorf("%q is restart-only but reports hot_reloadable", field)
		}
	}
	if meta, ok := body.Fields["log.retention"]; !ok || !meta.HotReloadable {
		t.Errorf("log.retention should be hot-reloadable, got %+v", meta)
	}
}

func TestConfigNamesTheSourceOfEachValue(t *testing.T) {
	s, _ := testServerFull(t)
	body := getConfig(t, s)

	if got := body.Fields["server.proxy_listen"].Source; got != "file" {
		t.Errorf("server.proxy_listen source = %q, want file", got)
	}
	// Never written in the fixture's YAML, so it is whatever applyDefaults
	// chose -- reporting it as "file" would be a lie the console repeats.
	if got := body.Fields["capture.max_bytes"].Source; got != "default" {
		t.Errorf("capture.max_bytes source = %q, want default", got)
	}
	if got := body.Fields["aliases"].Source; got != "database" {
		t.Errorf("aliases source = %q, want database", got)
	}
}

func TestConfigNeverEchoesACredential(t *testing.T) {
	// Phase 7 §4.1: no endpoint returns credential material. proxy_token is a
	// shared secret and the config block is the obvious place to leak it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/config", "")
	if strings.Contains(w.Body.String(), "proxy_token") {
		t.Errorf("the config response names proxy_token:\n%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "api_key") {
		t.Errorf("the config response names api_key:\n%s", w.Body.String())
	}
}
