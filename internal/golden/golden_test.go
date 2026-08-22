// Package golden runs every dialect and adapter over one fixture set, in both
// directions. It is a test-only package: it exists so the fixtures have a home
// that imports every edge and every adapter without any of them importing it.
package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

var update = flag.Bool("update", false, "rewrite the golden files from current behavior")

// meta carries what a fixture needs beyond its body: the Gemini model lives in
// the URL, and the Anthropic version arrives as a header.
type meta struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

// dialects are the inbound edges, keyed by the directory name under
// testdata/golden.
func dialects() map[string]edge.Dialect {
	return map[string]edge.Dialect{
		"openai":    openaiedge.New(),
		"anthropic": anthropicedge.New(),
		"gemini":    geminiedge.New(),
	}
}

// adapters are the outbound kinds, keyed by the rendered file's base name.
func adapters() map[string]adapter.Adapter {
	return map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		// A fetcher with a zero byte cap and a transport that refuses, so a
		// fixture carrying a public image URL drops it with a warning instead
		// of reaching the network. No golden test makes an outbound request.
		"gemini": geminiadapter.NewWithFetcher(&offlineFetcher),
	}
}

const targetBase = "https://upstream.example/v1"

func target() *adapter.Target {
	return &adapter.Target{BaseURL: targetBase, APIKey: "sk-test", Model: "target-model"}
}

// caseDirs lists testdata/golden/<dialect>/<case> for one dialect.
func caseDirs(t *testing.T, dialect string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", dialect)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no cases", root)
	}
	return out
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func readMeta(t *testing.T, dir string) meta {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if os.IsNotExist(err) {
		return meta{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("meta %s: %v", dir, err)
	}
	return m
}

// requestFor builds the inbound HTTP request a fixture describes, including the
// path value a Gemini route would carry.
func requestFor(t *testing.T, dialect string, m meta, body []byte) *http.Request {
	t.Helper()
	target := "/v1/chat/completions"
	switch dialect {
	case "anthropic":
		target = "/v1/messages"
	case "gemini":
		target = "/v1beta/models/" + m.Path
	}
	r := httptest.NewRequest("POST", target, bytes.NewReader(body))
	for k, v := range m.Headers {
		r.Header.Set(k, v)
	}
	if dialect == "gemini" {
		r.SetPathValue("model", m.Path)
	}
	return r
}

// compareJSON asserts semantic equality, writing the file instead when -update
// is set. Values are compared rather than bytes so that a key-order change does
// not fail the suite.
func compareJSON(t *testing.T, path string, got any) {
	t.Helper()
	pretty, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want := readFixture(t, path)
	var gotV, wantV any
	if err := json.Unmarshal(pretty, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatalf("golden %s: %v", path, err)
	}
	if !reflect.DeepEqual(gotV, wantV) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", path, pretty, want)
	}
}

func TestGoldenRequests(t *testing.T) {
	ctx := context.Background()
	for dialect, d := range dialects() {
		for _, dir := range caseDirs(t, dialect) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				m := readMeta(t, dir)
				body := readFixture(t, filepath.Join(dir, "request.json"))

				req, _, err := d.ParseRequest(requestFor(t, dialect, m, body), 1<<20)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				compareJSON(t, filepath.Join(dir, "ir.json"), req)

				for kind, ad := range adapters() {
					hr, warns, err := ad.BuildRequest(ctx, target(), req)
					if err != nil {
						t.Fatalf("%s build: %v", kind, err)
					}
					raw, err := io.ReadAll(hr.Body)
					if err != nil {
						t.Fatal(err)
					}
					var rendered any
					if err := json.Unmarshal(raw, &rendered); err != nil {
						t.Fatalf("%s produced invalid JSON: %v", kind, err)
					}
					compareJSON(t, filepath.Join(dir, "rendered", kind+".json"), rendered)
					compareJSON(t, filepath.Join(dir, "warnings", kind+".json"), warningStrings(warns))
				}
			})
		}
	}
}

// warningStrings renders warnings as a stable, human-readable list. A golden
// file full of struct field names is unreadable in a review, and reviewing
// these is the whole point.
func warningStrings(ws []ir.Warning) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.String())
	}
	return out
}

// offlineFetcher has a zero byte cap and a client that refuses every request,
// so a fixture carrying a public image URL exercises the drop-with-a-warning
// path rather than reaching the network.
var offlineFetcher = geminiadapter.Fetcher{
	Client:   &http.Client{Transport: refuseTransport{}},
	MaxBytes: 0,
}

type refuseTransport struct{}

func (refuseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errOffline
}

var errOffline = errors.New("golden: the suite makes no outbound requests")

func recorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// compareRecorded pins the status code alongside the body, because §14 makes
// the status part of the contract — Claude Code retries a 529 and gives up on
// a 400.
func compareRecorded(t *testing.T, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: response is not JSON: %v\n%s", path, err, rec.Body.String())
	}
	compareJSON(t, path, map[string]any{
		"status": rec.Code,
		"body":   normalize(body),
	})
}

func errorsAs(err error, target **ir.Error) bool { return errors.As(err, target) }
