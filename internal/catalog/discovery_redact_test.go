package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

// A transport error names the request URL, and a query-param credential is
// part of that URL. The failure row is stored in plaintext, unlike the key.
func TestAFailedListingDoesNotRecordAQueryParamKey(t *testing.T) {
	const secret = "sk-live/secret+value="
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1", AuthStyle: "query-param",
		Credentials: []provider.Credential{{ID: "k", Secret: secret, Enabled: true}},
	}}}
	NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	states, err := db.DiscoveryStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := states["p"].LastError
	if got == "" {
		t.Fatal("the failed listing recorded no error")
	}
	for _, form := range []string{secret, "sk-live%2Fsecret%2Bvalue%3D", "secret"} {
		if strings.Contains(got, form) {
			t.Errorf("last_error %q carries the credential as %q", got, form)
		}
	}
}

// Removing a key by replacing its text everywhere rewrites whatever else
// shares that text. A short key is still kept out of the row, by dropping the
// query string it travelled in, while the rest of the message stays readable.
func TestAShortQueryParamKeyDoesNotMangleTheRecordedError(t *testing.T) {
	const secret = "models"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1", AuthStyle: "query-param",
		Credentials: []provider.Credential{{ID: "k", Secret: secret, Enabled: true}},
	}}}
	NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	states, err := db.DiscoveryStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := states["p"].LastError
	if !strings.Contains(got, srv.URL+"/v1/models") {
		t.Errorf("last_error %q no longer names the listing URL", got)
	}
	if strings.Contains(got, "key=") {
		t.Errorf("last_error %q carries the query string the key was sent in", got)
	}
}
