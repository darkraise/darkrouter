package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	got, warns := NewFetcher().part(context.Background(),
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
	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{MIME: "image/png", URL: "https://generativelanguage.googleapis.com/v1beta/files/abc"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	fd := got["fileData"].(map[string]any)
	if fd["fileUri"] != "https://generativelanguage.googleapis.com/v1beta/files/abc" {
		t.Errorf("part = %v", got)
	}
}

func TestPartInlinesAPublicURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer up.Close()

	got, warns := NewFetcher().part(context.Background(), &ir.Media{URL: up.URL + "/a.png"}, "image")
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

	f := NewFetcher()
	f.MaxBytes = 10
	got, warns := f.part(context.Background(), &ir.Media{URL: up.URL + "/big.png"}, "image")
	if got != nil {
		t.Fatalf("part = %v, want nil", got)
	}
	if !hasWarning(warns, "messages[].image") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAFailedFetch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer up.Close()

	got, warns := NewFetcher().part(context.Background(), &ir.Media{URL: up.URL + "/gone.png"}, "image")
	if got != nil || !hasWarning(warns, "messages[].image") {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
}

func TestPartRefusesANonHTTPScheme(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{URL: "file:///etc/passwd"}, "document")
	if got != nil {
		t.Fatalf("part = %v; only http and https are fetched", got)
	}
	if !hasWarning(warns, "messages[].document") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAnEmptyMedia(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(), &ir.Media{}, "image")
	if got != nil || len(warns) != 1 {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
}
