package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

const minimal = `
server:
  proxy_listen: :8080
  admin_listen: :8081
providers:
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    models: [llama-3.3-70b-versatile]
`

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.MaxBodyBytes != 33554432 {
		t.Errorf("MaxBodyBytes = %d", c.Server.MaxBodyBytes)
	}
	if c.Server.SSE.MaxLineBytes != 1048576 {
		t.Errorf("MaxLineBytes = %d", c.Server.SSE.MaxLineBytes)
	}
	if c.Policy.Timeout.FirstByte != 60*time.Second {
		t.Errorf("FirstByte = %v", c.Policy.Timeout.FirstByte)
	}
}

func TestParseInterpolatesRequiredEnv(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].APIKey != "sk-x" {
		t.Fatalf("APIKey = %q", c.Providers[0].APIKey)
	}
}

func TestParseRejectsUnresolvedRequiredEnv(t *testing.T) {
	_, err := Parse([]byte(minimal), env(nil))
	if err == nil || !strings.Contains(err.Error(), "GROQ_KEY") {
		t.Fatalf("expected an error naming GROQ_KEY, got %v", err)
	}
}

func TestParseTreatsUnresolvedOptionalEnvAsDisabled(t *testing.T) {
	// The shipped example config references DARKROUTER_PROXY_TOKEN. It must
	// load on a machine that has not set it, with auth simply off.
	withToken := strings.Replace(minimal,
		"  admin_listen: :8081",
		"  admin_listen: :8081\n  proxy_token: ${DARKROUTER_PROXY_TOKEN}", 1)
	c, err := Parse([]byte(withToken), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatalf("optional env must not fail validation: %v", err)
	}
	if c.Server.ProxyToken != "" {
		t.Fatalf("ProxyToken = %q, want empty", c.Server.ProxyToken)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse([]byte("server:\n  nonsense: 1\n"), env(nil))
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}

func TestParseRejectsDuplicateProviderID(t *testing.T) {
	src := minimal + `
  - id: groq
    kind: openaicompat
    base_url: https://example.com/v1
    api_key: ${GROQ_KEY}
    models: [x]
`
	_, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
}

func TestParseRejectsRelativeBaseURL(t *testing.T) {
	src := strings.Replace(minimal, "https://api.groq.com/openai/v1", "/v1", 1)
	_, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected an absolute-URL error, got %v", err)
	}
}

func TestParseWarnsOnDuplicateModelAcrossProviders(t *testing.T) {
	src := minimal + `
  - id: cerebras
    kind: openaicompat
    base_url: https://api.cerebras.ai/v1
    api_key: ${GROQ_KEY}
    models: [llama-3.3-70b-versatile]
`
	c, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "llama-3.3-70b-versatile") {
		t.Fatalf("warnings = %v", c.Warnings)
	}
}
