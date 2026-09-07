package provider

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
)

func TestYAMLSourceCarriesThePreset(t *testing.T) {
	// Provider.Preset is documented as how quirks, surfaces and traits are
	// reached at request time. A YAML provider reached none of them, because
	// nothing between the file and this struct carried the name.
	c := &config.Config{}
	config.ApplyDefaults(c)
	c.Providers = []config.ProviderConfig{{
		ID: "cohere", Kind: "openaicompat", Preset: "cohere",
		BaseURL: "https://api.cohere.com/compatibility/v1",
		APIKey:  "sk", Models: []string{"rerank-v3.5"},
	}}
	cfgStore := config.NewStoreOf(c)
	ps, err := NewYAMLSource(cfgStore).Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Preset != "cohere" {
		t.Fatalf("providers = %+v", ps)
	}
}
