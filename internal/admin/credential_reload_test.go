package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/auth"
)

// liveCredentials reports what the running router would serve for a provider.
func liveCredentials(t *testing.T, s *Server, providerID string) []string {
	t.Helper()
	ps, err := s.deps.Src.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID != providerID {
			continue
		}
		out := make([]string, 0, len(p.Credentials))
		for _, c := range p.Credentials {
			out = append(out, c.Secret)
		}
		return out
	}
	return nil
}

// Disabling a credential is the emergency revocation control. The provider
// source caches decrypted credentials until it is reloaded, so a write that
// lands in SQLite and stops there keeps the revoked key serving traffic.
func TestDisablingACredentialStopsItServing(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p", "http://p.invalid")

	if got := liveCredentials(t, s, "p"); len(got) != 1 {
		t.Fatalf("credentials before the patch = %v, want one", got)
	}
	if w := do(t, s, cookie, token, "PATCH", "/api/providers/p/keys/"+keyID,
		`{"enabled":false}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if got := liveCredentials(t, s, "p"); len(got) != 0 {
		t.Errorf("credentials after disabling = %v, want none: the disable "+
			"reached the database but not the running router", got)
	}
}

// A reload keeps the previous provider set when it cannot read every row. A
// revocation that committed but did not reach the router must not answer as
// though the credential had stopped serving: nothing else would ever tell the
// operator the leaked key is still in use.
func TestARevocationTheRouterDidNotLoadIsNotReportedAsDone(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p", "http://p.invalid")
	otherID := seedProviderWithKey(t, s, cookie, token, "q", "http://q.invalid")
	// Another provider's row that no longer authenticates, so the next reload
	// fails as a whole.
	if _, err := db.Sync.Exec(`UPDATE provider_keys SET ciphertext = x'00' WHERE id = ?`,
		otherID); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, cookie, token, "PATCH", "/api/providers/p/keys/"+keyID, `{"enabled":false}`)
	if w.Code < 500 {
		t.Fatalf("patch = %d %s; the router still serves the revoked key, "+
			"so the response must not report success", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "saved") {
		t.Errorf("body = %s; it must say the change itself was saved", w.Body.String())
	}
	creds, err := db.CredentialSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(creds["p"]) != 1 || creds["p"][0].Enabled {
		t.Errorf("credential = %+v, want it disabled in the database", creds["p"])
	}
}

// Replacing a secret is the other half of the same control: rotating a leaked
// key has to stop the leaked one being used.
func TestReplacingASecretReachesTheRouter(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p", "http://p.invalid")

	if w := do(t, s, cookie, token, "PATCH", "/api/providers/p/keys/"+keyID,
		`{"secret":"sk-rotated-9876543210"}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	got := liveCredentials(t, s, "p")
	if len(got) != 1 || got[0] != "sk-rotated-9876543210" {
		t.Errorf("credentials after replacing = %v, want the new secret", got)
	}
}

// forgetRecorder is an AuthResolver that also records invalidations.
type forgetRecorder struct{ forgot []string }

func (f *forgetRecorder) For(context.Context, auth.Target, auth.Credential) (auth.Authorizer, error) {
	return nil, nil
}
func (f *forgetRecorder) Forget(credID string) { f.forgot = append(f.forgot, credID) }

// An OAuth account is cached under the credential id, so replacing the secret
// without telling the auth manager leaves the old token in play.
func TestPatchingACredentialInvalidatesItsCachedAuthorizer(t *testing.T) {
	s, _ := testServerFull(t)
	rec := &forgetRecorder{}
	s.deps.Auth = rec
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p", "http://p.invalid")

	if w := do(t, s, cookie, token, "PATCH", "/api/providers/p/keys/"+keyID,
		`{"secret":"sk-rotated-9876543210"}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if len(rec.forgot) != 1 || rec.forgot[0] != keyID {
		t.Errorf("forgotten credentials = %v, want [%s]", rec.forgot, keyID)
	}
}
