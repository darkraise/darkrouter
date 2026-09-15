package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/store"
)

// breakNextReload stores a credential on a provider of its own whose row no
// longer authenticates, so every later reload fails as a whole.
func breakNextReload(t *testing.T, s *Server, cookie *http.Cookie, token string) {
	t.Helper()
	otherID := seedProviderWithKey(t, s, cookie, token, "broken", "http://broken.invalid")
	if _, err := s.deps.DB.Sync.Exec(`UPDATE provider_keys SET ciphertext = x'00' WHERE id = ?`,
		otherID); err != nil {
		t.Fatal(err)
	}
}

type routingReply struct {
	ID             string `json:"id"`
	CredentialID   string `json:"credential_id"`
	Label          string `json:"label"`
	RoutingUpdated *bool  `json:"routing_updated"`
	Warning        string `json:"warning"`
}

func decodeRouting(t *testing.T, body []byte) routingReply {
	t.Helper()
	var out routingReply
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

// A reload failure can outlive any retry — one undecryptable row fails every
// reload — so a key that committed must not answer as a failure: the console
// would offer to send it again and store a second copy.
func TestACredentialTheRouterDidNotLoadIsReportedAsCreated(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	breakNextReload(t, s, cookie, token)
	if err := db.CreateProvider(context.Background(), store.ProviderRow{
		ID: "p", Kind: "openaicompat", BaseURL: "http://p.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	spy := withSpyTrigger(s)

	w := do(t, s, cookie, token, "POST", "/api/providers/p/keys", `{"label":"one","secret":"sk-one-123456"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s; the key was stored, so it was created", w.Code, w.Body.String())
	}
	got := decodeRouting(t, w.Body.Bytes())
	if got.ID == "" || got.Label != "one" {
		t.Errorf("reply = %+v, want the stored credential's id and label", got)
	}
	if got.RoutingUpdated == nil || *got.RoutingUpdated {
		t.Errorf("routing_updated = %v, want false", got.RoutingUpdated)
	}
	if !strings.Contains(got.Warning, "saved") {
		t.Errorf("warning = %q; it must say the change was saved but not loaded", got.Warning)
	}
	creds, err := db.CredentialSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(creds["p"]) != 1 {
		t.Errorf("credentials = %+v, want the one stored", creds["p"])
	}
	// The row exists whether or not the router loaded it, and a sweep reads
	// the database.
	if len(spy.swept) != 1 || spy.swept[0] != "p" {
		t.Errorf("swept %v, want one sweep of p", spy.swept)
	}
}

func TestAnAddedCredentialSaysRoutingWasUpdated(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p","kind":"openaicompat","base_url":"http://p.invalid"}`)
	w := do(t, s, cookie, token, "POST", "/api/providers/p/keys", `{"label":"one","secret":"sk-one-123456"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", w.Code, w.Body.String())
	}
	if got := decodeRouting(t, w.Body.Bytes()); got.RoutingUpdated == nil || !*got.RoutingUpdated || got.Warning != "" {
		t.Errorf("reply = %s, want routing_updated true and no warning", w.Body.String())
	}
}

// The console deletes a key the provider refused. A delete that committed and
// then failed to reload has to be recognisable as a delete, or the console
// reports a key it removed as kept.
func TestADeleteTheRouterDidNotLoadSaysItCommitted(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p", "http://p.invalid")
	breakNextReload(t, s, cookie, token)

	w := do(t, s, cookie, token, "DELETE", "/api/providers/p/keys/"+keyID, "")
	if w.Code < 500 {
		t.Fatalf("delete = %d %s; the router still serves the key", w.Code, w.Body.String())
	}
	if got := decodeRouting(t, w.Body.Bytes()); got.RoutingUpdated == nil || *got.RoutingUpdated {
		t.Errorf("reply = %s, want routing_updated false", w.Body.String())
	}
	creds, err := db.CredentialSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(creds["p"]) != 0 {
		t.Errorf("credentials = %+v, want the key deleted", creds["p"])
	}
}

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
