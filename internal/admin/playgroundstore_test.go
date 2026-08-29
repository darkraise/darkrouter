package admin

import (
	"encoding/json"
	"testing"
)

func TestPlaygroundPresetBlobIsOpaque(t *testing.T) {
	// A field the console learned before this binary did must survive a save.
	// Decoding into a struct of today's fields and re-marshalling would drop
	// it silently, which is the lossy preset the design forbids.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	body := `{"name":"terse","dialect":"anthropic","model":"claude",
	          "config":{"system":"be brief","fieldFromTheFuture":{"nested":true}}}`
	if w := do(t, s, cookie, token, "POST", "/api/playground/presets", body); w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", "/api/playground/presets", "")
	if w.Code != 200 {
		t.Fatalf("list = %d", w.Code)
	}
	var list []struct {
		ID     string         `json:"id"`
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d, want 1", len(list))
	}
	future, ok := list[0].Config["fieldFromTheFuture"].(map[string]any)
	if !ok || future["nested"] != true {
		t.Errorf("unknown field did not survive: %v", list[0].Config)
	}
}

func TestPlaygroundPresetNameClashOffersTheExistingRow(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	first := `{"name":"terse","dialect":"openai","model":"gpt","config":{}}`

	w := do(t, s, cookie, token, "POST", "/api/playground/presets", first)
	if w.Code != 201 {
		t.Fatalf("first create = %d", w.Code)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}

	w = do(t, s, cookie, token, "POST", "/api/playground/presets", first)
	if w.Code != 409 {
		t.Fatalf("clash = %d, want 409", w.Code)
	}
	var clash struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &clash); err != nil {
		t.Fatal(err)
	}
	if clash.ID != made.ID {
		t.Errorf("clash id = %q, want %q", clash.ID, made.ID)
	}
	if clash.Error == "" {
		t.Error("clash carried no message")
	}
}

func TestPlaygroundPresetRejectsABlobThatIsNotAnObject(t *testing.T) {
	// The blob is stored unparsed, so this is the only place its shape is
	// checked. A bare array or string would reach the client as a config it
	// cannot merge.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for _, cfg := range []string{`[1,2]`, `"text"`, `7`, `null`} {
		body := `{"name":"n","dialect":"openai","model":"m","config":` + cfg + `}`
		if w := do(t, s, cookie, token, "POST", "/api/playground/presets", body); w.Code != 400 {
			t.Errorf("config %s = %d, want 400", cfg, w.Code)
		}
	}
}

func TestPlaygroundPresetUpdateAndDeleteAnswer404ForAnUnknownID(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	body := `{"name":"n","dialect":"openai","model":"m","config":{}}`
	if w := do(t, s, cookie, token, "PATCH", "/api/playground/presets/nope", body); w.Code != 404 {
		t.Errorf("patch unknown = %d, want 404", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/presets/nope", ""); w.Code != 404 {
		t.Errorf("delete unknown = %d, want 404", w.Code)
	}
}
