package store

import (
	"context"
	"testing"
)

// ImportConfigOnce materialised all seven policy keys on every deployment that
// ever started, so without this pass the console shows them as chosen values
// the operator never picked, and a reset can never take.
func TestReconcileDeletesRowsEqualToTheDefault(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "policy.retry.max_attempts", "4"); err != nil {
		t.Fatal(err)
	}
	if err := putSetting(ctx, db.Write, "log.retention", "100h"); err != nil {
		t.Fatal(err)
	}
	n, err := ReconcileConfig(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("deleted %d rows, want 1", n)
	}
	if _, ok, _ := getSetting(ctx, db.Read, "policy.retry.max_attempts"); ok {
		t.Error("a row equal to the default must be deleted")
	}
	if _, ok, _ := getSetting(ctx, db.Read, "log.retention"); !ok {
		t.Error("a row that differs from the default must be kept")
	}
}

func TestReconcileDropsTheImportMarker(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, settingConfigImportedAt, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileConfig(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := getSetting(ctx, db.Read, settingConfigImportedAt); ok {
		t.Error("the import marker outlived the import and must go")
	}
}

// Rows that belong to other subsystems share this table and must survive.
func TestReconcileLeavesForeignRowsAlone(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "csrf_secret", "keep-me"); err != nil {
		t.Fatal(err)
	}
	// An empty value is what a registry lookup returns for a key the registry
	// does not own. A pass that read the table unfiltered and compared with a
	// single-value lookup would find them equal and delete this row.
	if err := putSetting(ctx, db.Write, "admin.password_env_fingerprint", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileConfig(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := getSetting(ctx, db.Read, "csrf_secret"); !ok {
		t.Error("reconciliation deleted a row it does not own")
	}
	if _, ok, _ := getSetting(ctx, db.Read, "admin.password_env_fingerprint"); !ok {
		t.Error("reconciliation deleted an empty-valued row it does not own")
	}
}

func TestReconcileIsANoOpOnAFreshDatabase(t *testing.T) {
	db := migrated(t)
	n, err := ReconcileConfig(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("deleted %d rows on a fresh database, want 0", n)
	}
}
