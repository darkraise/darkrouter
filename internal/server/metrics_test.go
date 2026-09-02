package server

import (
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

type recordSink struct{ n int }

func (r *recordSink) Log(*store.RequestRecord) { r.n++ }

func TestMetricsRenderPrometheusText(t *testing.T) {
	b := health.New(1, time.Minute)
	b.Record(health.Key{ProviderID: "groq", KeyID: "g1", Model: "m"},
		health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429})
	sink := &recordSink{}
	m := newMetrics(b, sink)

	ms := func(v int64) *int64 { return &v }
	m.Log(&store.RequestRecord{Dialect: "openai", Surface: "llm", Status: "success", TotalMs: ms(120),
		Attempts: []store.AttemptRecord{
			{ProviderID: "groq", Outcome: "retryable_provider"},
			{ProviderID: "cerebras", Outcome: "success"},
		}})
	m.Log(&store.RequestRecord{Dialect: "openai", Surface: "llm", Status: "success", TotalMs: ms(3000)})
	m.Log(&store.RequestRecord{Dialect: "anthropic", Surface: "llm", Status: "error", TotalMs: ms(40),
		Attempts: []store.AttemptRecord{{ProviderID: "groq", Outcome: "retryable_provider"}}})
	if sink.n != 3 {
		t.Fatalf("records forwarded = %d, want 3", sink.n)
	}

	var sb strings.Builder
	m.write(&sb, 2, 7)
	out := sb.String()
	for _, want := range []string{
		"# TYPE darkrouter_requests_total counter\n",
		`darkrouter_requests_total{dialect="openai",surface="llm",status="success"} 2` + "\n",
		`darkrouter_requests_total{dialect="anthropic",surface="llm",status="error"} 1` + "\n",
		"# TYPE darkrouter_attempts_total counter\n",
		`darkrouter_attempts_total{provider="groq",outcome="retryable_provider"} 2` + "\n",
		`darkrouter_attempts_total{provider="cerebras",outcome="success"} 1` + "\n",
		"# TYPE darkrouter_request_duration_seconds histogram\n",
		`darkrouter_request_duration_seconds_bucket{le="0.1"} 1` + "\n",
		`darkrouter_request_duration_seconds_bucket{le="0.25"} 2` + "\n",
		`darkrouter_request_duration_seconds_bucket{le="5"} 3` + "\n",
		`darkrouter_request_duration_seconds_bucket{le="+Inf"} 3` + "\n",
		"darkrouter_request_duration_seconds_sum 3.16\n",
		"darkrouter_request_duration_seconds_count 3\n",
		"# TYPE darkrouter_breaker_open gauge\n",
		`darkrouter_breaker_open{provider="groq",model="m"} 1` + "\n",
		"darkrouter_log_records_dropped_total 2\n",
		"darkrouter_log_records_written_total 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMetricsEscapeLabelValues(t *testing.T) {
	m := newMetrics(health.New(1, time.Minute), &recordSink{})
	m.Log(&store.RequestRecord{Dialect: `we"ird\n`, Surface: "llm", Status: "error"})
	var sb strings.Builder
	m.write(&sb, 0, 0)
	if !strings.Contains(sb.String(), `dialect="we\"ird\\n"`) {
		t.Errorf("label not escaped:\n%s", sb.String())
	}
}
