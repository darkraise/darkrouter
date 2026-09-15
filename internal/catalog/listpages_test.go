package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

type countingTransport struct{ n atomic.Int64 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func twoPageAnthropic(t *testing.T, second int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}],"has_more":true,"last_id":"m2"}`))
			return
		}
		w.WriteHeader(second)
		_, _ = w.Write([]byte(`{"data":[{"id":"m2"},{"id":"m3"}],"has_more":false}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListPagesReadsEveryPageThroughTheGivenClient(t *testing.T) {
	srv := twoPageAnthropic(t, http.StatusOK)
	tr := &countingTransport{}
	var statuses []int
	got, err := ListPages(context.Background(), &http.Client{Transport: tr},
		Probe{Kind: "anthropic", BaseURL: srv.URL + "/v1", AuthStyle: "x-api-key", APIKey: "sk"},
		func(resp *http.Response) error {
			statuses = append(statuses, resp.StatusCode)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ModelID)
	}
	if !slices.Equal(ids, []string{"m1", "m2", "m3"}) {
		t.Errorf("models = %v, want each once in listing order", ids)
	}
	if tr.n.Load() != 2 {
		t.Errorf("the given client made %d requests, want 2", tr.n.Load())
	}
	if !slices.Equal(statuses, []int{200, 200}) {
		t.Errorf("the hook saw %v, want every response", statuses)
	}
}

func TestListPagesEndsOnTheHooksError(t *testing.T) {
	srv := twoPageAnthropic(t, http.StatusForbidden)
	refused := errors.New("refused")
	_, err := ListPages(context.Background(), srv.Client(),
		Probe{Kind: "anthropic", BaseURL: srv.URL + "/v1", AuthStyle: "x-api-key", APIKey: "sk"},
		func(resp *http.Response) error {
			if resp.StatusCode == http.StatusForbidden {
				return refused
			}
			return nil
		})
	if !errors.Is(err, refused) {
		t.Errorf("err = %v, want the hook's error", err)
	}
}

func TestListPagesFailsANon2xxTheHookLetsThrough(t *testing.T) {
	srv := twoPageAnthropic(t, http.StatusInternalServerError)
	_, err := ListPages(context.Background(), srv.Client(),
		Probe{Kind: "anthropic", BaseURL: srv.URL + "/v1", AuthStyle: "x-api-key", APIKey: "sk"}, nil)
	if err == nil {
		t.Error("a failed page must fail the listing")
	}
}
