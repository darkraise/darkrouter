// Package config loads darkrouter.yaml, validates it, and hot-reloads it.
package config

import "time"

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Providers []ProviderConfig `yaml:"providers"`
	Policy    PolicyConfig     `yaml:"policy"`

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`
}

type ServerConfig struct {
	ProxyListen   string        `yaml:"proxy_listen"`
	AdminListen   string        `yaml:"admin_listen"`
	ProxyToken    string        `yaml:"proxy_token"`
	MaxBodyBytes  int64         `yaml:"max_body_bytes"`
	ShutdownGrace time.Duration `yaml:"shutdown_grace"`
	SSE           SSEConfig     `yaml:"sse"`
}

type SSEConfig struct {
	MaxLineBytes int `yaml:"max_line_bytes"`
}

type ProviderConfig struct {
	ID       string   `yaml:"id"`
	Kind     string   `yaml:"kind"`
	BaseURL  string   `yaml:"base_url"`
	APIKey   string   `yaml:"api_key"`
	Priority int      `yaml:"priority"`
	Models   []string `yaml:"models"`
}

type PolicyConfig struct {
	Timeout TimeoutConfig `yaml:"timeout"`
}

type TimeoutConfig struct {
	Connect   time.Duration `yaml:"connect"`
	FirstByte time.Duration `yaml:"first_byte"`
	Total     time.Duration `yaml:"total"`
	Idle      time.Duration `yaml:"idle"`
}

// RestartOnly names the fields a hot reload cannot apply. A reload changing one
// is accepted with a warning rather than rejected or silently ignored.
var RestartOnly = []string{"server.proxy_listen", "server.admin_listen", "server.max_body_bytes"}
