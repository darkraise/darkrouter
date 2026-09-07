package config

import "testing"

// env turns a map into a LookupEnv, so a test declares the variables it sets
// rather than mutating the process environment.
func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestBootstrapFallsBackToDefaults(t *testing.T) {
	b := BootstrapFrom(env(nil))
	if b.ProxyListen != ":18080" || b.AdminListen != ":18081" {
		t.Errorf("listen = %q/%q, want the defaults", b.ProxyListen, b.AdminListen)
	}
	// Empty means proxy authentication is off, which is why a machine that has
	// never set it still starts.
	if b.ProxyToken != "" {
		t.Errorf("ProxyToken = %q, want empty", b.ProxyToken)
	}
}

func TestBootstrapReadsTheEnvironment(t *testing.T) {
	b := BootstrapFrom(env(map[string]string{
		"DARKROUTER_PROXY_LISTEN": "127.0.0.1:9000",
		"DARKROUTER_ADMIN_LISTEN": "127.0.0.1:9001",
		"DARKROUTER_PROXY_TOKEN":  "sekrit",
	}))
	if b.ProxyListen != "127.0.0.1:9000" || b.AdminListen != "127.0.0.1:9001" {
		t.Errorf("listen = %q/%q, want the environment's", b.ProxyListen, b.AdminListen)
	}
	if b.ProxyToken != "sekrit" {
		t.Errorf("ProxyToken = %q, want the environment's", b.ProxyToken)
	}
}

// An operator who exports an empty variable meant "unset", not "listen on the
// port named by the empty string", which would fail to bind.
func TestBootstrapTreatsAnEmptyListenAsUnset(t *testing.T) {
	b := BootstrapFrom(env(map[string]string{"DARKROUTER_PROXY_LISTEN": "  "}))
	if b.ProxyListen != ":18080" {
		t.Errorf("ProxyListen = %q, want the default", b.ProxyListen)
	}
}

// The empty-means-unset rule is right for a listen address and dangerous for
// the secret: an unset proxy token turns proxy authentication off, so a
// whitespace-only value trimmed to "" would open the gateway rather than close
// it. The token is read raw for exactly that reason.
func TestBootstrapDoesNotTreatAWhitespaceTokenAsUnset(t *testing.T) {
	b := BootstrapFrom(env(map[string]string{"DARKROUTER_PROXY_TOKEN": "  "}))
	if b.ProxyToken == "" {
		t.Error("ProxyToken = empty for a whitespace-only value, which switches proxy authentication off")
	}
	if b.ProxyToken != "  " {
		t.Errorf("ProxyToken = %q, want the raw value", b.ProxyToken)
	}
}
