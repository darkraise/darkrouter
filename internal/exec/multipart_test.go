package exec

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// buildForm writes a multipart body with the parts in the order given, so a
// test can put the model field after the file exactly as a client may.
func buildForm(t *testing.T, parts [][2]string, file [2]string, fileFirst bool) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeFile := func() {
		fw, err := w.CreateFormFile("file", file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, file[1]); err != nil {
			t.Fatal(err)
		}
	}
	if fileFirst {
		writeFile()
	}
	for _, p := range parts {
		if err := w.WriteField(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if !fileFirst {
		writeFile()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), w.FormDataContentType()
}

func parseForm(t *testing.T, body, ct string, max int64) (*Form, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	return ParseForm(r, max)
}

// reparse reads a rendered form back, which is how a test asserts on what the
// upstream would actually receive rather than on the writer's intentions.
func reparse(t *testing.T, body []byte, contentType string) *Form {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	f, err := ParseForm(r, 1<<20)
	if err != nil {
		t.Fatalf("rendered form did not parse: %v", err)
	}
	return f
}

func TestParseFormFindsAFieldPlacedAfterTheFile(t *testing.T) {
	// Clients do this. A streaming router would have had to consume the whole
	// upload before it knew where to route.
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}, {"language", "en"}},
		[2]string{"a.mp3", "AUDIO"}, true)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Field("model"); got != "whisper-1" {
		t.Errorf("model = %q", got)
	}
	if got := f.Field("language"); got != "en" {
		t.Errorf("language = %q", got)
	}
	if got := f.Field("absent"); got != "" {
		t.Errorf("absent field = %q", got)
	}
}

func TestRenderRewritesTheModelInsideTheForm(t *testing.T) {
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}},
		[2]string{"a.mp3", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("distil-whisper-large-v3")
	if err != nil {
		t.Fatal(err)
	}
	got := reparse(t, out, outCT)
	if got.Field("model") != "distil-whisper-large-v3" {
		t.Errorf("model = %q; the target's name must replace the client's", got.Field("model"))
	}
	if got.File("file") != "AUDIO" {
		t.Errorf("file = %q; the upload did not survive the rewrite", got.File("file"))
	}
}

func TestRenderAddsAModelFieldWhenTheClientSentNone(t *testing.T) {
	// The router resolved a target from an alias in the URL or from a default,
	// so the upstream still needs a model name.
	body, ct := buildForm(t, nil, [2]string{"a.mp3", "AUDIO"}, true)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("whisper-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := reparse(t, out, outCT).Field("model"); got != "whisper-1" {
		t.Errorf("model = %q", got)
	}
}

func TestRenderIsReplayable(t *testing.T) {
	// Two attempts, two different targets, both from one buffered body. This
	// is the whole reason the body is buffered.
	body, ct := buildForm(t, [][2]string{{"model", "a"}}, [2]string{"a.mp3", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, ct1, err := f.Render("first-model")
	if err != nil {
		t.Fatal(err)
	}
	second, ct2, err := f.Render("second-model")
	if err != nil {
		t.Fatal(err)
	}
	if reparse(t, first, ct1).Field("model") != "first-model" {
		t.Error("first render lost its model")
	}
	if reparse(t, second, ct2).Field("model") != "second-model" {
		t.Error("second render lost its model")
	}
	if reparse(t, second, ct2).File("file") != "AUDIO" {
		t.Error("the second render lost the upload")
	}
}

func TestRenderPreservesTheFilePartMetadata(t *testing.T) {
	// Whisper providers select a decoder from the filename extension. Dropping
	// it turns a working upload into an unsupported-format error.
	body, ct := buildForm(t, [][2]string{{"model", "m"}},
		[2]string{"recording.m4a", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("m")
	if err != nil {
		t.Fatal(err)
	}
	if name := reparse(t, out, outCT).FileName("file"); name != "recording.m4a" {
		t.Errorf("filename = %q", name)
	}
}

func TestParseFormRefusesAnOversizedUpload(t *testing.T) {
	body, ct := buildForm(t, [][2]string{{"model", "m"}},
		[2]string{"a.mp3", strings.Repeat("A", 4096)}, false)
	_, err := parseForm(t, body, ct, 512)
	if err == nil {
		t.Fatal("an oversized upload was accepted")
	}
	var ie *ir.Error
	if !errors.As(err, &ie) || ie.Type != ir.ErrPayloadTooLarge {
		t.Errorf("err = %v; it must be distinguishable so the route answers 413", err)
	}
}

func requirePayloadTooLarge(t *testing.T, err error) {
	t.Helper()
	var ie *ir.Error
	if !errors.As(err, &ie) || ie.Type != ir.ErrPayloadTooLarge {
		t.Fatalf("err = %v, want payload_too_large", err)
	}
}

// Part headers are held for the life of the request, so they are spent from
// the same budget as part values. Otherwise a client sends megabytes of
// headers on empty parts and the budget barely moves.
func TestParseFormChargesPartHeadersAgainstTheBudget(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for range 16 {
		h := textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="pad"`},
			"X-Pad":               {strings.Repeat("h", 1024)},
		}
		if _, err := w.CreatePart(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := parseForm(t, buf.String(), w.FormDataContentType(), 8<<10)
	requirePayloadTooLarge(t, err)
}

// Every part costs memory beyond its encoded bytes, so a flood of empty parts
// whose encoding alone fits must still exhaust the budget.
func TestParseFormChargesEachPartAgainstTheBudget(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for range 4000 {
		if err := w.WriteField("x", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := parseForm(t, buf.String(), w.FormDataContentType(), int64(buf.Len())*2)
	requirePayloadTooLarge(t, err)
}

func TestParseFormRejectsANonMultipartBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(`{"a":1}`))
	r.Header.Set("Content-Type", "application/json")
	if _, err := ParseForm(r, 1<<20); err == nil {
		t.Fatal("a JSON body parsed as multipart")
	}
}
