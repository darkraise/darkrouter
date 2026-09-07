package config

import "strings"

// Bootstrap is the configuration that cannot live in the database, because it
// is what the process needs before the database is open or in order to be
// reachable at all. Everything else is a row in `settings`.
type Bootstrap struct {
	ProxyListen string
	AdminListen string
	// ProxyToken is the shared inbound secret. It stays an environment
	// variable rather than a stored row because it is a credential, and
	// because the process already reads its other credentials this way.
	ProxyToken string
}

func BootstrapFrom(lookup func(string) (string, bool)) Bootstrap {
	get := func(name, def string) string {
		v, ok := lookup(name)
		if !ok {
			return def
		}
		// An exported-but-empty variable reads as unset. The alternative is
		// binding to the empty string, which fails at listen time with an
		// error that names neither the variable nor this decision.
		if v = strings.TrimSpace(v); v == "" {
			return def
		}
		return v
	}
	return Bootstrap{
		ProxyListen: get("DARKROUTER_PROXY_LISTEN", ":18080"),
		AdminListen: get("DARKROUTER_ADMIN_LISTEN", ":18081"),
		ProxyToken:  get("DARKROUTER_PROXY_TOKEN", ""),
	}
}
