package storetest

import "testing"

// Every fixture must contain the full migrated schema but none of another
// test's writes, including when fixtures are opened concurrently.
func TestMigratedCopiesAreIndependent(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run("isolated", func(t *testing.T) {
			t.Parallel()
			db := Migrated(t)
			if err := db.Migrate(t.Context()); err != nil {
				t.Fatalf("copied migration history: %v", err)
			}
			claimed, err := db.ClaimFirstUser(t.Context(), "u1", "admin", "fixture-hash")
			if err != nil || !claimed {
				t.Fatalf("claim in fresh fixture = %v, %v", claimed, err)
			}
			if _, ok, err := db.UserByUsername(t.Context(), "admin"); err != nil || !ok {
				t.Fatalf("read back fixture user = %v, %v", ok, err)
			}
		})
	}
}
