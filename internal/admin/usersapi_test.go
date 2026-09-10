package admin

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// newServerWithMemberSession composes the two fixtures: a console claimed by
// the founding admin, then a second account with role 'member' whose cookie is
// the one returned. The admin's own cookie is dropped, so a test using this can
// only speak as the member.
func newServerWithMemberSession(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, _ := newServerWithSession(t)
	_, cookie := seedSecondAccountWithSession(t, s)
	return s, cookie
}

func TestAMemberCannotListAccounts(t *testing.T) {
	s, cookie := newServerWithMemberSession(t)
	rec := getJSONAs(t, s, "/api/users", cookie)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}

func TestAMemberCannotCreateAnAccount(t *testing.T) {
	s, cookie := newServerWithMemberSession(t)
	rec := postJSONAs(t, s, "/api/users",
		`{"username":"mallory","password":"correct-horse-battery","role":"admin"}`, cookie)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 2 {
		t.Error("a member created an account")
	}
}

func TestAnAdminCreatesAMember(t *testing.T) {
	s, _, cookie := newServerWithSession(t) // founding admin
	rec := postJSONAs(t, s, "/api/users",
		`{"username":"bob","password":"correct-horse-battery","role":"member"}`, cookie)
	if rec.Code != 201 {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body)
	}
	u, ok, _ := s.deps.DB.UserByUsername(t.Context(), "bob")
	if !ok || u.Role != "member" {
		t.Errorf("account not created as a member: ok=%v role=%q", ok, u.Role)
	}
}

// TestAnUnknownRoleIsRefused guards the only check in the design against a
// misspelled role: the column has no CHECK constraint and the store validates
// nothing, so a stored "admn" would be invisible to AdminCount and the last
// administrator could then delete themselves.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	s, _, cookie := newServerWithSession(t)
	rec := postJSONAs(t, s, "/api/users",
		`{"username":"bob","password":"correct-horse-battery","role":"admn"}`, cookie)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 1 {
		t.Error("an account was created with an unknown role")
	}
}

func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	// There is no recovery path. A console whose last administrator deleted
	// themselves cannot be managed again by any route the code provides.
	s, uid, cookie := newServerWithSession(t)
	rec := deleteAs(t, s, "/api/users/"+uid, cookie)
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	if n, _ := s.deps.DB.AdminCount(t.Context()); n != 1 {
		t.Error("the last administrator was removed")
	}
}

func TestRemovingAnAccountSignsItOut(t *testing.T) {
	s, _, adminCookie := newServerWithSession(t)
	memberID, memberCookie := seedSecondAccountWithSession(t, s)

	rec := deleteAs(t, s, "/api/users/"+memberID, adminCookie)
	if rec.Code != 204 {
		t.Fatalf("code = %d, want 204: %s", rec.Code, rec.Body)
	}
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), memberCookie, sessionTTL); ok {
		t.Error("a removed account still has a live session")
	}
}

// TestListingNeverCarriesAHash asserts the exact key set rather than only the
// absence of two substrings: store.Users never selects a hash, so a handler
// that marshalled the store row straight out would still pass a substring
// check while shipping an empty PasswordHash field to the browser.
func TestListingNeverCarriesAHash(t *testing.T) {
	s, _, cookie := newServerWithSession(t)
	seedSecondAccountWithSession(t, s)
	rec := getJSONAs(t, s, "/api/users", cookie)
	if strings.Contains(rec.Body.String(), "password_hash") || strings.Contains(rec.Body.String(), "$2a$") {
		t.Errorf("the listing carried a password hash: %s", rec.Body)
	}
	var body struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode listing: %v: %s", err, rec.Body)
	}
	if len(body.Users) != 2 {
		t.Fatalf("users = %d, want 2: %s", len(body.Users), rec.Body)
	}
	for _, u := range body.Users {
		keys := []string{}
		for k := range u {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if got := strings.Join(keys, ","); got != "created_at,id,role,username" {
			t.Errorf("listing keys = %q, want %q", got, "created_at,id,role,username")
		}
	}
}
