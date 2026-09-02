package admin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAliasWritesAreVisibleThroughBothSurfaces(t *testing.T) {
	// Two write paths that can disagree is the failure worth testing for: the
	// focused endpoint and /api/config share one store method and one
	// validation, so a write through either must be visible through the other.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	if w := do(t, s, cookie, token, "PUT", "/api/aliases",
		`{"fast":["groq/llama"]}`); w.Code != 200 {
		t.Fatalf("PUT /api/aliases = %d: %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", "/api/aliases", "")
	var got map[string][]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got["fast"]) != 1 {
		t.Errorf("GET /api/aliases = %v", got)
	}
	if body := getConfig(t, s); body.Blocks["aliases"] == nil {
		t.Error("the write is not visible through /api/config")
	}
	if live := s.deps.Config.Current().Aliases["fast"]; len(live) != 1 {
		t.Errorf("the live config does not carry it: %v", s.deps.Config.Current().Aliases)
	}
}

func TestAliasWriteRejectsAnUnknownProvider(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/aliases", `{"fast":["nosuch/m"]}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestPolicyWriteRefusesARestartOnlyField(t *testing.T) {
	// Same rule and same message as PUT /api/config: one endpoint accepting
	// what the other refuses would be worse than either behaviour.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"first_byte":"30s"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT /api/policy = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestPolicyWriteTakesEffect(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"retry":{"max_attempts":6}}`); w.Code != 200 {
		t.Fatalf("PUT /api/policy = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got != 6 {
		t.Errorf("max_attempts = %d, want 6", got)
	}
}

func TestModelOverrideWriteReadDelete(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")

	path := "/api/models/groq/m/override"
	if w := do(t, s, cookie, token, "PUT", path,
		`{"context_window":128000,"surfaces":["llm"]}`); w.Code != 200 {
		t.Fatalf("PUT %s = %d: %s", path, w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", path, "")
	if w.Code != 200 {
		t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
	}
	var got overrideBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ContextWindow == nil || *got.ContextWindow != 128000 {
		t.Errorf("context_window = %v", got.ContextWindow)
	}

	if w := do(t, s, cookie, token, "DELETE", path, ""); w.Code != 204 {
		t.Fatalf("DELETE %s = %d: %s", path, w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "GET", path, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Errorf("GET after delete = %d %q, want 200 {}", w.Code, w.Body.String())
	}
}

func TestModelOverrideForAnUnknownProviderIs404(t *testing.T) {
	// model_overrides cascades on providers, so an orphan row would be
	// accepted here and vanish later with nothing to explain it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/models/nosuch/m/override",
		`{"context_window":1}`)
	if w.Code != 404 {
		t.Fatalf("PUT = %d, want 404: %s", w.Code, w.Body.String())
	}
}
