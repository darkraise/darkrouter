package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// defaultConfig is the compiled configuration every test here starts from: a
// zero Config plus applyDefaults, which is exactly what the database loader
// builds before it overlays a stored row.
func defaultConfig() *Config {
	c := &Config{}
	applyDefaults(c)
	return c
}

func TestApplyDefaultsFillsTheServerBlock(t *testing.T) {
	c := defaultConfig()
	if c.Server.MaxBodyBytes != 33554432 {
		t.Errorf("MaxBodyBytes = %d", c.Server.MaxBodyBytes)
	}
	if c.Server.SSE.MaxLineBytes != 1048576 {
		t.Errorf("MaxLineBytes = %d", c.Server.SSE.MaxLineBytes)
	}
	if c.Policy.Timeout.FirstByte != 60*time.Second {
		t.Errorf("FirstByte = %v", c.Policy.Timeout.FirstByte)
	}
	if c.Server.ShutdownGrace != 10*time.Second {
		t.Errorf("ShutdownGrace = %v, want 10s", c.Server.ShutdownGrace)
	}
	if c.Policy.Retry.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want 4", c.Policy.Retry.MaxAttempts)
	}
}

func TestApplyDefaultsFillsThePolicyAndRetentionBlocks(t *testing.T) {
	c := defaultConfig()
	if *c.Policy.Cooldown.TripAfter != 3 {
		t.Errorf("TripAfter = %d, want 3", *c.Policy.Cooldown.TripAfter)
	}
	if c.Policy.Cooldown.Max != 15*time.Minute {
		t.Errorf("Cooldown.Max = %s, want 15m", c.Policy.Cooldown.Max)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("Log.Retention = %s, want 720h", c.Log.Retention)
	}
	if c.Capture.Bodies {
		t.Error("Capture.Bodies must default to false")
	}
	if c.Capture.MaxBytes != 256000 {
		t.Errorf("Capture.MaxBytes = %d, want 256000", c.Capture.MaxBytes)
	}
	if c.Capture.Retention != 72*time.Hour {
		t.Errorf("Capture.Retention = %s, want 72h", c.Capture.Retention)
	}
}

func TestValidateRejectsAnExplicitZeroTripAfter(t *testing.T) {
	c := defaultConfig()
	zero := 0
	c.Policy.Cooldown.TripAfter = &zero
	if err := Validate(c); err == nil {
		t.Fatal("expected trip_after 0 to be rejected")
	}
}

func TestValidateRejectsANegativeRetention(t *testing.T) {
	c := defaultConfig()
	c.Log.Retention = -time.Hour
	if err := Validate(c); err == nil {
		t.Fatal("expected a negative retention to be rejected")
	}
}

func TestRetentionShorterThanTwoDaysIsRejected(t *testing.T) {
	// The daily rollup rewrites yesterday and today wholesale, so pruning
	// must never be able to reach either. 24h looks plausible and is exactly
	// the value that would leave all of yesterday prunable, so it is the one
	// worth pinning rather than an obviously-too-short value like 6h.
	c := defaultConfig()
	c.Log.Retention = 24 * time.Hour
	err := Validate(c)
	if err == nil {
		t.Fatal("24h must be rejected: it leaves all of yesterday prunable")
	}
	if !strings.Contains(err.Error(), "log.retention") {
		t.Fatalf("the error must name the setting, got %q", err)
	}
}

func TestRetentionOfExactlyTwoDaysIsAccepted(t *testing.T) {
	c := defaultConfig()
	c.Log.Retention = 48 * time.Hour
	if err := Validate(c); err != nil {
		t.Fatalf("48h is the floor and must be accepted: %v", err)
	}
}

func TestValidateRejectsANegativeMaxAttempts(t *testing.T) {
	// A written 0 is indistinguishable from an unset key by the time validate
	// runs, so it becomes the default 4. A negative value is the case that can
	// be caught, and the one an operator might produce by arithmetic.
	c := defaultConfig()
	c.Policy.Retry.MaxAttempts = -1
	if err := Validate(c); err == nil {
		t.Fatal("expected a negative max_attempts to be rejected")
	}
}

func TestValidateRejectsANegativeShutdownGrace(t *testing.T) {
	c := defaultConfig()
	c.Server.ShutdownGrace = -time.Second
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "server.shutdown_grace must be positive") {
		t.Errorf("err = %v, want a rejection naming server.shutdown_grace", err)
	}
}

func TestValidateRejectsAnEmptyAlias(t *testing.T) {
	c := defaultConfig()
	c.Aliases = map[string][]string{"broken": {}}
	if err := Validate(c); err == nil {
		t.Fatal("expected an alias with no targets to be rejected")
	}
}

// A provider that does not exist is not an error: providers live in SQLite and
// the loader cannot see them.
func TestValidateAcceptsAnAliasNamingAnUnknownProvider(t *testing.T) {
	c := defaultConfig()
	c.Aliases = map[string][]string{"fast": {"nosuchprovider/model"}}
	if err := Validate(c); err != nil {
		t.Fatalf("an unknown provider in an alias must not fail the load: %v", err)
	}
}

func TestCatalogDefaults(t *testing.T) {
	c := defaultConfig()
	if c.Catalog.ModelsDevURL != "https://models.dev/api.json" {
		t.Errorf("url = %q", c.Catalog.ModelsDevURL)
	}
	if c.Catalog.SyncInterval != 12*time.Hour {
		t.Errorf("sync interval = %v, want 12h", c.Catalog.SyncInterval)
	}
	if c.Catalog.Discovery.Interval != 15*time.Minute {
		t.Errorf("discovery interval = %v, want 15m", c.Catalog.Discovery.Interval)
	}
	if c.Catalog.Discovery.Concurrency != 8 {
		t.Errorf("concurrency = %d, want 8", c.Catalog.Discovery.Concurrency)
	}
	// Enabled is a pointer so "absent" and "explicitly false" stay apart; the
	// default is on.
	if c.Catalog.Discovery.Enabled == nil || !*c.Catalog.Discovery.Enabled {
		t.Errorf("discovery enabled = %v, want true", c.Catalog.Discovery.Enabled)
	}
}

func TestMediaInlineDefaultsOnAndFollowsAnExplicitFalse(t *testing.T) {
	// A pointer, so an explicit false is distinguishable from an unset key.
	// Without that, the default could only be off.
	c := defaultConfig()
	if !c.MediaInline() {
		t.Fatal("media.inline defaults to on")
	}
	off := false
	c.Media.Inline = &off
	if c.MediaInline() {
		t.Fatal("MediaInline() must follow the explicit false")
	}
}

func TestMediaInlineIsRestartOnly(t *testing.T) {
	// The adapters map is built once at startup, so a reload changing this
	// would be accepted, warn about nothing, and take effect at the next
	// process start.
	found := false
	for _, f := range RestartOnly {
		if f == "media.inline" {
			found = true
		}
	}
	if !found {
		t.Fatalf("media.inline missing from RestartOnly: %v", RestartOnly)
	}
}

func TestFreeCatalogSyncDefaultsToDaily(t *testing.T) {
	c := defaultConfig()
	if c.Catalog.FreeCatalogInterval != 24*time.Hour {
		t.Errorf("free catalog interval = %v, want 24h", c.Catalog.FreeCatalogInterval)
	}
	if c.Catalog.FreeCatalogURL == "" {
		t.Error("no default source for the curated free-tier catalogue")
	}
	// Absent means on: a frozen catalogue silently drops models an operator
	// can use for free, and that failure is invisible where a refused
	// outbound call is not.
	if !c.Catalog.FreeCatalogSyncEnabled() {
		t.Error("the daily refresh must default to on")
	}
}

func TestFreeCatalogSyncCanBeTurnedOff(t *testing.T) {
	c := defaultConfig()
	off := false
	c.Catalog.FreeCatalogSync = &off
	if c.Catalog.FreeCatalogSyncEnabled() {
		t.Error("an explicit false did not turn the refresh off")
	}
}

func TestSaveConversationsDefaultsOnAndFollowsAnExplicitFalse(t *testing.T) {
	// The default is on, so an unset key and an explicit false must be
	// distinguishable. A plain bool would read every silent config as off and
	// quietly stop the playground saving anything.
	c := defaultConfig()
	if !c.SaveConversations() {
		t.Error("SaveConversations() = false with the key unset, want true")
	}
	off := false
	c.Playground.SaveConversations = &off
	if c.SaveConversations() {
		t.Error("SaveConversations() = true with the key set to false")
	}
}

func TestTimeoutValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"negative connect", func(c *Config) { c.Policy.Timeout.Connect = -time.Second },
			"policy.timeout.connect must be positive"},
		{"negative first_byte", func(c *Config) { c.Policy.Timeout.FirstByte = -time.Second },
			"policy.timeout.first_byte must be positive"},
		{"negative total", func(c *Config) { c.Policy.Timeout.Total = -time.Second },
			"policy.timeout.total must be positive"},
		{"negative idle", func(c *Config) { c.Policy.Timeout.Idle = -time.Second },
			"policy.timeout.idle must be positive"},
		{"total below one attempt", func(c *Config) {
			c.Policy.Timeout.Connect = 10 * time.Second
			c.Policy.Timeout.FirstByte = 60 * time.Second
			c.Policy.Timeout.Total = 30 * time.Second
		}, "policy.timeout.total (30s) must be at least connect + first_byte (1m10s)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := defaultConfig()
			tc.change(c)
			err := Validate(c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// Exactly connect + first_byte is enough for one attempt.
	c := defaultConfig()
	c.Policy.Timeout.Connect = 10 * time.Second
	c.Policy.Timeout.FirstByte = 60 * time.Second
	c.Policy.Timeout.Total = 70 * time.Second
	if err := Validate(c); err != nil {
		t.Fatalf("a total equal to connect + first_byte must be accepted: %v", err)
	}
}

// server.public_url is how a deployment tells the console where clients reach
// it, and nothing else in the process can know: the value describes what a
// published port or a reverse proxy did on the far side of the listener.
func TestPublicURLIsAcceptedWhenAbsolute(t *testing.T) {
	c := defaultConfig()
	c.Server.PublicURL = "https://api.example.com/darkrouter"
	if err := Validate(c); err != nil {
		t.Fatalf("an absolute public_url must validate: %v", err)
	}
}

func TestPublicURLDefaultsToEmpty(t *testing.T) {
	// Empty is what tells the console to fall back to guessing. A default here
	// would be a guess baked in one layer lower, where it cannot be seen.
	if got := defaultConfig().Server.PublicURL; got != "" {
		t.Errorf("PublicURL = %q, want empty when unset", got)
	}
}

// A bare domain is how an operator says the address out loud, so it is what
// they write. https is supplied because a domain reachable from outside this
// machine has TLS terminated in front of it, and guessing http would put a
// client's token on the wire in the clear.
func TestNormalizeDomainAcceptsABareDomain(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"domain", "llm.example.com", "https://llm.example.com"},
		{"domain and port", "llm.example.com:8443", "https://llm.example.com:8443"},
		{"domain and path", "example.com/darkrouter", "https://example.com/darkrouter"},
		{"surrounding space", "  llm.example.com  ", "https://llm.example.com"},
		{"scheme already written", "http://llm.example.com", "http://llm.example.com"},
		{"https already written", "https://llm.example.com", "https://llm.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDomain(tc.value); got != tc.want {
				t.Errorf("normalizeDomain(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// A value that is not a usable base URL has to fail validation, where the
// setting that carries it is named. Pasted into a client instead, it fails as
// that client's connection error, one layer away from the mistake.
func TestPublicURLRejectsWhatAClientCannotUse(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"scheme relative", "//api.example.com"},
		{"scheme with no host", "https://"},
		{"path only", "/v1"},
		{"query", "https://api.example.com?key=x"},
		{"fragment", "https://api.example.com#v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := defaultConfig()
			c.Server.PublicURL = tc.value
			if err := Validate(c); err == nil {
				t.Fatalf("public_url %q must be refused", tc.value)
			}
		})
	}
}

func TestTimeoutBudgetFailureNamesItsKeys(t *testing.T) {
	// The loader has to know which keys to revert when a stored pair is
	// unusable. A bare error message names no culprit, and reverting the
	// wrong key produces a config the operator did not ask for either.
	c := defaultConfig()
	c.Policy.Timeout.Connect = 30 * time.Second
	c.Policy.Timeout.FirstByte = 60 * time.Second
	c.Policy.Timeout.Total = 40 * time.Second

	err := Validate(c)
	if err == nil {
		t.Fatal("a total below connect + first_byte must be refused")
	}
	var re RuleError
	if !errors.As(err, &re) {
		t.Fatalf("error is %T, want a RuleError naming the keys", err)
	}
	want := []string{"policy.timeout.total", "policy.timeout.connect", "policy.timeout.first_byte"}
	if !slices.Equal(re.Keys, want) {
		t.Errorf("Keys = %v, want %v", re.Keys, want)
	}
}

// The one validator is about settings. Providers have their own tables, their
// own encryption and their own endpoints, and a rule here could only produce a
// message the loader cannot attribute to any key -- which reverts all of them.
func TestValidateHasNoProviderRules(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	if err := Validate(c); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
}
