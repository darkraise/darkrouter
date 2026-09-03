package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func migrated(t *testing.T) *DB {
	t.Helper()
	db := openTest(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrateCreatesEveryTable(t *testing.T) {
	db := migrated(t)
	want := []string{
		"providers", "provider_keys", "models", "model_overrides",
		"requests", "request_attempts", "request_bodies",
		"health", "usage_daily", "sessions", "settings", "aliases",
		"proxy_tokens", "playground_presets",
		"playground_conversations", "playground_messages",
	}
	for _, table := range want {
		var name string
		err := db.Read.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrationRealignsTheProvidersShippedAsKeylessThatAreNot(t *testing.T) {
	// The correction has to reach a provider the operator already added: the
	// row holds the style, so a fixed preset alone would leave it in the No
	// auth group failing every request.
	db := openTest(t)
	ctx := context.Background()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	// Build a database that stops short of this migration, rather than a
	// current one wearing an older version number. That is the situation the
	// migration exists for -- rows written by an older binary, then a deploy --
	// and it keeps every migration applied exactly once, so none of them has to
	// be safe to run twice.
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > 12 {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %04d: %v", m.version, err)
		}
	}

	for _, p := range []struct{ id, preset, style string }{
		{"stale", "hackclub", "optional"},      // the copy the old preset wrote
		{"keyed", "naga-ac", "bearer"},         // already corrected by hand
		{"deliberate", "pollinations", "none"}, // an operator's own override
		{"untouched", "opencode", "optional"},  // still genuinely keyless
	} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO providers (id, preset, kind, base_url, auth_style, created_at)
			 VALUES (?, ?, 'openaicompat', 'https://example.test/v1', ?, 0)`,
			p.id, p.preset, p.style); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"stale": "bearer", "keyed": "bearer", "deliberate": "none", "untouched": "optional",
	}
	for id, style := range want {
		var got string
		if err := db.Read.QueryRowContext(ctx,
			`SELECT auth_style FROM providers WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != style {
			t.Errorf("%s: auth_style = %q, want %q", id, got, style)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want := len(ms)

	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var v int
		if err := db.Read.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		// Pinned to the embedded count rather than a literal: the invariant is
		// that a repeated run lands on the last migration, not that there are
		// seven of them.
		if v != want {
			t.Errorf("run %d: version = %d, want %d", i, v, want)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// Simulate a rollback deploy: the file was written by a future binary.
	if _, err := db.Write.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	err := db.Migrate(ctx)
	if err == nil {
		t.Fatal("expected a newer schema to be refused")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// provider_keys references providers(id); this provider does not exist.
	_, err := db.Write.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, ciphertext, nonce)
		 VALUES ('k1', 'nope', x'00', x'00')`)
	if err == nil {
		t.Fatal("expected the foreign key to reject an orphan credential")
	}
}

func TestMigrationsAreContiguous(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations were embedded")
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %d has version %d, want %d", i, m.version, i+1)
		}
	}
}

// atVersionOne builds a database carrying only migration 0001, so the upgrade
// path itself is exercised rather than a fresh install that never had the old
// values. Using the embedded migration rather than a copy is what keeps this
// honest when 0001 changes.
func atVersionOne(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, ms[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigration0002RewritesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db := atVersionOne(t)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	// Exactly how a phase 2 row was written: both values come from the old
	// defaults, which is the case an ADD COLUMN migration would have missed.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, capabilities_source) VALUES ('p', 'legacy', 'inferred')`); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("upgrade from version 1: %v", err)
	}

	var state, surfaces, source string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, surfaces, capabilities_source FROM models WHERE model_id = 'legacy'`).
		Scan(&state, &surfaces, &source); err != nil {
		t.Fatal(err)
	}
	if state != "live" {
		t.Errorf("state = %q, want live", state)
	}
	if surfaces != `["llm"]` {
		t.Errorf("surfaces = %q, want [\"llm\"]", surfaces)
	}
	// The rebuild must carry every pre-existing column across, not just the
	// two it rewrites.
	if source != "inferred" {
		t.Errorf("capabilities_source = %q, want inferred", source)
	}
}

func TestMigration0002FixesTheDefaults(t *testing.T) {
	// A row written after the migration must not be able to reintroduce the
	// old values. This is what the rebuild buys over ADD COLUMN.
	ctx := context.Background()
	db := migrated(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	var state, surfaces string
	var streak int
	var lastSeen *int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, surfaces, missing_streak, last_seen_at
		   FROM models WHERE model_id = 'm'`).
		Scan(&state, &surfaces, &streak, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if state != "live" || surfaces != `["llm"]` {
		t.Errorf("defaults = (%q, %q), want (live, [\"llm\"])", state, surfaces)
	}
	if streak != 0 || lastSeen != nil {
		t.Errorf("new columns = (%d, %v), want (0, nil)", streak, lastSeen)
	}
}

func TestMigration0002CreatesProviderDiscovery(t *testing.T) {
	var name string
	err := migrated(t).Read.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='provider_discovery'`).Scan(&name)
	if err != nil {
		t.Fatalf("provider_discovery missing: %v", err)
	}
}

func TestMigrationThreeIsAdditive(t *testing.T) {
	// Nothing is dropped or rebuilt, so a failed run leaves the phase 2 schema
	// exactly as it was. Every added column carries a non-null default, which
	// ALTER TABLE ADD COLUMN requires on a STRICT table.
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToUpper(ms[2].sql)
	for _, forbidden := range []string{"DROP TABLE", "DROP COLUMN", "DELETE FROM"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("migration 3 contains %q", forbidden)
		}
	}
	if strings.Count(body, "ADD COLUMN") != 3 {
		t.Errorf("migration 3 adds %d columns, want 3", strings.Count(body, "ADD COLUMN"))
	}
}

func TestEveryMigrationIsEmbedded(t *testing.T) {
	// The loader asserts contiguity from 1, so a mis-numbered file fails here
	// rather than at a customer's first start. Counted against the directory
	// rather than a literal: the risk is a file that exists but was never
	// embedded, and a literal only catches that until someone updates it.
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != len(onDisk) {
		t.Fatalf("loaded %d migrations, %d files on disk", len(ms), len(onDisk))
	}
	if len(ms) == 0 {
		t.Fatal("no migrations were embedded")
	}
}

func TestMigration0006AddsAliasAndAttemptUsage(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	// usage_daily carries alias and keys on it
	var pk string
	err := db.Read.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='usage_daily'`).Scan(&pk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pk, "alias") {
		t.Fatalf("usage_daily has no alias column:\n%s", pk)
	}
	if !strings.Contains(pk, "PRIMARY KEY (day, provider_id, model, alias)") {
		t.Fatalf("usage_daily key was not widened:\n%s", pk)
	}

	// two rows differing only by alias must coexist
	for _, alias := range []string{"", "fast-coder"} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
			 VALUES ('2026-08-25','groq','gpt-oss-120b',?,1)`, alias); err != nil {
			t.Fatalf("insert alias=%q: %v", alias, err)
		}
	}

	// request_attempts carries usage
	for _, col := range []string{"tokens_in", "tokens_out", "cost_micros"} {
		var n int
		if err := db.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM pragma_table_info('request_attempts') WHERE name=?`,
			col).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("request_attempts is missing %s", col)
		}
	}
}

func TestMigration0006PreservesExistingUsageRows(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > 5 {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %04d: %v", m.version, err)
		}
	}

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, requests, tokens_in, tokens_out)
		 VALUES ('2026-08-01','groq','a',5,100,200),
		        ('2026-08-02','openai','b',7,300,400)`); err != nil {
		t.Fatalf("pre-0006 insert: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	var rows, requests, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*), sum(requests), sum(tokens_in) FROM usage_daily`,
	).Scan(&rows, &requests, &tokensIn); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || requests != 12 || tokensIn != 400 {
		t.Fatalf("rows lost across the rebuild: rows=%d requests=%d tokens_in=%d, want 2/12/400",
			rows, requests, tokensIn)
	}

	var alias string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT alias FROM usage_daily WHERE provider_id='groq'`).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if alias != "" {
		t.Fatalf("a row that predates aliases must carry the empty alias, got %q", alias)
	}
}

func TestMigration0007AddsAttemptsAndKeepsRows(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('usage_daily') WHERE name='attempts'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("usage_daily is missing the attempts column")
	}

	// A row written before the column existed must read as zero, not NULL.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','',3)`); err != nil {
		t.Fatal(err)
	}
	var attempts int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT attempts FROM usage_daily`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("attempts must default to 0, got %d", attempts)
	}
}

func TestTheKeysetIndexExists(t *testing.T) {
	// Spec §4.2: the keyset promise is theoretical without this. A query
	// planner check is the only assertion that cannot pass by accident.
	db := migrated(t)
	var plan string
	row := db.Read.QueryRow(
		`EXPLAIN QUERY PLAN
		 SELECT id FROM requests WHERE (ts, id) < (?, ?) ORDER BY ts DESC, id DESC LIMIT 50`,
		1, "z")
	var a, b, c int
	if err := row.Scan(&a, &b, &c, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_requests_keyset") {
		t.Errorf("query plan = %q; the keyset query is not using its index", plan)
	}
}

func TestTheFilterIndexesExist(t *testing.T) {
	// Spec §4.2 names provider, model, status, surface as filter columns.
	// An index missing here turns a filtered page into a full scan.
	db := migrated(t)
	rows, err := db.Read.Query(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'requests'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		have[n] = true
	}
	for _, want := range []string{
		"idx_requests_keyset", "idx_requests_provider_keyset", "idx_requests_model",
		"idx_requests_status", "idx_requests_surface",
		"idx_requests_alias_keyset", "idx_requests_error_keyset",
	} {
		if !have[want] {
			t.Errorf("index %s is missing; have %v", want, have)
		}
	}
}

func TestPriceSourceDefaultsToInferred(t *testing.T) {
	db := migrated(t)
	if _, err := db.Write.Exec(
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO models (provider_id, model_id, capabilities_source) VALUES ('p', 'legacy', 'inferred')`); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.Read.QueryRow(
		`SELECT price_source FROM models WHERE model_id = 'legacy'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "inferred" {
		t.Errorf("price_source = %q, want %q", got, "inferred")
	}
}
