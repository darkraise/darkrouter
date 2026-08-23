package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

// canaries are values that must never appear in any response or log line.
// Each is planted in a credential of a different kind, because the three fail
// in different ways: a bare key is short and looks like an id, a service-account
// document is multi-line JSON whose last field may be the key itself, and a
// refresh token is a long opaque string indistinguishable from a ULID.
var canaries = map[string]string{
	"static key":          "sk-CANARY-STATIC-0001",
	"aws secret":          "CANARY-AWS-SECRET-0002",
	"service-account key": "CANARY-PRIVATE-KEY-0003",
	"oauth refresh token": "CANARY-REFRESH-0004",
	"oauth access token":  "CANARY-ACCESS-0005",
}

// seedEveryCredentialKind creates one provider per strategy, each carrying its
// canary, and returns the provider ids.
func seedEveryCredentialKind(t *testing.T, s *Server, cookie *http.Cookie, token string) []string {
	t.Helper()

	create := func(body string) {
		if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	addKey := func(id, secret string) {
		encoded, err := json.Marshal(secret)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"label":"primary","secret":` + string(encoded) + `}`
		if w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/keys", body); w.Code != http.StatusCreated {
			t.Fatalf("key for %s: %d %s", id, w.Code, w.Body.String())
		}
	}

	create(`{"id":"static-p","kind":"openaicompat","base_url":"https://x/v1"}`)
	addKey("static-p", canaries["static key"])

	create(`{"id":"bed-p","kind":"bedrock","base_url":"https://bedrock.invalid","auth_style":"sigv4","region":"us-east-1"}`)
	addKey("bed-p", `{"access_key_id":"AKID","secret_access_key":"`+canaries["aws secret"]+`"}`)

	create(`{"id":"vx-p","kind":"vertex","base_url":"https://vertex.invalid","auth_style":"gcp-sa","project":"proj","location":"us-central1"}`)
	addKey("vx-p", `{"type":"service_account","project_id":"proj","client_email":"sa@x","private_key":"-----BEGIN PRIVATE KEY-----\n`+
		canaries["service-account key"]+`\n-----END PRIVATE KEY-----\n"}`)

	create(`{"id":"oauth-p","preset":"anthropic-oauth"}`)
	tok := auth.Token{
		AccessToken:  canaries["oauth access token"],
		RefreshToken: canaries["oauth refresh token"],
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.deps.DB.AddCredential(context.Background(), s.deps.Key, store.Credential{
		ProviderID: "oauth-p", Label: "personal", Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	return []string{"static-p", "bed-p", "vx-p", "oauth-p"}
}

func readEndpoints() []string {
	return []string{
		"/api/overview", "/api/providers", "/api/presets", "/api/models",
		"/api/requests", "/api/usage", "/api/config", "/api/auth/status",
	}
}

func assertNoCanary(t *testing.T, where, body string) {
	t.Helper()
	for name, canary := range canaries {
		if strings.Contains(body, canary) {
			t.Errorf("%s leaked the %s", where, name)
		}
		if strings.Contains(body, base64.StdEncoding.EncodeToString([]byte(canary))) {
			t.Errorf("%s leaked the %s, base64-encoded", where, name)
		}
	}
}

func TestNoCredentialMaterialInAnyResponse(t *testing.T) {
	s, cookie, token, _ := strategyServer(t, catalog.Embedded(), http.DefaultClient)
	seedEveryCredentialKind(t, s, cookie, token)

	for _, path := range readEndpoints() {
		assertNoCanary(t, path, do(t, s, cookie, token, "GET", path, "").Body.String())
	}
}

func TestNoCredentialMaterialInAProbeResponse(t *testing.T) {
	// The probe talks to the credential, so its error paths are the likeliest
	// place for one to be echoed back inside a provider's message.
	s, cookie, token, _ := strategyServer(t, catalog.Embedded(), http.DefaultClient)
	for _, id := range seedEveryCredentialKind(t, s, cookie, token) {
		w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "")
		assertNoCanary(t, "the probe on "+id, w.Body.String())
	}
}

func TestNoCredentialMaterialInALogLine(t *testing.T) {
	// Spec §8 names the access log alongside the API. There is none today, so
	// this is what stops one being added that logs a signed URL or a callback.
	s, cookie, token, _ := strategyServer(t, catalog.Embedded(), http.DefaultClient)
	ids := seedEveryCredentialKind(t, s, cookie, token)

	var logged strings.Builder
	prevOut := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prevOut)

	for _, path := range readEndpoints() {
		_ = do(t, s, cookie, token, "GET", path, "")
	}
	for _, id := range ids {
		_ = do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "")
	}
	assertNoCanary(t, "a log line", logged.String())
}

func TestAServiceAccountDocumentIsNeverPartiallyShown(t *testing.T) {
	// A masked helper written for a 40-character key shows the last four
	// characters. On a multi-line JSON document that is harmless; on one whose
	// last field happens to be the key it is not. The masked value must be
	// derived from the credential's identity, not from its content shape.
	s, cookie, token, _ := strategyServer(t, catalog.Embedded(), http.DefaultClient)
	seedEveryCredentialKind(t, s, cookie, token)

	raw := do(t, s, cookie, token, "GET", "/api/providers", "").Body.String()
	for _, fragment := range []string{"BEGIN PRIVATE KEY", "private_key", "-----", "access_key_id"} {
		if strings.Contains(raw, fragment) {
			t.Errorf("a credential document fragment %q is in the response", fragment)
		}
	}
}
