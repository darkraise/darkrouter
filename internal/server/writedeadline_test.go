package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// deadlineRecorder is a response writer with a connection-shaped deadline, so a
// test can see whether a handler's writes are bounded.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines int
}

func (d *deadlineRecorder) SetWriteDeadline(time.Time) error {
	d.deadlines++
	return nil
}

// Both listeners serve clients that can stop reading: the proxy's streams, and
// the console's playground, which streams a real response through the admin
// listener.
func TestBothListenersBoundTheirWrites(t *testing.T) {
	s := newTestServer(t, nil)
	for name, h := range map[string]http.Handler{
		"proxy": s.ProxyHandler(),
		"admin": s.AdminHandler(),
	} {
		t.Run(name, func(t *testing.T) {
			rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
			if rec.deadlines == 0 {
				t.Error("a response was written with no write deadline")
			}
		})
	}
}
