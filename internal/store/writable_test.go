package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite answers an unwritable directory with "unable to open database file
// (14)" and an unwritable file with "attempt to write a readonly database
// (8)". Neither names the directory, the uid, or the fix, and both are the
// first thing a bind-mounted deployment can get wrong.
func TestCheckWritableNamesThePathAndTheUser(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(notADir, "darkrouter.db")

	err := CheckWritable(dbPath)
	if err == nil {
		t.Fatal("a database directory that cannot be written was accepted")
	}
	if !strings.Contains(err.Error(), notADir) {
		t.Errorf("error does not name the directory: %v", err)
	}
	if !strings.Contains(err.Error(), "uid") {
		t.Errorf("error does not name the uid the process runs as: %v", err)
	}
}

func TestCheckWritableAcceptsAUsableDirectory(t *testing.T) {
	if err := CheckWritable(filepath.Join(t.TempDir(), "darkrouter.db")); err != nil {
		t.Fatalf("a writable directory was rejected: %v", err)
	}
}

// The probe must not survive: a stray file in the operator's data directory
// is noise at best, and at worst something a later version reads.
func TestCheckWritableLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := CheckWritable(filepath.Join(dir, "darkrouter.db")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the writability probe left %d files behind", len(entries))
	}
}
