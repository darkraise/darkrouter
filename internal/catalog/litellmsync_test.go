package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const litellmSyncSample = `{
	"gpt-4o": {"litellm_provider": "openai", "input_cost_per_token": 2.5e-06, "output_cost_per_token": 1e-05},
	"claude-3-5-sonnet": {"litellm_provider": "anthropic", "input_cost_per_token": 3e-06, "output_cost_per_token": 1.5e-05}
}`

func TestLiteLLMSyncerServesTheFetchedIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(litellmSyncSample))
	}))
	defer srv.Close()

	s := NewLiteLLMSyncer(LiteLLMSyncOptions{URL: srv.URL})
	if len(s.Doc()) != 0 {
		t.Fatal("the syncer holds prices before any fetch")
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.Doc()
	if p := got["openai"]["gpt-4o"]; !p.Known || p.InputMicrosPerMTok != 2_500_000 || p.OutputMicrosPerMTok != 10_000_000 {
		t.Errorf("gpt-4o = %+v", p)
	}
	if p := got["anthropic"]["claude-3-5-sonnet"]; p.Source != SourceLiteLLM {
		t.Errorf("claude-3-5-sonnet stamped %q, want litellm", p.Source)
	}
}

func TestLiteLLMSyncerKeepsWhatItHasWhenTheFetchFails(t *testing.T) {
	// A gateway that lost the index because GitHub had a bad minute would
	// start billing models it had a real rate for as unpriced.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(litellmSyncSample))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s := NewLiteLLMSyncer(LiteLLMSyncOptions{URL: srv.URL})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("a 502 was reported as a successful sync")
	}
	if !s.Doc()["openai"]["gpt-4o"].Known {
		t.Error("a failed fetch emptied the index it already had")
	}
}

func TestLiteLLMSyncerRefusesADocumentItCannotRead(t *testing.T) {
	s, stop := litellmSyncerAfterAGoodSync(t, "<html>not json</html>")
	defer stop()

	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("an unparseable body was reported as a successful sync")
	}
	if !s.Doc()["openai"]["gpt-4o"].Known {
		t.Error("an unparseable body emptied the index it already had")
	}
}

// A 200 carrying a document that parses to nothing is the one failure the
// transport cannot see, and the one that silently unprices everything.
func TestLiteLLMSyncerRefusesAnEmptyDocument(t *testing.T) {
	for _, body := range []string{`{}`, `{"gpt-4o": {"input_cost_per_token": 2.5e-06,"output_cost_per_token":2.5e-06}}`} {
		func() {
			s, stop := litellmSyncerAfterAGoodSync(t, body)
			defer stop()

			if err := s.SyncOnce(context.Background()); err == nil {
				t.Errorf("%s was reported as a successful sync", body)
			}
			if !s.Doc()["openai"]["gpt-4o"].Known {
				t.Errorf("%s emptied the index it already had", body)
			}
		}()
	}
}

// litellmSyncerAfterAGoodSync returns a syncer holding a real index, whose next
// fetch serves nextBody. Retention is only testable against a syncer that has
// something to lose.
func litellmSyncerAfterAGoodSync(t *testing.T, nextBody string) (*LiteLLMSyncer, func()) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(litellmSyncSample))
			return
		}
		_, _ = w.Write([]byte(nextBody))
	}))
	s := NewLiteLLMSyncer(LiteLLMSyncOptions{URL: srv.URL})
	if err := s.SyncOnce(context.Background()); err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return s, srv.Close
}

func TestLiteLLMSyncOptionsDefaultToDaily(t *testing.T) {
	o := LiteLLMSyncOptions{}.withDefaults()
	if o.Interval != 24*time.Hour {
		t.Errorf("interval = %v, want 24h", o.Interval)
	}
	if o.URL != LiteLLMURL {
		t.Errorf("url = %q", o.URL)
	}
	if o.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", o.Timeout)
	}
}
