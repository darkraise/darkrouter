package writedeadline

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

const testIdle = 200 * time.Millisecond

func idleOf(d time.Duration) func() time.Duration { return func() time.Duration { return d } }

// A client that stops reading fills the socket and blocks the handler's next
// write. Cancelling a context does not unblock a write, so without a deadline
// the handler, its upstream and its buffers stay pinned until shutdown.
func TestAClientThatStopsReadingReleasesTheHandler(t *testing.T) {
	returned := make(chan struct{})
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for range 1 << 14 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}), idleOf(testIdle))
	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was still blocked writing to a client that stopped reading")
	}
}

// A small event waits in the response buffer until it is flushed, so the flush
// is the write that blocks on a client that stopped reading. Its failure has
// to reach the handler, which otherwise cannot tell the client from the
// provider when the response ends.
func TestAFailedFlushReachesTheHandler(t *testing.T) {
	flushErr := make(chan error, 1)
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		event := bytes.Repeat([]byte("x"), 512)
		for range 1 << 20 {
			if _, err := w.Write(event); err != nil {
				break
			}
			if err := rc.Flush(); err != nil {
				flushErr <- err
				return
			}
		}
		flushErr <- nil
	}), idleOf(testIdle))
	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-flushErr:
		if err == nil {
			t.Fatal("every flush to a client that stopped reading reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was still blocked writing to a client that stopped reading")
	}
}

// The deadline bounds one blocked write, not the response: a client that
// keeps reading receives a stream that lasts many times idle.
func TestAReadingClientOutlivesTheWriteDeadline(t *testing.T) {
	const chunks = 12
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range chunks {
			if _, err := w.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(testIdle / 2)
		}
	}), idleOf(testIdle))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) != chunks*1024 {
		t.Fatalf("read %d bytes, err %v; a steadily read stream was cut", len(body), err)
	}
}

// net/http leaves a connection's write deadline in place between keep-alive
// requests unless it clears it, so a deadline set during one response must
// not fail the next request on the same connection once it has passed.
func TestAKeptAliveConnectionServesAfterTheDeadlinePassed(t *testing.T) {
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), idleOf(testIdle))
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := srv.Client()
	get := func() (reused bool) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused },
		}))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if b, err := io.ReadAll(resp.Body); err != nil || string(b) != "ok" {
			t.Fatalf("body = %q, err %v", b, err)
		}
		return reused
	}
	get()
	time.Sleep(3 * testIdle)
	if !get() {
		t.Fatal("the second request did not reuse the connection, so this proves nothing")
	}
}
