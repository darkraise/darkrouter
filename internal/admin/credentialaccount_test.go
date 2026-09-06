package admin

import (
	"net/http"
	"strings"
	"testing"
)

// Cloudflare Workers AI and Snowflake Cortex serve each account under its own
// endpoint. A credential without the account cannot address either, and the
// failure would otherwise arrive as a 404 from a URL nobody can explain.
func TestACredentialForATemplatedEndpointNeedsAnAccount(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, csrf := login(t, s)

	if w := do(t, s, cookie, csrf, "POST", "/api/providers",
		`{"id":"cf","name":"CF","kind":"openaicompat","preset":"cloudflare-ai",
		  "base_url":"https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1"}`); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create provider = %d %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, csrf, "POST", "/api/providers/cf/keys", `{"label":"a","secret":"k"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a credential with no account = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "account") {
		t.Errorf("the refusal does not say what is missing: %s", w.Body.String())
	}
}

func TestACredentialCarriesItsAccountThrough(t *testing.T) {
	s, db := testServerFull(t)
	cookie, csrf := login(t, s)
	if w := do(t, s, cookie, csrf, "POST", "/api/providers",
		`{"id":"cf","name":"CF","kind":"openaicompat","preset":"cloudflare-ai",
		  "base_url":"https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1"}`); w.Code >= 300 {
		t.Fatalf("create provider = %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, csrf, "POST", "/api/providers/cf/keys",
		`{"label":"a","secret":"k","account_id":"abc123"}`); w.Code >= 300 {
		t.Fatalf("add credential = %d %s", w.Code, w.Body.String())
	}
	creds, err := db.Credentials(t.Context(), s.deps.Key, "cf")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].AccountID != "abc123" {
		t.Fatalf("stored credential = %+v", creds)
	}
}

// A provider whose endpoint carries no account must not start demanding one.
func TestAnOrdinaryProviderStillTakesABareKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, csrf := login(t, s)
	if w := do(t, s, cookie, csrf, "POST", "/api/providers",
		`{"id":"g","name":"G","kind":"openaicompat","base_url":"https://api.groq.com/openai/v1"}`); w.Code >= 300 {
		t.Fatalf("create provider = %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, csrf, "POST", "/api/providers/g/keys", `{"label":"a","secret":"k"}`); w.Code >= 300 {
		t.Fatalf("add credential = %d %s", w.Code, w.Body.String())
	}
}
