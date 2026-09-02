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

func TestParseAppliesPhase2Defaults(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
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

func TestParseReadsExplicitPhase2Blocks(t *testing.T) {
	body := minimal + `
policy:
  cooldown: { trip_after: 5, max: 30m }
log:
  retention: 168h
capture:
  bodies: true
  max_bytes: 1024
  retention: 1h
`
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if *c.Policy.Cooldown.TripAfter != 5 || c.Policy.Cooldown.Max != 30*time.Minute {
		t.Errorf("cooldown = %d/%s", *c.Policy.Cooldown.TripAfter, c.Policy.Cooldown.Max)
	}
	if c.Log.Retention != 168*time.Hour {
		t.Errorf("Log.Retention = %s", c.Log.Retention)
	}
	if !c.Capture.Bodies || c.Capture.MaxBytes != 1024 || c.Capture.Retention != time.Hour {
		t.Errorf("capture = %+v", c.Capture)
	}
}

func TestParseRejectsExplicitZeroTripAfter(t *testing.T) {
	body := minimal + "\npolicy:\n  cooldown: { trip_after: 0 }\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected trip_after: 0 to be rejected")
	}
}

func TestParseRejectsNonPositiveRetention(t *testing.T) {
	body := minimal + "\nlog:\n  retention: -1h\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected a negative retention to be rejected")
	}
}

func TestRetentionShorterThanTwoDaysIsRejected(t *testing.T) {
	// The daily rollup rewrites yesterday and today wholesale, so pruning
	// must never be able to reach either. 24h looks plausible and is exactly
	// the value that would leave all of yesterday prunable, so it is the one
	// worth pinning rather than an obviously-too-short value like 6h.
	body := minimal + "\nlog:\n  retention: 24h\n"
	_, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err == nil {
		t.Fatal("24h must be rejected: it leaves all of yesterday prunable")
	}
	if !strings.Contains(err.Error(), "log.retention") {
		t.Fatalf("the error must name the setting, got %q", err)
	}
}

func TestRetentionOfExactlyTwoDaysIsAccepted(t *testing.T) {
	body := minimal + "\nlog:\n  retention: 48h\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err != nil {
		t.Fatalf("48h is the floor and must be accepted: %v", err)
	}
}

// The shipped example is documentation that has to stay loadable. It drifted
// once already, naming a model the provider had decommissioned.
func TestShippedExampleParses(t *testing.T) {
	c, err := Load("../../darkrouter.example.yaml", env(map[string]string{
		"GROQ_KEY": "sk-x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if *c.Policy.Cooldown.TripAfter != 3 || c.Policy.Cooldown.Max != 15*time.Minute {
		t.Errorf("cooldown = %d/%s", *c.Policy.Cooldown.TripAfter, c.Policy.Cooldown.Max)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("Log.Retention = %s", c.Log.Retention)
	}
	if c.Capture.MaxBytes != 256000 || c.Capture.Retention != 72*time.Hour {
		t.Errorf("capture = %+v", c.Capture)
	}
}

func TestParseReadsAliases(t *testing.T) {
	body := minimal + `
aliases:
  fast:
    - groq/llama-3.3-70b
    - cerebras/llama-3.3-70b
  coding:
    - anthropic/claude-sonnet-4-5
`
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aliases) != 2 {
		t.Fatalf("got %d aliases, want 2", len(c.Aliases))
	}
	fast := c.Aliases["fast"]
	if len(fast) != 2 || fast[0] != "groq/llama-3.3-70b" {
		t.Errorf("fast = %v", fast)
	}
	// Order is the chain order and must survive the round trip.
	if fast[1] != "cerebras/llama-3.3-70b" {
		t.Errorf("alias order was not preserved: %v", fast)
	}
}

func TestParseDefaultsMaxAttempts(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Retry.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want 4", c.Policy.Retry.MaxAttempts)
	}
}

func TestParseReadsMaxAttempts(t *testing.T) {
	body := minimal + "\npolicy:\n  retry: { max_attempts: 2 }\n"
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Retry.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want 2", c.Policy.Retry.MaxAttempts)
	}
}

// A written 0 is indistinguishable from an omitted key by the time validate
// runs, so it becomes the default 4. A negative value is the case that can be
// caught, and the one an operator might actually produce by arithmetic.
func TestParseRejectsNegativeMaxAttempts(t *testing.T) {
	body := minimal + "\npolicy:\n  retry: { max_attempts: -1 }\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected a negative max_attempts to be rejected")
	}
}

func TestParseRejectsAnEmptyAlias(t *testing.T) {
	body := minimal + "\naliases:\n  broken: []\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected an alias with no targets to be rejected")
	}
}

// A provider that does not exist is a warning, not an error: providers live in
// SQLite and the loader cannot see them.
func TestParseAcceptsAnAliasNamingAnUnknownProvider(t *testing.T) {
	body := minimal + "\naliases:\n  fast:\n    - nosuchprovider/model\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err != nil {
		t.Fatalf("an unknown provider in an alias must not fail the load: %v", err)
	}
}

func TestCatalogDefaults(t *testing.T) {
	c, err := Parse([]byte("server:\n  proxy_listen: \":8080\"\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
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

func TestCatalogBlockParses(t *testing.T) {
	c, err := Parse([]byte(`
server:
  proxy_listen: ":8080"
catalog:
  models_dev_url: https://example.invalid/api.json
  sync_interval: 1h
  discovery:
    enabled: false
    interval: 90s
    concurrency: 2
    timeout: 5s
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Catalog.ModelsDevURL != "https://example.invalid/api.json" {
		t.Errorf("url = %q", c.Catalog.ModelsDevURL)
	}
	if c.Catalog.SyncInterval != time.Hour {
		t.Errorf("sync interval = %v", c.Catalog.SyncInterval)
	}
	if c.Catalog.Discovery.Enabled == nil || *c.Catalog.Discovery.Enabled {
		t.Errorf("discovery enabled = %v, want an explicit false", c.Catalog.Discovery.Enabled)
	}
	if c.Catalog.Discovery.Interval != 90*time.Second || c.Catalog.Discovery.Concurrency != 2 {
		t.Errorf("discovery = %+v", c.Catalog.Discovery)
	}
	if c.Catalog.Discovery.Timeout != 5*time.Second {
		t.Errorf("timeout = %v", c.Catalog.Discovery.Timeout)
	}
}

func TestUnknownCatalogFieldIsRejected(t *testing.T) {
	// KnownFields(true) is what turns a typo into a startup error rather than
	// a setting that silently did nothing.
	_, err := Parse([]byte("catalog:\n  sync_intervals: 1h\n"), nil)
	if err == nil {
		t.Fatal("a misspelled catalog field parsed cleanly")
	}
}

func TestAProviderCanNameItsPreset(t *testing.T) {
	// Without the field the loader rejects the key outright: KnownFields(true)
	// makes an unknown key a validation failure, so a YAML provider could
	// never reach its preset at all.
	c, err := Parse([]byte(`
server:
  proxy_listen: ":0"
providers:
  - id: cohere
    kind: openaicompat
    preset: cohere
    base_url: https://api.cohere.com/compatibility/v1
    api_key: sk
    models: [rerank-v3.5]
`), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 1 || c.Providers[0].Preset != "cohere" {
		t.Fatalf("providers = %+v", c.Providers)
	}
}

func TestParseRecordsWhichKeysTheFileCarried(t *testing.T) {
	// A source marker that says "file" for a value nobody wrote is worse than
	// no marker, so the loader records the keys the document actually had
	// rather than letting the reader infer it from a non-zero value.
	c, err := Parse([]byte(minimal+"\nlog:\n  retention: 96h\n"), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	has := func(key string) bool {
		for _, k := range c.FileKeys {
			if k == key {
				return true
			}
		}
		return false
	}
	if !has("log.retention") {
		t.Errorf("log.retention was in the file but is not recorded: %v", c.FileKeys)
	}
	if has("capture.bodies") {
		t.Errorf("capture.bodies was never written but is recorded as from the file")
	}
	if !has("server.proxy_listen") {
		t.Errorf("server.proxy_listen was in the file but is not recorded: %v", c.FileKeys)
	}
}

func TestMediaInlineDefaultsOnAndParsesOff(t *testing.T) {
	// A pointer, so an explicit false is distinguishable from an absent key.
	// Without that, the default could only be off.
	on, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if !on.MediaInline() {
		t.Fatal("media.inline defaults to on")
	}
	off, err := Parse([]byte(minimal+"media:\n  inline: false\n"),
		env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if off.Media.Inline == nil || *off.Media.Inline {
		t.Fatalf("media.inline should parse false, got %v", off.Media.Inline)
	}
	if off.MediaInline() {
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
	c, err := Parse([]byte("server:\n  proxy_listen: \":8080\"\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
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
	c, err := Parse([]byte("catalog:\n  free_catalog_sync: false\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Catalog.FreeCatalogSyncEnabled() {
		t.Error("an explicit false did not turn the refresh off")
	}
}

func TestSaveConversationsDefaultsOnAndParsesOff(t *testing.T) {
	// The default is on, so an unset key and an explicit false must be
	// distinguishable. A plain bool would read every silent config as off and
	// quietly stop the playground saving anything.
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SaveConversations() {
		t.Error("SaveConversations() = false with the key absent, want true")
	}

	off := minimal + "\nplayground:\n  save_conversations: false\n"
	c, err = Parse([]byte(off), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SaveConversations() {
		t.Error("SaveConversations() = true with the key set to false")
	}
	found := false
	for _, k := range c.FileKeys {
		if k == "playground.save_conversations" {
			found = true
		}
	}
	if !found {
		// The settings screen labels a value "file" or "default" from this
		// list, and a key missing from it is reported as a built-in default
		// the operator never chose.
		t.Errorf("playground.save_conversations not recorded in FileKeys: %v", c.FileKeys)
	}
}

func TestTimeoutValidation(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{"negative connect", "policy:\n  timeout:\n    connect: -1s\n", "policy.timeout.connect must be positive"},
		{"negative first_byte", "policy:\n  timeout:\n    first_byte: -1s\n", "policy.timeout.first_byte must be positive"},
		{"negative total", "policy:\n  timeout:\n    total: -1s\n", "policy.timeout.total must be positive"},
		{"negative idle", "policy:\n  timeout:\n    idle: -1s\n", "policy.timeout.idle must be positive"},
		{"total below one attempt", "policy:\n  timeout:\n    connect: 10s\n    first_byte: 60s\n    total: 30s\n",
			"policy.timeout.total (30s) must be at least connect + first_byte (1m10s)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(minimal+tc.yaml), env(map[string]string{"GROQ_KEY": "sk-x"}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// Exactly connect + first_byte is enough for one attempt.
	if _, err := Parse([]byte(minimal+"policy:\n  timeout:\n    connect: 10s\n    first_byte: 60s\n    total: 70s\n"),
		env(map[string]string{"GROQ_KEY": "sk-x"})); err != nil {
		t.Fatalf("a total equal to connect + first_byte must be accepted: %v", err)
	}
}

func TestShutdownGraceDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.ShutdownGrace != 10*time.Second {
		t.Errorf("ShutdownGrace = %v, want 10s", c.Server.ShutdownGrace)
	}
	negative := strings.Replace(minimal, "server:\n", "server:\n  shutdown_grace: -1s\n", 1)
	if _, err := Parse([]byte(negative), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil ||
		!strings.Contains(err.Error(), "server.shutdown_grace must be positive") {
		t.Errorf("err = %v, want a rejection", err)
	}
}
