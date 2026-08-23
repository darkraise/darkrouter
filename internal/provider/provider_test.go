package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
)

func TestResolvePicksHighestPriority(t *testing.T) {
	ps := []Provider{
		{ID: "low", Priority: 1, Models: []string{"m"}},
		{ID: "high", Priority: 10, Models: []string{"m"}},
	}
	got, ok := Resolve(ps, "m")
	if !ok || got.ID != "high" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestResolveFallsBackToDeclarationOrderOnEqualPriority(t *testing.T) {
	ps := []Provider{
		{ID: "first", Priority: 5, Models: []string{"m"}},
		{ID: "second", Priority: 5, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "first" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestResolveReportsMiss(t *testing.T) {
	if _, ok := Resolve([]Provider{{ID: "a", Models: []string{"x"}}}, "y"); ok {
		t.Fatal("expected a miss")
	}
}

func TestResolveSkipsProviderWithoutTheModel(t *testing.T) {
	ps := []Provider{
		{ID: "wrong", Priority: 99, Models: []string{"other"}},
		{ID: "right", Priority: 1, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "right" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestYAMLSourceCarriesThePreset(t *testing.T) {
	// Provider.Preset is documented as how quirks, surfaces and traits are
	// reached at request time. A YAML provider reached none of them, because
	// nothing between the file and this struct carried the name.
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  proxy_listen: ":0"
providers:
  - id: cohere
    kind: openaicompat
    preset: cohere
    base_url: https://api.cohere.com/compatibility/v1
    api_key: sk
    models: [rerank-v3.5]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	ps, err := NewYAMLSource(cfgStore).Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Preset != "cohere" {
		t.Fatalf("providers = %+v", ps)
	}
}
