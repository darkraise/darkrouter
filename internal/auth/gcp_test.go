package auth

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
	"strings"
	"sync"
	"testing"
)

// testKey is generated once per test binary. A 2048-bit RSA keygen is about a
// tenth of a second, and this package would otherwise pay it in every case.
var testKey = sync.OnceValue(func() []byte {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
})

// serviceAccount writes a document pointing at tokenURL. token_uri is read by
// JWTConfigFromJSON, so the fake endpoint needs no hook in the code under test.
func serviceAccount(t *testing.T, tokenURL string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "proj",
		"private_key":  string(testKey()),
		"client_email": "sa@proj.iam.gserviceaccount.com",
		"token_uri":    tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type tokenFake struct {
	mu        sync.Mutex
	calls     int
	expiresIn int
	status    int
}

func (f *tokenFake) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		n, exp, status := f.calls, f.expiresIn, f.status
		f.mu.Unlock()

		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		if exp == 0 {
			exp = 3600
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":%d}`, n, exp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *tokenFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func gcpAuthorizer(t *testing.T, secret string) Authorizer {
	t.Helper()
	m := NewManager(Deps{})
	az, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj", Location: "us-central1"},
		Credential{ID: "k", Kind: "gcp_sa", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return az
}

func gcpRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", "https://aiplatform.googleapis.invalid/x", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGCPExchangesTheKeyForABearerToken(t *testing.T) {
	f := &tokenFake{}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	r := gcpRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestGCPReusesATokenInsideItsLifetime(t *testing.T) {
	// One exchange per hour, not one per request. Without this every Vertex
	// call pays a JWT signature and a round trip to Google.
	f := &tokenFake{}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	for i := 0; i < 5; i++ {
		if err := az(context.Background(), gcpRequest(t)); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != 1 {
		t.Errorf("exchanged %d times, want 1", f.count())
	}
}

func TestGCPRefreshesInsideTheDelta(t *testing.T) {
	// The token is still valid by the clock but a request starting now could
	// arrive after it expires. Spec §4.2's "no request fails on an expiry race".
	f := &tokenFake{expiresIn: 30}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	if err := az(context.Background(), gcpRequest(t)); err != nil {
		t.Fatal(err)
	}
	if err := az(context.Background(), gcpRequest(t)); err != nil {
		t.Fatal(err)
	}
	if f.count() != 2 {
		t.Errorf("exchanged %d times, want 2: a token 30s from expiry is inside the %v delta",
			f.count(), DefaultRefreshDelta)
	}
}

func TestGCPExchangeFailureIsReportedWithoutTheKey(t *testing.T) {
	// The upstream is fine; this key is not. And the error must not carry the
	// private key into a log line or an admin response.
	f := &tokenFake{status: http.StatusUnauthorized}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	err := az(context.Background(), gcpRequest(t))
	if err == nil {
		t.Fatal("a refused token exchange must be an error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the error should name the exchange, got %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatal("the error carries the private key")
	}
}

func TestGCPRefusesAMalformedDocument(t *testing.T) {
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj"},
		Credential{ID: "k", Kind: "gcp_sa", Secret: "not json"})
	if err == nil {
		t.Fatal("a malformed service-account document must be refused at resolution")
	}
}

func TestGCPNeedsAProject(t *testing.T) {
	// Project and location construct the endpoint, spec §4.2. Without a
	// project there is no URL to call, and a token would be useless.
	f := &tokenFake{}
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA},
		Credential{ID: "k", Kind: "gcp_sa", Secret: serviceAccount(t, f.serve(t).URL)})
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("a vertex provider with no project must be refused, got %v", err)
	}
}

func TestGCPSourcesAreCachedPerCredential(t *testing.T) {
	// Two resolutions of the same credential must share a token source, or the
	// cache above is defeated by the executor resolving once per attempt.
	f := &tokenFake{}
	secret := serviceAccount(t, f.serve(t).URL)
	m := NewManager(Deps{})
	tgt := Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj"}
	cred := Credential{ID: "k", Kind: "gcp_sa", Secret: secret}

	for i := 0; i < 3; i++ {
		az, err := m.For(context.Background(), tgt, cred)
		if err != nil {
			t.Fatal(err)
		}
		if err := az(context.Background(), gcpRequest(t)); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != 1 {
		t.Errorf("exchanged %d times across three resolutions, want 1", f.count())
	}
}

func TestReplacingAKeyInvalidatesTheCachedSource(t *testing.T) {
	// Same credential id, different content. Serving tokens minted from the
	// old key until restart would make a key rotation look like it did nothing.
	f1, f2 := &tokenFake{}, &tokenFake{}
	m := NewManager(Deps{})
	tgt := Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj"}

	az1, err := m.For(context.Background(), tgt,
		Credential{ID: "k", Kind: "gcp_sa", Secret: serviceAccount(t, f1.serve(t).URL)})
	if err != nil {
		t.Fatal(err)
	}
	if err := az1(context.Background(), gcpRequest(t)); err != nil {
		t.Fatal(err)
	}

	az2, err := m.For(context.Background(), tgt,
		Credential{ID: "k", Kind: "gcp_sa", Secret: serviceAccount(t, f2.serve(t).URL)})
	if err != nil {
		t.Fatal(err)
	}
	if err := az2(context.Background(), gcpRequest(t)); err != nil {
		t.Fatal(err)
	}
	if f2.count() != 1 {
		t.Errorf("the replacement key was never exchanged; the stale source was reused")
	}
}

func TestGCPIsRaceFree(t *testing.T) {
	// Twenty concurrent requests on a cold source must exchange once, not
	// twenty times.
	f := &tokenFake{}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := az(context.Background(), gcpRequest(t)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.count() != 1 {
		t.Errorf("exchanged %d times under concurrency, want 1", f.count())
	}
}
