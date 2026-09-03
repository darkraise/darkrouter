package catalog

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEmbeddedPresetsValidate(t *testing.T) {
	ps, err := LoadPresets()
	if err != nil {
		t.Fatalf("embedded presets do not load: %v", err)
	}
	if len(ps) == 0 {
		t.Fatal("embedded presets are empty")
	}
	for id, p := range ps {
		if p.Name == "" {
			t.Errorf("%s: no name", id)
		}
		if !Kinds[p.Kind] {
			t.Errorf("%s: kind %q is not a real kind", id, p.Kind)
		}
		if !AuthStyles[p.Auth.Style] {
			t.Errorf("%s: auth style %q is not in the vocabulary", id, p.Auth.Style)
		}
		if len(p.Surfaces) == 0 {
			t.Errorf("%s: declares no surfaces", id)
		}
		for _, q := range p.Quirks {
			if !knownQuirk(q) {
				t.Errorf("%s: quirk %q is not in the closed vocabulary", id, q)
			}
		}
		// Spec §10: every entry has a models_dev_id or an explicit exemption.
		// Silence is the failure mode this catches — an entry that simply
		// forgot the join key falls back to inferred metadata forever and
		// nothing says so.
		if p.ModelsDevID == "" && !p.NoModelsDev {
			t.Errorf("%s: neither models_dev_id nor no_models_dev", id)
		}
		// The anonymous style is only anonymous if the published credential
		// actually ships: without it the preset promises a provider an
		// operator can add with no key and then sends an empty Authorization
		// header, which is a 401 the console never explains.
		if p.Auth.Style == "anonymous" && p.Auth.Key == "" {
			t.Errorf("%s: auth style anonymous with no published key", id)
		}
		if p.Auth.Style != "anonymous" && p.Auth.Key != "" {
			t.Errorf("%s: ships an auth key with style %q", id, p.Auth.Style)
		}
		if p.Auth.Style == "oauth" {
			if p.OAuth == nil {
				t.Errorf("%s: auth style oauth with no oauth block", id)
				continue
			}
			if p.OAuth.AuthorizeURL == "" || p.OAuth.TokenURL == "" ||
				p.OAuth.ClientID == "" || len(p.OAuth.Scopes) == 0 {
				t.Errorf("%s: incomplete oauth block", id)
			}
			if p.OAuth.Redirect.Style != "localhost" && p.OAuth.Redirect.Style != "manual" {
				t.Errorf("%s: redirect style %q", id, p.OAuth.Redirect.Style)
			}
			if p.OAuth.Redirect.Style == "localhost" && p.OAuth.Redirect.Port == 0 {
				t.Errorf("%s: localhost redirect with no port", id)
			}
		}
	}
}

func TestAnonymousAuthResolvesThePublishedKey(t *testing.T) {
	a := Auth{Style: "anonymous", Key: "0000000000"}
	if got := a.Secret(""); got != "0000000000" {
		t.Errorf("secret = %q, want the published key", got)
	}
	if got := a.Secret("mine"); got != "mine" {
		t.Errorf("secret = %q, want the configured credential to win", got)
	}
	// Every other style has nothing to fall back to, and inventing one would
	// send an empty header as if it were a key.
	if got := (Auth{Style: "bearer"}).Secret(""); got != "" {
		t.Errorf("bearer resolved %q", got)
	}
}

func TestSelfServingIsWhatAnOperatorCanUseWithoutConfiguringAnything(t *testing.T) {
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	ids := ps.SelfServing()
	if len(ids) == 0 {
		t.Fatal("no preset is usable without a credential; the release seeds nothing")
	}
	for _, id := range ids {
		p := ps[id]
		if !AuthStyles[p.Auth.Style] {
			t.Errorf("%s: unknown style %q", id, p.Auth.Style)
		}
		switch p.Auth.Style {
		case "none", "optional", "anonymous":
		default:
			t.Errorf("%s: style %q needs a credential the operator has to find", id, p.Auth.Style)
		}
		// A local runtime asks for no credential either, and seeding one would
		// add a provider pointing at a server nobody has started.
		if isLoopback(p.BaseURL) {
			t.Errorf("%s: %s is this machine, not a service", id, p.BaseURL)
		}
		if !strings.HasPrefix(p.BaseURL, "http") {
			t.Errorf("%s: %s needs a transport of its own", id, p.BaseURL)
		}
	}
}

func TestSelfServingIsSortedSoSeedingIsStable(t *testing.T) {
	ps := Presets{
		"zeta":  {Name: "Z", Kind: "openaicompat", BaseURL: "https://z.example/v1", Auth: Auth{Style: "none"}},
		"alpha": {Name: "A", Kind: "openaicompat", BaseURL: "https://a.example/v1", Auth: Auth{Style: "optional"}},
		"keyed": {Name: "K", Kind: "openaicompat", BaseURL: "https://k.example/v1", Auth: Auth{Style: "bearer"}},
		"local": {Name: "L", Kind: "openaicompat", BaseURL: "http://localhost:11434/v1", Auth: Auth{Style: "none"}},
		"cli":   {Name: "C", Kind: "openaicompat", BaseURL: "auggie://cli/v1", Auth: Auth{Style: "optional"}},
	}
	got := ps.SelfServing()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("SelfServing() = %v, want [alpha zeta]", got)
	}
}

func TestDuplicateIDIsRejected(t *testing.T) {
	// yaml.v3 rejects a repeated mapping key outright, which is what makes the
	// spec's map shape enforce "no id repeats" without a separate check.
	_, err := parsePresets([]byte("groq:\n  name: A\n  kind: openaicompat\ngroq:\n  name: B\n  kind: openaicompat\n"))
	if err == nil {
		t.Fatal("a duplicate id parsed cleanly")
	}
	if !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("error does not name the duplicate: %v", err)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	// A typo in a generated file is otherwise silently dropped, and the preset
	// serves with a field the author believed they had set.
	_, err := parsePresets([]byte("groq:\n  name: Groq\n  kind: openaicompat\n  base_yurl: x\n"))
	if err == nil {
		t.Fatal("an unknown field parsed cleanly")
	}
}

func TestValuedQuirksParse(t *testing.T) {
	for _, q := range []string{"rerank-path=/v2/rerank", "context-override=8192"} {
		if !knownQuirk(q) {
			t.Errorf("%q rejected", q)
		}
	}
	for _, q := range []string{"rerank-path", "context-override", "invented-quirk", "rerank-path="} {
		if knownQuirk(q) {
			t.Errorf("%q accepted", q)
		}
	}
}

func TestBareQuirkWithValueIsRejected(t *testing.T) {
	// A bare tag given a value is a different quirk from the one the
	// vocabulary declares, and accepting it would let a preset silently
	// configure nothing.
	if knownQuirk("no-system-role=true") {
		t.Error("a bare quirk accepted a value")
	}
}

func TestEveryDeclaredSurfaceParses(t *testing.T) {
	// merge.parseSurfaces drops what ir.ParseSurface rejects, so a preset
	// spelling a surface wrong loses it silently: the model serves chat only
	// and nothing reports the loss. This is the guard for that.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for id, p := range ps {
		for _, s := range p.Surfaces {
			if _, ok := ir.ParseSurface(s); !ok {
				t.Errorf("%s: surface %q is not in the vocabulary", id, s)
			}
		}
	}
}

func TestAuxiliarySurfacesAreDeclaredSomewhere(t *testing.T) {
	// Phase 5's done criterion is that each of the seven routes reaches a real
	// provider. A surface no preset declares cannot, and the failure would be
	// a confusing "no provider offers this" at request time rather than here.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]int{}
	for _, p := range ps {
		for _, s := range p.Surfaces {
			declared[s]++
		}
	}
	for _, s := range ir.AllSurfaces() {
		if declared[string(s)] == 0 {
			t.Errorf("no shipped preset declares the %q surface", s)
		}
	}
}

func TestRerankPresetsDeclareAPath(t *testing.T) {
	// Spec §3.1: each preset declares its own rerank path, because providers
	// expose it at differing URLs. A rerank surface without one would build a
	// request against the chat path.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for id, p := range ps {
		serves := false
		for _, s := range p.Surfaces {
			if s == string(ir.SurfaceRerank) {
				serves = true
			}
		}
		if !serves {
			continue
		}
		if _, ok := p.QuirkValue("rerank-path"); !ok {
			t.Errorf("%s declares the rerank surface with no rerank-path quirk", id)
		}
	}
}

// A preset must declare a LiteLLM join key or an explicit exemption. Without
// the exemption a forgotten key is indistinguishable from a provider the index
// genuinely does not cover, which is the failure the models.dev key already
// guards against.
func TestEveryPresetDeclaresALiteLLMKeyOrExemption(t *testing.T) {
	for id, p := range Embedded() {
		if p.LiteLLMID == "" && !p.NoLiteLLM {
			t.Errorf("%s: needs litellm_id or no_litellm", id)
		}
	}
}
