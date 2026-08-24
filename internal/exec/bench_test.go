package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
)

// The upstream is a local httptest server, so absolute numbers carry no
// network. What the comparison isolates is Darkrouter's own work: the render
// the fast path skips, and the per-event re-serialization it also skips.
//
// The IR baseline is produced by hiding the adapter's Forwarder implementation
// rather than by a production flag, which is the same technique the differential
// suite uses and for the same reason.
type benchUnforwardable struct{ adapter.Adapter }

const benchInbound = `{"model":"target-model","max_tokens":64,` +
	`"messages":[{"role":"user","content":"Summarize the following in one sentence."}]}`

const benchUnaryResponse = `{"id":"msg_1","type":"message","role":"assistant",
	"content":[{"type":"text","text":"A one sentence summary."}],"model":"target-model",
	"stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":8}}`

// benchStream writes twelve events, the first content-bearing one third, which
// is what a real Anthropic stream looks like at the front.
func benchStream() string {
	var b strings.Builder
	b.WriteString("event: message_start\ndata: {\"type\":\"message_start\"," +
		"\"message\":{\"id\":\"msg_1\",\"model\":\"target-model\"," +
		"\"usage\":{\"input_tokens\":42}}}\n\n")
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\"," +
		"\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	for i := 0; i < 9; i++ {
		b.WriteString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"," +
			"\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"word \"}}\n\n")
	}
	b.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\"," +
		"\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":9}}\n\n")
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

// benchExecutor is newExecutorFor with the fast path optionally hidden.
func benchExecutor(b *testing.B, upstreamURL string, forward bool) *Executor {
	b.Helper()
	e := newExecutorFor(b, "anthropic", upstreamURL, Deps{Log: &capture{}})
	if !forward {
		for k, ad := range e.adapters {
			e.adapters[k] = benchUnforwardable{ad}
		}
	}
	return e
}

func BenchmarkUnaryBothPaths(b *testing.B) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(benchUnaryResponse))
	}))
	defer up.Close()

	for _, tc := range []struct {
		name    string
		forward bool
	}{{"passthrough", true}, {"ir", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := benchExecutor(b, up.URL, tc.forward)
			d := anthropicedge.New()
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(benchInbound))
				r.Header.Set("Content-Type", "application/json")
				e.Handle(httptest.NewRecorder(), r, d)
			}
		})
	}
}

func BenchmarkStreamTimeToFirstToken(b *testing.B) {
	body := benchStream()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer up.Close()

	inbound := strings.Replace(benchInbound, `"max_tokens":64`, `"max_tokens":64,"stream":true`, 1)
	for _, tc := range []struct {
		name    string
		forward bool
	}{{"passthrough", true}, {"ir", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := benchExecutor(b, up.URL, tc.forward)
			d := anthropicedge.New()
			b.ReportAllocs()

			var total time.Duration
			n := 0
			for b.Loop() {
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(inbound))
				r.Header.Set("Content-Type", "application/json")
				w := &firstByteRecorder{ResponseRecorder: httptest.NewRecorder()}
				start := time.Now()
				e.Handle(w, r, d)
				// TTFT is to the client's first byte, not to the end: that is
				// the number a user feels, and it is the one the fast path
				// moves.
				total += w.first.Sub(start)
				n++
			}
			b.ReportMetric(float64(total.Nanoseconds())/float64(n), "ns/ttft")
		})
	}
}

// firstByteRecorder stamps the moment the first body byte is written.
type firstByteRecorder struct {
	*httptest.ResponseRecorder
	first time.Time
}

func (w *firstByteRecorder) Write(b []byte) (int, error) {
	if w.first.IsZero() && len(b) > 0 {
		w.first = time.Now()
	}
	return w.ResponseRecorder.Write(b)
}

func (w *firstByteRecorder) Flush() { w.ResponseRecorder.Flush() }
