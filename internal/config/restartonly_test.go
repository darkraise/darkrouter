package config

import (
	"strings"
	"testing"
	"time"
)

// mutations changes one restart-only field each. The test below requires an
// entry for every name in RestartOnly, so adding a field to that list without
// teaching the warning generator about it fails here rather than in production,
// where the operator gets a successful reload and stale behaviour.
var mutations = map[string]func(*Config){
	"server.proxy_listen":           func(c *Config) { c.Server.ProxyListen = ":19090" },
	"server.admin_listen":           func(c *Config) { c.Server.AdminListen = ":19091" },
	"policy.timeout.connect":        func(c *Config) { c.Policy.Timeout.Connect = 3 * time.Second },
	"policy.timeout.first_byte":     func(c *Config) { c.Policy.Timeout.FirstByte = 7 * time.Second },
	"catalog.models_dev_url":        func(c *Config) { c.Catalog.ModelsDevURL = "https://example.invalid/api.json" },
	"catalog.sync_interval":         func(c *Config) { c.Catalog.SyncInterval = 3 * time.Hour },
	"catalog.sync_timeout":          func(c *Config) { c.Catalog.SyncTimeout = 11 * time.Second },
	"catalog.free_catalog_interval": func(c *Config) { c.Catalog.FreeCatalogInterval = 9 * time.Hour },
	"catalog.free_catalog_url":      func(c *Config) { c.Catalog.FreeCatalogURL = "https://example.invalid/free.json" },
	"catalog.free_catalog_sync":     func(c *Config) { f := false; c.Catalog.FreeCatalogSync = &f },
	"catalog.litellm_interval":      func(c *Config) { c.Catalog.LiteLLMInterval = 9 * time.Hour },
	"catalog.litellm_url":           func(c *Config) { c.Catalog.LiteLLMURL = "https://example.invalid/litellm.json" },
	"catalog.litellm_sync":          func(c *Config) { f := false; c.Catalog.LiteLLMSync = &f },
	"catalog.discovery.interval":    func(c *Config) { c.Catalog.Discovery.Interval = 13 * time.Minute },
	"catalog.discovery.enabled":     func(c *Config) { f := false; c.Catalog.Discovery.Enabled = &f },
	"media.inline":                  func(c *Config) { f := false; c.Media.Inline = &f },
}

func TestEveryRestartOnlyFieldWarnsWhenItChanges(t *testing.T) {
	for _, field := range RestartOnly {
		mutate, ok := mutations[field]
		if !ok {
			t.Errorf("%s is in RestartOnly but this test does not exercise it", field)
			continue
		}
		prev := &Config{}
		next := &Config{}
		mutate(next)

		warns := restartOnlyWarnings(prev, next)
		found := false
		for _, w := range warns {
			if strings.Contains(w, field) && strings.Contains(w, "restart") {
				found = true
			}
		}
		if !found {
			t.Errorf("changing %s produced %v; a restart-only field that reloads "+
				"silently tells the operator the change took effect", field, warns)
		}
	}
}

// The reverse direction: a field the warning generator reports has to be one
// the console also marks as cold, or the two disagree about the same edit.
func TestEveryWarnedFieldIsNamedInRestartOnly(t *testing.T) {
	for field, mutate := range mutations {
		next := &Config{}
		mutate(next)
		if len(restartOnlyWarnings(&Config{}, next)) == 0 {
			continue
		}
		found := false
		for _, name := range RestartOnly {
			if name == field {
				found = true
			}
		}
		if !found {
			t.Errorf("a change to %s warns but RestartOnly does not name it, so "+
				"the console offers it as hot-reloadable", field)
		}
	}
}

// The two catalog workers capture these when they are constructed, and whether
// the discovery worker starts at all is decided there too.
func TestRestartOnlyNamesTheWorkerConstructionInputs(t *testing.T) {
	for _, field := range []string{
		"catalog.models_dev_url", "catalog.sync_timeout", "catalog.discovery.enabled",
	} {
		found := false
		for _, got := range RestartOnly {
			if got == field {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is captured at startup but RestartOnly does not name it", field)
		}
	}
}
