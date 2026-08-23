package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

type fakeLister struct {
	models []Discovered
}

func (f *fakeLister) List(context.Context, Probe) ([]Discovered, error) {
	return f.models, nil
}

func TestAKindListerReplacesTheGenericListing(t *testing.T) {
	// bedrock has no /models endpoint; ProbeFor refuses the kind, and without
	// a registered lister discovery silently does nothing for it. That silence
	// is correct for vertex and wrong for bedrock.
	f := &fakeLister{models: []Discovered{{ModelID: "us.anthropic.claude-x"}}}
	p := provider.Provider{
		ID: "bed", Kind: "bedrock", Region: "us-east-1", AuthStyle: "sigv4",
		Credentials: []provider.Credential{{ID: "k", Secret: "{}", Enabled: true}},
	}
	pr, err := ProbeForKind(p, Preset{}, "{}", map[string]KindLister{"bedrock": f})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Lister == nil {
		t.Fatal("the registered lister did not reach the probe")
	}
	if pr.Region != "us-east-1" {
		t.Errorf("region = %q; a signed listing needs it", pr.Region)
	}
	// And the credential must not have been copied into the probe as an api
	// key: a sigv4 credential is a JSON document, not a bearer token.
	if pr.APIKey != "" {
		t.Errorf("APIKey = %q; a signed probe carries no bearer token", pr.APIKey)
	}
}

func TestAnUnregisteredKindIsStillUndiscoverable(t *testing.T) {
	// vertex has no listing API at all, spec §4.3. It must stay a silent skip
	// rather than becoming an error the discovery worker logs every tick.
	p := provider.Provider{ID: "v", Kind: "vertex"}
	_, err := ProbeForKind(p, Preset{}, "", nil)
	if err == nil {
		t.Fatal("vertex must remain undiscoverable")
	}
	if !errors.Is(err, ErrKindNotDiscoverable) {
		t.Errorf("error = %v, want ErrKindNotDiscoverable", err)
	}
}

func TestProbeForKindFallsBackToTheGenericProbe(t *testing.T) {
	// An openaicompat provider with a lister registry present must be entirely
	// unaffected by it.
	p := provider.Provider{ID: "g", Kind: "openaicompat", BaseURL: "https://x/v1", AuthStyle: "bearer"}
	pr, err := ProbeForKind(p, Preset{}, "sk-1", map[string]KindLister{"bedrock": &fakeLister{}})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Lister != nil {
		t.Error("a lister leaked onto a discoverable kind")
	}
	if pr.APIKey != "sk-1" {
		t.Errorf("APIKey = %q", pr.APIKey)
	}
}
