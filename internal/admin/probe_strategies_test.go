package admin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

type probeReply struct {
	OK         bool   `json:"ok"`
	Probe      string `json:"probe"`
	ModelCount int    `json:"model_count"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error"`
	Rejected   bool   `json:"rejected"`
	AuthStyle  string `json:"auth_style"`
}

func probeProvider(t *testing.T, s *Server, cookie *http.Cookie, token, id string) probeReply {
	t.Helper()
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	var out probeReply
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fakeAWS serves the two control-plane listings and records the Authorization
// header it saw.
type fakeAWS struct {
	mu      sync.Mutex
	status  int
	errType string
	errBody string
	authz   string
}

func newFakeAWS(t *testing.T) (*fakeAWS, *httptest.Server) {
	t.Helper()
	f := &fakeAWS{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authz = r.Header.Get("Authorization")
		status, errType, errBody := f.status, f.errType, f.errBody
		f.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			if errType != "" {
				w.Header().Set("X-Amzn-Errortype", errType)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(errBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/foundation-models":
			_, _ = w.Write([]byte(`{"modelSummaries":[{"modelId":"amazon.titan-v1",
			  "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"ACTIVE"}}]}`))
		case "/inference-profiles":
			_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

// strategyServer builds an admin server with a live auth manager, so the probe
// exercises the real strategy path rather than a stub.
func strategyServer(t *testing.T, presets catalog.Presets, client *http.Client) (
	*Server, *http.Cookie, string, *store.DB) {

	t.Helper()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if presets == nil {
		presets = catalog.Embedded()
	}
	s, err := New(Deps{
		DB:     db,
		Config: configStoreFor(t, nil), Key: key, Presets: presets,
		Src:     provider.NewSQLSource(db, key),
		Breaker: health.New(3, time.Minute),
		Auth: auth.NewManager(auth.Deps{
			HTTP:  client,
			OAuth: testPresetOAuth{presets},
		}),
		HTTP: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, s)
	return s, cookie, token, db
}

func bedrockProvider(t *testing.T, s *Server, cookie *http.Cookie, token, baseURL string) string {
	t.Helper()
	body := fmt.Sprintf(
		`{"id":"bed","name":"bed","kind":"bedrock","base_url":%q,"auth_style":"sigv4","region":"us-east-1"}`,
		baseURL)
	if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	secret := `{\"access_key_id\":\"AKIDEXAMPLE\",\"secret_access_key\":\"CANARY-AWS-SECRET\"}`
	if w := do(t, s, cookie, token, "POST", "/api/providers/bed/keys",
		`{"label":"primary","secret":"`+secret+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	return "bed"
}

func TestSigV4ProbeReportsTheModelCount(t *testing.T) {
	// The same signal an openaicompat probe gives: a number the operator can
	// sanity-check against what they expect the account to have.
	aws, srv := newFakeAWS(t)
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.ModelCount == 0 {
		t.Error("model_count = 0")
	}
	if got.Probe != "listing" {
		t.Errorf("probe = %q", got.Probe)
	}
	aws.mu.Lock()
	authz := aws.authz
	aws.mu.Unlock()
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		t.Errorf("the listing was not signed: %q", authz)
	}
}

func TestSigV4ProbeNamesAPermissionFailure(t *testing.T) {
	// "It doesn't work" is not actionable. A 403 means the key is valid and
	// the policy is not, which is a different fix from a wrong region.
	aws, srv := newFakeAWS(t)
	aws.status = http.StatusForbidden
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a 403 must not report success")
	}
	if got.Probe != "permission" {
		t.Errorf("probe = %q, want permission", got.Probe)
	}
}

func TestSigV4ProbeMarksARefusedSignatureRejected(t *testing.T) {
	aws, srv := newFakeAWS(t)
	aws.status = http.StatusUnauthorized
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK || got.Probe != "signature" {
		t.Fatalf("ok = %v, probe = %q, want a signature failure", got.OK, got.Probe)
	}
	if !got.Rejected {
		t.Error("a refused signature is a rejected credential")
	}
}

func TestSigV4ProbeMarksAnUnknownKeyRejected(t *testing.T) {
	// Bedrock documents UnrecognizedClientException as a 403, and a wrong
	// secret arrives as InvalidSignatureException on a 403 too. The status
	// alone reads as a policy problem; the error type says the key is refused.
	for _, errType := range []string{
		"UnrecognizedClientException:http://internal.amazon.com/coral/com.amazon.coral.service/",
		"InvalidSignatureException",
	} {
		t.Run(errType, func(t *testing.T) {
			aws, srv := newFakeAWS(t)
			aws.status, aws.errType = http.StatusForbidden, errType
			aws.errBody = `{"message":"The security token included in the request is invalid."}`
			s, cookie, token, _ := strategyServer(t, nil, srv.Client())
			id := bedrockProvider(t, s, cookie, token, srv.URL)

			got := probeProvider(t, s, cookie, token, id)
			if got.OK || got.Probe != "signature" {
				t.Fatalf("ok = %v, probe = %q, want a signature failure: %s", got.OK, got.Probe, got.Error)
			}
			if !got.Rejected {
				t.Errorf("an unrecognised key is a rejected credential: %s", got.Error)
			}
		})
	}
}

func TestSigV4ProbeKeepsAKeyOnAccessDenied(t *testing.T) {
	aws, srv := newFakeAWS(t)
	aws.status, aws.errType = http.StatusForbidden, "AccessDeniedException"
	aws.errBody = `{"message":"User is not authorized to perform bedrock:ListFoundationModels"}`
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.Probe != "permission" || got.Rejected {
		t.Errorf("probe = %q, rejected = %v; a missing permission keeps the key", got.Probe, got.Rejected)
	}
}

func TestSigV4ProbeKeepsAKeyOnClockSkew(t *testing.T) {
	// AWS reports a skewed clock as InvalidSignatureException too. The host's
	// clock is wrong, not the key.
	aws, srv := newFakeAWS(t)
	aws.status, aws.errType = http.StatusForbidden, "InvalidSignatureException"
	aws.errBody = `{"message":"Signature expired: 20260101T000000Z is now earlier than 20260915T000000Z (20260914T235500Z - 5 min.)"}`
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a refused signature must not report success")
	}
	if got.Rejected {
		t.Errorf("a skewed clock is not a rejected credential: %s", got.Error)
	}
}

func TestSigV4ProbeKeepsAKeyOnAClockAhead(t *testing.T) {
	// A host clock ahead of AWS is the mirror of an expired signature: the
	// same error type, and the same good key.
	aws, srv := newFakeAWS(t)
	aws.status, aws.errType = http.StatusForbidden, "InvalidSignatureException"
	aws.errBody = `{"message":"Signature not yet current: 20260915T001000Z is still later than 20260915T000500Z (20260915T000000Z + 5 min.)"}`
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a refused signature must not report success")
	}
	if got.Rejected {
		t.Errorf("a clock ahead is not a rejected credential: %s", got.Error)
	}
}

func TestSigV4ProbeKeepsAKeyScopedToAnotherRegion(t *testing.T) {
	// A signature scoped to one region and sent to another region's endpoint
	// is the provider's region or base URL being wrong, not the key. The
	// wordings are AWS's SigV4 troubleshooting templates.
	for _, message := range []string{
		"Credential should be scoped to a valid region.",
		"Credential should be scoped to a valid Region, not 'us-east-2'.",
		"Credential should be scoped to correct service: 'bedrock'.",
	} {
		t.Run(message, func(t *testing.T) {
			aws, srv := newFakeAWS(t)
			aws.status, aws.errType = http.StatusForbidden, "InvalidSignatureException"
			aws.errBody = `{"message":"` + message + `"}`
			s, cookie, token, _ := strategyServer(t, nil, srv.Client())
			id := bedrockProvider(t, s, cookie, token, srv.URL)

			got := probeProvider(t, s, cookie, token, id)
			if got.OK {
				t.Fatal("a refused signature must not report success")
			}
			if got.Rejected {
				t.Errorf("a scope mismatch is not a rejected credential: %s", got.Error)
			}
			if got.Probe != "region" {
				t.Errorf("probe = %q, want region", got.Probe)
			}
		})
	}
}

func TestSigV4ProbeDoesNotRejectFromIncidentalText(t *testing.T) {
	// An error type the probe does not know falls back on what AWS said, and
	// a request id or a message can carry "401" or a signature type name
	// without the key being refused.
	cases := []struct {
		name, errType, body string
		status              int
	}{
		{"throttle with a request id", "ThrottlingException",
			`{"message":"Rate exceeded for request 9f401c2e"}`, http.StatusTooManyRequests},
		{"outage naming a signature", "",
			`{"message":"InternalFailure while validating InvalidSignature cache"}`, http.StatusInternalServerError},
		{"unavailable", "ServiceUnavailableException",
			`{"message":"Service unavailable: upstream returned 401"}`, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aws, srv := newFakeAWS(t)
			aws.status, aws.errType, aws.errBody = tc.status, tc.errType, tc.body
			s, cookie, token, _ := strategyServer(t, nil, srv.Client())
			id := bedrockProvider(t, s, cookie, token, srv.URL)

			got := probeProvider(t, s, cookie, token, id)
			if got.OK {
				t.Fatal("a failed listing must not report success")
			}
			if got.Rejected {
				t.Errorf("rejected from free text: %s", got.Error)
			}
			if got.Probe != "reachability" {
				t.Errorf("probe = %q, want reachability: %s", got.Probe, got.Error)
			}
		})
	}
}

func TestSigV4ProbeRefusesWithoutARegion(t *testing.T) {
	// Signing for the wrong region is a 403 that reads as a bad key.
	_, srv := newFakeAWS(t)
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"bed2","kind":"bedrock","base_url":"`+srv.URL+`","auth_style":"sigv4"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/bed2/keys",
		`{"label":"k","secret":"{\"access_key_id\":\"A\",\"secret_access_key\":\"B\"}"}`); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	got := probeProvider(t, s, cookie, token, "bed2")
	if got.OK {
		t.Fatal("a provider with no region must not probe OK")
	}
	if !strings.Contains(got.Error, "region") {
		t.Errorf("the error should name the cause: %q", got.Error)
	}
}

// vertexProbeServer builds a server whose every outbound request, the token
// exchange and the generation alike, lands on one fake answering the
// generation with status.
func vertexProbeServer(t *testing.T, status int) (*Server, *http.Cookie, string) {
	t.Helper()
	return vertexProbeServerWithToken(t, http.StatusOK,
		`{"access_token":"at","token_type":"Bearer","expires_in":3600}`, status)
}

// vertexProbeServerWithToken is vertexProbeServer with the token exchange
// answering tokenStatus and tokenBody.
func vertexProbeServerWithToken(t *testing.T, tokenStatus int, tokenBody string, status int) (
	*Server, *http.Cookie, string) {

	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tokenStatus)
			_, _ = w.Write([]byte(tokenBody))
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(fake.Close)
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host, r.Host = target.Scheme, target.Host, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}

	s, cookie, token, _ := strategyServer(t, nil, client)
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "vx", ModelID: "gemini-2.0-flash", Publisher: vertex.PublisherGoogle, State: catalog.StateLive,
	}}, []string{"vx"}))
	s.deps.Catalog = cat

	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"vx","preset":"vertex","project":"proj","location":"us-central1"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "proj", "client_email": "sa@proj.iam.gserviceaccount.com",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"private_key_id": gcpCanaryKeyID,
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	body, _ := json.Marshal(map[string]string{"label": "sa", "secret": string(doc)})
	if w := do(t, s, cookie, token, "POST", "/api/providers/vx/keys", string(body)); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	return s, cookie, token
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGCPProbeDoesNotRejectAKeyOnPermissionDenied(t *testing.T) {
	// On Google Cloud a 403 is an API not enabled, a missing IAM role or a
	// model not enabled in the project. The key authenticated; the console
	// deletes a rejected key, and this one is good.
	s, cookie, token := vertexProbeServer(t, http.StatusForbidden)

	got := probeProvider(t, s, cookie, token, "vx")
	if got.OK {
		t.Fatal("a 403 must not report success")
	}
	if got.Probe != "permission" {
		t.Errorf("probe = %q, want permission", got.Probe)
	}
	if got.Rejected {
		t.Errorf("a permission failure is not a rejected credential: %s", got.Error)
	}
}

func TestGCPProbeMarksAnUnauthenticatedKeyRejected(t *testing.T) {
	s, cookie, token := vertexProbeServer(t, http.StatusUnauthorized)

	got := probeProvider(t, s, cookie, token, "vx")
	if got.OK {
		t.Fatal("a 401 must not report success")
	}
	if !got.Rejected {
		t.Errorf("a refused credential is rejected: %s", got.Error)
	}
}

func TestGCPProbeMarksARefusedKeySignatureRejected(t *testing.T) {
	// Google documents "Invalid JWT Signature." as a key not associated with
	// the service account, or one deleted, disabled or expired.
	s, cookie, token := vertexProbeServerWithToken(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`, http.StatusOK)

	got := probeProvider(t, s, cookie, token, "vx")
	if got.OK || got.Probe != "expiry" {
		t.Fatalf("ok = %v, probe = %q; want a failed token exchange: %s", got.OK, got.Probe, got.Error)
	}
	if !got.Rejected {
		t.Errorf("a refused key signature is a rejected credential: %s", got.Error)
	}
}

func TestGCPProbeKeepsAKeyOnClockSkew(t *testing.T) {
	// The same invalid_grant names a host clock outside Google's window. The
	// key is fine.
	s, cookie, token := vertexProbeServerWithToken(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"Invalid JWT: Token must be a short-lived token (60 minutes) and in a reasonable timeframe. Check your iat and exp values in the JWT claim."}`,
		http.StatusOK)

	got := probeProvider(t, s, cookie, token, "vx")
	if got.OK {
		t.Fatal("a refused exchange must not report success")
	}
	if got.Rejected {
		t.Errorf("a skewed clock is not a rejected credential: %s", got.Error)
	}
}

func TestARejectedProbeNamesTheAuthStyle(t *testing.T) {
	// The console deletes a key the probe rejects, unless the secret is one
	// the operator cannot download again. The probe resolves the style the
	// provider actually authenticates with, so the answer carries it.
	t.Run("sigv4", func(t *testing.T) {
		aws, srv := newFakeAWS(t)
		aws.status = http.StatusUnauthorized
		s, cookie, token, _ := strategyServer(t, nil, srv.Client())
		id := bedrockProvider(t, s, cookie, token, srv.URL)

		got := probeProvider(t, s, cookie, token, id)
		if !got.Rejected || got.AuthStyle != auth.StyleSigV4 {
			t.Errorf("rejected = %v, auth_style = %q; want a rejected sigv4 key", got.Rejected, got.AuthStyle)
		}
	})
	t.Run("gcp-sa from the preset", func(t *testing.T) {
		s, cookie, token := vertexProbeServer(t, http.StatusUnauthorized)

		got := probeProvider(t, s, cookie, token, "vx")
		if !got.Rejected || got.AuthStyle != auth.StyleGCPSA {
			t.Errorf("rejected = %v, auth_style = %q; want a rejected gcp-sa key", got.Rejected, got.AuthStyle)
		}
	})
}

const gcpCanaryKeyID = "0f1e2d3c4b5a69788796a5b4c3d2e1f0canary"

func TestAProbeDoesNotShowAPartOfAStructuredSecret(t *testing.T) {
	// A sigv4 or service-account secret is stored as one JSON document, and
	// an upstream that echoes a credential echoes one field of it. Redacting
	// the document as a whole matches nothing.
	t.Run("sigv4", func(t *testing.T) {
		const secretKey, session = "CANARY-SECRET-ACCESS-KEY", "CANARY-SESSION-TOKEN-value"
		aws, srv := newFakeAWS(t)
		aws.status, aws.errType = http.StatusForbidden, "InvalidSignatureException"
		aws.errBody = `{"message":"The request signature we calculated does not match. Secret: ` +
			secretKey + `, token: ` + session + `"}`
		s, cookie, token, _ := strategyServer(t, nil, srv.Client())
		if w := do(t, s, cookie, token, "POST", "/api/providers",
			`{"id":"bed","name":"bed","kind":"bedrock","base_url":"`+srv.URL+`","auth_style":"sigv4","region":"us-east-1"}`); w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		doc, _ := json.Marshal(map[string]string{
			"access_key_id": "AKIDEXAMPLE", "secret_access_key": secretKey, "session_token": session,
		})
		body, _ := json.Marshal(map[string]string{"label": "primary", "secret": string(doc)})
		if w := do(t, s, cookie, token, "POST", "/api/providers/bed/keys", string(body)); w.Code != http.StatusCreated {
			t.Fatalf("key: %d %s", w.Code, w.Body.String())
		}

		got := probeProvider(t, s, cookie, token, "bed")
		if got.OK || got.Error == "" {
			t.Fatalf("ok = %v, error = %q; want the refusal reported", got.OK, got.Error)
		}
		for _, part := range []string{secretKey, session} {
			if strings.Contains(got.Error, part) {
				t.Errorf("error %q carries %q", got.Error, part)
			}
		}
	})

	t.Run("gcp-sa", func(t *testing.T) {
		s, cookie, token := vertexProbeServerWithToken(t, http.StatusBadRequest,
			`{"error":"invalid_grant","error_description":"No key `+gcpCanaryKeyID+` for this account."}`,
			http.StatusOK)

		got := probeProvider(t, s, cookie, token, "vx")
		if got.OK || got.Error == "" {
			t.Fatalf("ok = %v, error = %q; want the refusal reported", got.OK, got.Error)
		}
		if strings.Contains(got.Error, gcpCanaryKeyID) {
			t.Errorf("error %q carries the private key id", got.Error)
		}
	})
}

func TestProbeSecretsNamesEachSecretField(t *testing.T) {
	const pemKey = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----\n"
	doc, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "sa@proj.iam.gserviceaccount.com",
		"private_key": pemKey, "private_key_id": gcpCanaryKeyID,
	})
	got := probeSecrets(auth.StyleGCPSA, string(doc))
	for _, want := range []string{string(doc), pemKey, gcpCanaryKeyID} {
		if !slices.Contains(got, want) {
			t.Errorf("probeSecrets(gcp-sa) = %q, missing %q", got, want)
		}
	}
	if slices.Contains(got, "sa@proj.iam.gserviceaccount.com") {
		t.Error("the client email is an identifier, not a secret")
	}

	if got := probeSecrets(auth.StyleBearer, "sk-plain-key"); !slices.Equal(got, []string{"sk-plain-key"}) {
		t.Errorf("probeSecrets(bearer) = %q, want the key alone", got)
	}
}

// oauthProviderListingAt creates the anthropic-oauth provider pointed at a
// listing server, so its probe never leaves the test.
func oauthProviderListingAt(t *testing.T, s *Server, cookie *http.Cookie, token string,
	listing http.HandlerFunc) string {

	t.Helper()
	srv := httptest.NewServer(listing)
	t.Cleanup(srv.Close)
	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"sub","preset":"anthropic-oauth","base_url":"`+srv.URL+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create oauth provider: %d %s", w.Code, w.Body.String())
	}
	return "sub"
}

func TestOAuthProbeRefreshesAndLists(t *testing.T) {
	fake, srv := newFakeAuthServer(t)
	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProviderListingAt(t, s, cookie, token,
		headerGatedUpstream("Authorization", "Bearer the-access-token-1", "m1", "m2"))
	seedOAuthCredential(t, db, s, id, -time.Minute)

	got := probeProvider(t, s, cookie, token, id)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.Probe != "listing" || got.ModelCount != 2 {
		t.Errorf("probe = %q, model_count = %d; want the listing the refreshed token reached",
			got.Probe, got.ModelCount)
	}
	if fake.refreshCount() == 0 {
		t.Error("the probe did not refresh")
	}
}

func TestOAuthProbeReachesTheProviderWithAnUnexpiredToken(t *testing.T) {
	// A cached token needs no refresh, so only a request to the provider can
	// tell a working account from a revoked one.
	fake, srv := newFakeAuthServer(t)
	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	var listed atomic.Int32
	id := oauthProviderListingAt(t, s, cookie, token, func(w http.ResponseWriter, r *http.Request) {
		listed.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	seedOAuthCredential(t, db, s, id, time.Hour)

	got := probeProvider(t, s, cookie, token, id)
	if listed.Load() == 0 {
		t.Fatal("the probe never contacted the provider")
	}
	if got.OK {
		t.Fatal("a token the provider refuses must not probe OK")
	}
	if !got.Rejected {
		t.Error("a refused token is a rejected credential")
	}
	if fake.refreshCount() != 0 {
		t.Error("an unexpired token was refreshed")
	}
}

func TestOAuthProbeReportsAnExpiredGrant(t *testing.T) {
	fake, srv := newFakeAuthServer(t)
	fake.mu.Lock()
	fake.status, fake.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	fake.mu.Unlock()

	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProviderListingAt(t, s, cookie, token, listingUpstream("m1"))
	seedOAuthCredential(t, db, s, id, -time.Minute)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a refused refresh must not report success")
	}
	if got.Probe != "refresh" {
		t.Errorf("probe = %q; a refused refresh is not a listing failure", got.Probe)
	}
	if !strings.Contains(strings.ToLower(got.Error), "reconnect") {
		t.Errorf("the operator must be told to reconnect: %q", got.Error)
	}
	if !got.Rejected {
		t.Error("a refused refresh is a rejected credential")
	}
}

func TestNoProbeResponseCarriesCredentialMaterial(t *testing.T) {
	_, srv := newFakeAuthServer(t)
	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProviderListingAt(t, s, cookie, token, listingUpstream("m1"))
	seedOAuthCredential(t, db, s, id, -time.Minute)

	raw := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "").Body.String()
	for _, secret := range []string{"rt-canary", "at-canary", "the-refresh-token", "the-access-token"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the probe response carries %q:\n%s", secret, raw)
		}
	}
}

// seedOAuthCredential writes a token document directly, which is what a
// completed connect flow leaves behind.
func seedOAuthCredential(t *testing.T, db *store.DB, s *Server, providerID string, expiresIn time.Duration) string {
	t.Helper()
	tok := auth.Token{
		AccessToken: "at-canary", RefreshToken: "rt-canary",
		ExpiresAt: time.Now().Add(expiresIn),
	}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(context.Background(), s.deps.Key, store.Credential{
		ProviderID: providerID, Label: "personal", Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// testPresetOAuth resolves a preset id to its OAuth endpoints, mirroring what
// the server wires in production. Declared here rather than imported because
// internal/server imports internal/admin, not the other way round.
type testPresetOAuth struct{ presets catalog.Presets }

func (p testPresetOAuth) OAuthFor(preset string) (auth.OAuthConfig, bool) {
	entry, ok := p.presets[preset]
	if !ok || entry.OAuth == nil {
		return auth.OAuthConfig{}, false
	}
	return auth.OAuthConfig{
		AuthorizeURL: entry.OAuth.AuthorizeURL, TokenURL: entry.OAuth.TokenURL,
		ClientID: entry.OAuth.ClientID, Scopes: entry.OAuth.Scopes,
	}, true
}
