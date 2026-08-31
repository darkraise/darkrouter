package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// One credential's token endpoint accepts the connection and never answers.
// The worker walks its list in order under the process-lifetime context, so
// without a bound of its own the whole list stops there — every credential
// behind the hung one goes unrenewed until the process restarts.
func TestOneHungEndpointDoesNotStopTheRest(t *testing.T) {
	release := make(chan struct{})
	var hung, answered int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") == "rt-hang" {
			hung++
			<-release
			return
		}
		answered++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1",` +
			`"token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	// Released before the server is closed: Close waits on active connections,
	// and the hung handler is one.
	defer close(release)

	m := NewManager(Deps{
		Tokens: newMemTokens(),
		OAuth:  fixedPresets{cfg: OAuthConfig{TokenURL: srv.URL, ClientID: "client"}},
		HTTP:   srv.Client(),
	})
	w := NewRefreshWorker(m, &expiringFake{rows: []StoredCredential{
		{ID: "hung", ProviderID: "sub", Kind: "oauth", Style: StyleOAuth,
			Preset: "anthropic-oauth", Secret: tokenWithRefresh(t, "rt-hang")},
		{ID: "behind", ProviderID: "sub", Kind: "oauth", Style: StyleOAuth,
			Preset: "anthropic-oauth", Secret: tokenWithRefresh(t, "rt-0")},
	}}, RefreshOptions{PerCredential: 100 * time.Millisecond})

	done := make(chan struct{})
	go func() { defer close(done); w.Once(context.Background()) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Once never returned: one hung endpoint blocked the whole pass")
	}
	if answered != 1 {
		t.Errorf("the credential behind the hung one was refreshed %d times, want 1", answered)
	}
}

func tokenWithRefresh(t *testing.T, refresh string) string {
	t.Helper()
	tok := Token{AccessToken: "at-0", RefreshToken: refresh,
		ExpiresAt: time.Now().Add(-time.Minute)}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Every caller is supposed to supply a client, and the one in production did
// not: the fallback was http.DefaultClient, which has no timeout at all. A
// call path whose context carries no deadline would hang forever on a token
// endpoint that accepts the connection and says nothing.
func TestTheFallbackTokenClientIsBounded(t *testing.T) {
	if tokenClient.Timeout <= 0 {
		t.Fatal("the fallback token client has no timeout")
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	restore := tokenClient.Timeout
	tokenClient.Timeout = 100 * time.Millisecond
	defer func() { tokenClient.Timeout = restore }()

	done := make(chan error, 1)
	go func() {
		_, err := postToken(context.Background(), nil, srv.URL, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a hung token endpoint returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("postToken never returned with no client and no deadline")
	}
}
