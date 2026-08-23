package golden

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// normalize strips the fields no writer can make deterministic. `created` is
// the only one: everything else in a response is a pure function of the IR.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			if k == "created" {
				t[k] = float64(0)
				continue
			}
			t[k] = normalize(inner)
		}
		return t
	case []any:
		for i, inner := range t {
			t[i] = normalize(inner)
		}
		return t
	default:
		return v
	}
}

func responseCaseDirs(t *testing.T, kind string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", "responses", kind)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		// A kind with no response fixtures. TestEveryKindHasResponseFixtures
		// is what stops that from being an accident.
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

func TestGoldenResponses(t *testing.T) {
	for kind, ad := range adapters() {
		for _, dir := range responseCaseDirs(t, kind) {
			t.Run(kind+"/"+filepath.Base(dir), func(t *testing.T) {
				body := readFixture(t, filepath.Join(dir, "response.json"))
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(string(body))),
				}
				out, err := ad.ParseResponse(resp)
				if err != nil {
					// A parse that fails is itself a result worth pinning: the
					// Gemini blocked prompt reaches the client as an error.
					var e *ir.Error
					if !errorsAs(err, &e) {
						t.Fatalf("parse: %v", err)
					}
					compareJSON(t, filepath.Join(dir, "error.json"), e)
					for dialect, d := range dialects() {
						rec := recorder()
						if werr := d.WriteError(rec, e); werr != nil {
							t.Fatalf("%s: %v", dialect, werr)
						}
						compareRecorded(t, filepath.Join(dir, "written", dialect+".json"), rec)
					}
					return
				}

				compareJSON(t, filepath.Join(dir, "ir.json"), out)
				for dialect, d := range dialects() {
					rec := recorder()
					if werr := d.WriteResponse(rec, out); werr != nil {
						t.Fatalf("%s: %v", dialect, werr)
					}
					compareRecorded(t, filepath.Join(dir, "written", dialect+".json"), rec)
				}
			})
		}
	}
}
