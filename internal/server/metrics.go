package server

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

// durationBuckets are the histogram's upper bounds in seconds. Fixed rather
// than configurable: a scrape that changes shape between deploys is worse
// than one with a slightly wrong resolution.
var durationBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

type requestKey struct{ dialect, surface, status string }
type attemptKey struct{ provider, outcome string }

// metrics counts what the request log already carries, so the scrape sees
// exactly what the trace does. It sits in front of the log writer as an
// exec.Logger and never blocks: one mutex, a few map increments.
type metrics struct {
	next    exec.Logger
	breaker interface{ Snapshot() []health.Entry }
	now     func() time.Time

	mu       sync.Mutex
	requests map[requestKey]uint64
	attempts map[attemptKey]uint64
	buckets  []uint64
	sum      float64
	count    uint64
}

func newMetrics(breaker interface{ Snapshot() []health.Entry }, next exec.Logger) *metrics {
	return &metrics{
		next: next, breaker: breaker, now: time.Now,
		requests: make(map[requestKey]uint64),
		attempts: make(map[attemptKey]uint64),
		buckets:  make([]uint64, len(durationBuckets)),
	}
}

func (m *metrics) Log(rec *store.RequestRecord) {
	m.observe(rec)
	if m.next != nil {
		m.next.Log(rec)
	}
}

func (m *metrics) observe(rec *store.RequestRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[requestKey{rec.Dialect, rec.Surface, rec.Status}]++
	for _, a := range rec.Attempts {
		m.attempts[attemptKey{a.ProviderID, a.Outcome}]++
	}
	if rec.TotalMs != nil {
		secs := float64(*rec.TotalMs) / 1000
		for i, le := range durationBuckets {
			if secs <= le {
				m.buckets[i]++
			}
		}
		m.sum += secs
		m.count++
	}
}

// write renders the Prometheus text exposition. Series are sorted so two
// scrapes of the same state produce the same bytes.
func (m *metrics) write(w io.Writer, dropped, written int64) {
	m.mu.Lock()
	requests := make([]string, 0, len(m.requests))
	for k, v := range m.requests {
		requests = append(requests, fmt.Sprintf("darkrouter_requests_total{dialect=%s,surface=%s,status=%s} %d\n",
			quoteLabel(k.dialect), quoteLabel(k.surface), quoteLabel(k.status), v))
	}
	attempts := make([]string, 0, len(m.attempts))
	for k, v := range m.attempts {
		attempts = append(attempts, fmt.Sprintf("darkrouter_attempts_total{provider=%s,outcome=%s} %d\n",
			quoteLabel(k.provider), quoteLabel(k.outcome), v))
	}
	buckets := append([]uint64(nil), m.buckets...)
	sum, count := m.sum, m.count
	m.mu.Unlock()
	sort.Strings(requests)
	sort.Strings(attempts)

	fmt.Fprint(w, "# HELP darkrouter_requests_total Requests served, by inbound dialect, surface and final status.\n"+
		"# TYPE darkrouter_requests_total counter\n")
	for _, l := range requests {
		fmt.Fprint(w, l)
	}
	fmt.Fprint(w, "# HELP darkrouter_attempts_total Upstream attempts, by provider and outcome.\n"+
		"# TYPE darkrouter_attempts_total counter\n")
	for _, l := range attempts {
		fmt.Fprint(w, l)
	}
	fmt.Fprint(w, "# HELP darkrouter_request_duration_seconds Request wall time from receipt to the log record.\n"+
		"# TYPE darkrouter_request_duration_seconds histogram\n")
	for i, le := range durationBuckets {
		fmt.Fprintf(w, "darkrouter_request_duration_seconds_bucket{le=\"%g\"} %d\n", le, buckets[i])
	}
	fmt.Fprintf(w, "darkrouter_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(w, "darkrouter_request_duration_seconds_sum %g\n", sum)
	fmt.Fprintf(w, "darkrouter_request_duration_seconds_count %d\n", count)

	fmt.Fprint(w, "# HELP darkrouter_breaker_open 1 while any credential for the provider and model is cooling.\n"+
		"# TYPE darkrouter_breaker_open gauge\n")
	for _, l := range m.breakerOpen() {
		fmt.Fprint(w, l)
	}

	fmt.Fprintf(w,
		"# HELP darkrouter_log_records_dropped_total Request records discarded because the log channel was full.\n"+
			"# TYPE darkrouter_log_records_dropped_total counter\n"+
			"darkrouter_log_records_dropped_total %d\n"+
			"# HELP darkrouter_log_records_written_total Request records persisted.\n"+
			"# TYPE darkrouter_log_records_written_total counter\n"+
			"darkrouter_log_records_written_total %d\n",
		dropped, written)
}

func (m *metrics) breakerOpen() []string {
	if m.breaker == nil {
		return nil
	}
	now := m.now()
	open := map[[2]string]bool{}
	for _, e := range m.breaker.Snapshot() {
		if !e.CoolingUntil.IsZero() && now.Before(e.CoolingUntil) {
			open[[2]string{e.Key.ProviderID, e.Key.Model}] = true
		}
	}
	out := make([]string, 0, len(open))
	for k := range open {
		out = append(out, fmt.Sprintf("darkrouter_breaker_open{provider=%s,model=%s} 1\n",
			quoteLabel(k[0]), quoteLabel(k[1])))
	}
	sort.Strings(out)
	return out
}

// quoteLabel renders a label value per the exposition format: backslash,
// double quote and newline are the three characters that need escaping.
func quoteLabel(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}
