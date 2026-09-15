package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

// onePart renders a single block against a fresh request budget.
func onePart(t *testing.T, f *Fetcher, m *ir.Media, field string) (map[string]any, []ir.Warning) {
	t.Helper()
	got, warns, err := f.part(context.Background(), m, field, &inlineBudget{left: f.MaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	return got, warns
}

func TestPassthroughURIRecognizesOnlyTheAcceptedForms(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://generativelanguage.googleapis.com/v1beta/files/abc123", true},
		{"gs://bucket/object.png", true},
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://x.example/a.png", false},
		{"http://x.example/a.png", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := passthroughURI(tc.in); got != tc.want {
			t.Errorf("passthroughURI(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPartEmitsInlineDataForBase64(t *testing.T) {
	got, warns := onePart(t, NewFetcher(),
		&ir.Media{MIME: "image/png", Data: "AAAA"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	in := got["inlineData"].(map[string]any)
	if in["mimeType"] != "image/png" || in["data"] != "AAAA" {
		t.Errorf("part = %v", got)
	}
}

func TestPartPassesAFilesAPIURIThrough(t *testing.T) {
	got, warns := onePart(t, NewFetcher(),
		&ir.Media{MIME: "image/png", URL: "https://generativelanguage.googleapis.com/v1beta/files/abc"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	fd := got["fileData"].(map[string]any)
	if fd["fileUri"] != "https://generativelanguage.googleapis.com/v1beta/files/abc" {
		t.Errorf("part = %v", got)
	}
}

// localFetcher is a Fetcher whose dial-time egress guard is lifted, because
// every httptest server listens on loopback and the guard exists precisely to
// refuse that. The tests that use it are about what happens once a connection
// is made — the size cap, a non-2xx status, the base64 round trip. The guard
// itself is tested in ssrf_test.go, against an unmodified NewFetcher.
func localFetcher() *Fetcher {
	f := NewFetcher()
	f.Client.Transport = http.DefaultTransport
	return f
}

func TestPartInlinesAReachableURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer up.Close()

	got, warns := onePart(t, localFetcher(), &ir.Media{URL: up.URL + "/a.png"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	in, ok := got["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("part = %v; a public URL must be inlined, not sent as fileData", got)
	}
	if in["mimeType"] != "image/png" {
		t.Errorf("mimeType = %v; it comes from the response when the IR has none", in["mimeType"])
	}
	if in["data"] != "iVBORw==" {
		t.Errorf("data = %v", in["data"])
	}
}

func TestPartDropsAnOversizedURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer up.Close()

	f := localFetcher()
	f.MaxBytes = 10
	got, warns := onePart(t, f, &ir.Media{URL: up.URL + "/big.png"}, "image")
	if got != nil {
		t.Fatalf("part = %v, want nil", got)
	}
	// The reason, not just the warning: the drop has to be the size cap rather
	// than any other refusal on the way to it.
	if !hasWarning(warns, "messages[].image") ||
		!strings.Contains(warns[0].Reason, "exceeded the inline cap") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAFailedFetch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer up.Close()

	got, warns := onePart(t, localFetcher(), &ir.Media{URL: up.URL + "/gone.png"}, "image")
	if got != nil || !hasWarning(warns, "messages[].image") {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
	if !strings.Contains(warns[0].Reason, "did not return 2xx") {
		t.Errorf("reason = %q, want the status to be what dropped it", warns[0].Reason)
	}
}

func TestPartRefusesANonHTTPScheme(t *testing.T) {
	got, warns := onePart(t, NewFetcher(),
		&ir.Media{URL: "file:///etc/passwd"}, "document")
	if got != nil {
		t.Fatalf("part = %v; only http and https are fetched", got)
	}
	if !hasWarning(warns, "messages[].document") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAnEmptyMedia(t *testing.T) {
	got, warns := onePart(t, NewFetcher(), &ir.Media{}, "image")
	if got != nil || len(warns) != 1 {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
}

func TestADisabledFetcherDropsRemoteURLsWithAWarning(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	defer up.Close()

	f := localFetcher()
	f.Inline = false
	got, warns := onePart(t, f, &ir.Media{URL: up.URL + "/a.png"}, "image")
	if got != nil {
		t.Fatalf("the block should be dropped, got %v", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Reason, "media inlining is disabled") {
		t.Fatalf("warnings = %v", warns)
	}
}

func TestADisabledFetcherStillPassesWhatNeedsNoFetch(t *testing.T) {
	// The switch governs outbound requests, not media. Inline data and a
	// fileUri Gemini already resolves cost the gateway no traffic, and
	// dropping them would break prompts the switch was never about.
	f := NewFetcher()
	f.Inline = false
	for name, m := range map[string]*ir.Media{
		"inline data": {Data: "aGk=", MIME: "image/png"},
		"file id":     {FileID: "files/abc", MIME: "image/png"},
		"youtube":     {URL: "https://www.youtube.com/watch?v=x"},
	} {
		got, warns := onePart(t, f, m, "image")
		if got == nil {
			t.Errorf("%s: dropped, warnings = %v", name, warns)
		}
	}
}

func TestFetchedMediaSharesOneBudgetAcrossTheRequest(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(strings.Repeat("x", 6)))
	}))
	defer up.Close()

	image := func(name string) ir.ContentBlock {
		return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: up.URL + "/" + name}}
	}
	// Each URL fits the cap on its own; the second, arriving inside a tool
	// result, is what pushes the request over it. The third must never be
	// fetched.
	req := &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{image("a.png")}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "c1", Name: "shot", Input: []byte(`{}`)}}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockToolResult,
			ToolResult: &ir.ToolResult{ToolUseID: "c1", Content: []ir.ContentBlock{image("b.png")}}}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{image("c.png")}},
	}}

	f := localFetcher()
	f.MaxBytes = 10
	_, _, err := f.BuildRequest(context.Background(), &adapter.Target{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta", Model: "gemini-2.0-flash",
	}, req)

	var ie *ir.Error
	if !errors.As(err, &ie) || ie.Type != ir.ErrPayloadTooLarge {
		t.Fatalf("err = %v; a request over the media budget must fail as payload too large", err)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("fetched %d URLs; fetching must stop once the budget is exceeded", n)
	}
}
