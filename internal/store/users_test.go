package store

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

func TestClaimFirstUserSucceedsOnlyOnce(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	won, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("the first claim on an empty database must win")
	}

	// A second claim loses even with a different name: the claim is for the
	// console, not for the username.
	won, err = db.ClaimFirstUser(ctx, "id-2", "bob", "hash-2")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Error("a second claim won; the console can only be claimed once")
	}

	n, err := db.UserCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

func TestClaimFirstUserIsAdmin(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	u, ok, err := db.UserByID(ctx, "id-1")
	if err != nil || !ok {
		t.Fatalf("UserByID: %v ok=%v", err, ok)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, RoleAdmin)
	}
}

func TestUserByUsernameIsCaseInsensitive(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alice", "alice", "ALICE", "aLiCe"} {
		u, ok, err := db.UserByUsername(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("%q did not match the stored account", name)
		}
		if u.ID != "id-1" {
			t.Errorf("%q matched %q, want id-1", name, u.ID)
		}
	}
	// The display spelling is preserved, not folded.
	u, _, _ := db.UserByUsername(ctx, "alice")
	if u.Username != "Alice" {
		t.Errorf("Username = %q, want the spelling as entered", u.Username)
	}
}

func TestUserByUsernameMissIsNotAnError(t *testing.T) {
	db := migrated(t)
	_, ok, err := db.UserByUsername(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("a miss must not be an error: %v", err)
	}
	if ok {
		t.Error("ok = true for an account that does not exist")
	}
}

func TestSetUserPasswordReplacesTheHash(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserPassword(ctx, "id-1", "new"); err != nil {
		t.Fatal(err)
	}
	u, _, _ := db.UserByID(ctx, "id-1")
	if u.PasswordHash != "new" {
		t.Errorf("hash = %q, want %q", u.PasswordHash, "new")
	}
}

// The emptiness test has to live inside the INSERT. A read followed by a write
// leaves a window in which two callers both see an empty table, and at first
// boot that window is two browsers both being told they founded the console.
// Racing the call directly -- rather than through the handler, where bcrypt
// staggers the arrivals far enough apart to hide the window -- is what makes a
// check-then-insert implementation fail here.
func TestConcurrentClaimsCreateOneUser(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	const racers = 16
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		mu    sync.Mutex
		won   int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			name := "racer" + strconv.Itoa(i)
			ok, err := db.ClaimFirstUser(ctx, name, name, "hash")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				won++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if won != 1 {
		t.Errorf("winners = %d, want exactly 1", won)
	}
	n, err := db.UserCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

func TestCreateUserRefusesADuplicateNameRegardlessOfCase(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "Alice", "hash"); err != nil {
		t.Fatal(err)
	}
	err := db.CreateUser(ctx, "u2", "ALICE", "hash", RoleMember)
	if err == nil {
		t.Fatal("a second account took a name differing only by case")
	}
}

func TestUsersListsEveryAccountOldestFirst(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(ctx, "u2", "bob", "hash", RoleMember); err != nil {
		t.Fatal(err)
	}
	list, err := db.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].ID != "u1" {
		t.Errorf("first = %q, want the founding account", list[0].ID)
	}
	for _, u := range list {
		if u.PasswordHash != "" {
			t.Error("Users returned a password hash; a listing has no business carrying one")
		}
	}
}

func TestDeleteUserReportsWhetherARowWent(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	gone, err := db.DeleteUser(ctx, "u1")
	if err != nil || !gone {
		t.Fatalf("DeleteUser: %v gone=%v", err, gone)
	}
	gone, err = db.DeleteUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if gone {
		t.Error("deleting an absent account reported a removal")
	}
}

func TestAdminCountSeesOnlyAdmins(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(ctx, "u2", "bob", "hash", RoleMember); err != nil {
		t.Fatal(err)
	}
	n, err := db.AdminCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("AdminCount = %d, want 1", n)
	}
}
