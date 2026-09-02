package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	var env struct {
		Presets []struct {
			ID     string         `json:"id"`
			Config map[string]any `json:"config"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	list := env.Presets
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

func TestPlaygroundPresetRejectsAnUnknownDialect(t *testing.T) {
	// The admin API is operator-facing: a row can be written by hand with
	// curl, not only by the console. dialect-support.ts has no fallback case
	// for an unknown dialect, so a stored value outside the three the console
	// knows would crash the config pane's render the moment it loads.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for _, dialect := range []string{"responses", "OpenAI", "", "chatml"} {
		body := `{"name":"n","dialect":"` + dialect + `","model":"m","config":{}}`
		if w := do(t, s, cookie, token, "POST", "/api/playground/presets", body); w.Code != 400 {
			t.Errorf("dialect %q = %d, want 400", dialect, w.Code)
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

func TestPlaygroundConversationUpdateAndDeleteAnswer404ForAnUnknownID(t *testing.T) {
	// Presets have had this since stage 3; conversations answer 404 in code
	// but nothing held them to it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	body := `{"title":"t","dialect":"openai","model":"m","config":{}}`
	if w := do(t, s, cookie, token, "PATCH", "/api/playground/conversations/nope", body); w.Code != 404 {
		t.Errorf("patch unknown = %d, want 404", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations/nope", ""); w.Code != 404 {
		t.Errorf("delete unknown = %d, want 404", w.Code)
	}
}

func TestPlaygroundConversationRoundTripsThroughTheAPI(t *testing.T) {
	// The whole point of storing the config blob is that a conversation
	// reopened next week still knows the system prompt that shaped it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	create := `{"title":"be brief","dialect":"anthropic","model":"claude",
	            "config":{"system":"answer in one line","fieldFromTheFuture":{"nested":true}}}`
	w := do(t, s, cookie, token, "POST", "/api/playground/conversations", create)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}

	turn := `{"role":"user","content":"hello","request_id":""}`
	if w := do(t, s, cookie, token,
		"POST", "/api/playground/conversations/"+made.ID+"/messages", turn); w.Code != 201 {
		t.Fatalf("append user turn = %d: %s", w.Code, w.Body.String())
	}
	answer := `{"role":"assistant","content":"hi","request_id":"req-1"}`
	if w := do(t, s, cookie, token,
		"POST", "/api/playground/conversations/"+made.ID+"/messages", answer); w.Code != 201 {
		t.Fatalf("append assistant turn = %d: %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "GET", "/api/playground/conversations/"+made.ID, "")
	if w.Code != 200 {
		t.Fatalf("read = %d: %s", w.Code, w.Body.String())
	}
	var read struct {
		Title    string         `json:"title"`
		Dialect  string         `json:"dialect"`
		Config   map[string]any `json:"config"`
		Messages []struct {
			Seq       int    `json:"seq"`
			Role      string `json:"role"`
			Content   string `json:"content"`
			RequestID string `json:"request_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Title != "be brief" || read.Dialect != "anthropic" {
		t.Errorf("columns did not round-trip: %+v", read)
	}
	if read.Config["system"] != "answer in one line" {
		t.Errorf("system prompt lost: %v", read.Config)
	}
	future, ok := read.Config["fieldFromTheFuture"].(map[string]any)
	if !ok || future["nested"] != true {
		t.Errorf("unknown field did not survive: %v", read.Config)
	}
	if len(read.Messages) != 2 {
		t.Fatalf("read %d messages, want 2", len(read.Messages))
	}
	if read.Messages[0].Seq != 0 || read.Messages[0].Role != "user" {
		t.Errorf("first message = %+v", read.Messages[0])
	}
	if read.Messages[1].RequestID != "req-1" {
		t.Errorf("assistant turn lost its request id: %+v", read.Messages[1])
	}
	// A turn stored before the log writer's batch lands has no trace, and the
	// client must be able to tell that apart from a malformed row.
	if read.Messages[0].RequestID != "" {
		t.Errorf("user turn invented a request id: %q", read.Messages[0].RequestID)
	}

	if w := do(t, s, cookie, token, "PATCH", "/api/playground/conversations/"+made.ID,
		`{"title":"renamed","dialect":"openai","model":"gpt","config":{"system":"x"}}`); w.Code != 200 {
		t.Fatalf("patch = %d: %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/playground/conversations", "")
	if w.Code != 200 {
		t.Fatalf("list = %d", w.Code)
	}
	var env struct {
		Conversations []struct {
			Title   string `json:"title"`
			Model   string `json:"model"`
			Preview string `json:"preview"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	list := env.Conversations
	if len(list) != 1 {
		t.Fatalf("listed %d, want 1", len(list))
	}
	if list[0].Title != "renamed" || list[0].Model != "gpt" {
		t.Errorf("patch did not land: %+v", list[0])
	}
	// The rail draws the most recent user turn under each title.
	if list[0].Preview != "hello" {
		t.Errorf("preview = %q, want hello", list[0].Preview)
	}

	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations/"+made.ID, ""); w.Code != 204 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "GET", "/api/playground/conversations/"+made.ID, ""); w.Code != 404 {
		t.Errorf("read after delete = %d, want 404", w.Code)
	}
}

func TestPlaygroundConversationRejectsWhatTheClientCannotRender(t *testing.T) {
	// dialect-support.ts has no fallback case for a dialect outside the three
	// it knows, so a row naming a fourth would crash the pane that loads it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	for _, body := range []string{
		`{"title":"x","dialect":"mistral","model":"m","config":{}}`,
		`{"title":"","dialect":"openai","model":"m","config":{}}`,
		`{"title":"x","dialect":"openai","model":"m","config":[1,2]}`,
	} {
		if w := do(t, s, cookie, token, "POST", "/api/playground/conversations", body); w.Code != 400 {
			t.Errorf("create %s = %d, want 400", body, w.Code)
		}
	}

	w := do(t, s, cookie, token, "POST", "/api/playground/conversations",
		`{"title":"x","dialect":"openai","model":"m","config":{}}`)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/"+made.ID+"/messages",
		`{"role":"system","content":"x"}`); w.Code != 400 {
		t.Errorf("append with an unknown role = %d, want 400", w.Code)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/nosuch/messages",
		`{"role":"user","content":"x"}`); w.Code != 404 {
		t.Errorf("append to a missing conversation = %d, want 404", w.Code)
	}
}

func TestPlaygroundConversationsListNewestFirst(t *testing.T) {
	// The rail's whole premise: the conversation you spoke to last is the one
	// at the top. Both rows are backdated to distinct times because the
	// timestamps are whole seconds -- two rows written in the same second tie,
	// and a tie under ORDER BY leaves the order undefined rather than wrong,
	// which is a test that passes until it does not.
	s, db := testServerFull(t)
	cookie, token := login(t, s)

	ids := map[string]string{}
	for _, title := range []string{"older", "newer"} {
		w := do(t, s, cookie, token, "POST", "/api/playground/conversations",
			`{"title":"`+title+`","dialect":"openai","model":"gpt","config":{}}`)
		if w.Code != 201 {
			t.Fatalf("create %s = %d: %s", title, w.Code, w.Body.String())
		}
		var made struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
			t.Fatal(err)
		}
		ids[title] = made.ID
	}

	// Only updated_at moves. created_at stays where the insert put it because
	// the listing reaps empty conversations by age, and a row backdated past
	// that floor would be deleted before it could be ordered.
	now := time.Now().UTC()
	for title, ago := range map[string]time.Duration{"older": 2 * time.Hour, "newer": time.Hour} {
		if _, err := db.Write.ExecContext(t.Context(),
			`UPDATE playground_conversations SET updated_at = ? WHERE id = ?`,
			now.Add(-ago).Unix(), ids[title]); err != nil {
			t.Fatal(err)
		}
	}

	titles := func() []string {
		w := do(t, s, cookie, token, "GET", "/api/playground/conversations", "")
		if w.Code != 200 {
			t.Fatalf("list = %d: %s", w.Code, w.Body.String())
		}
		var env struct {
			Conversations []struct {
				Title string `json:"title"`
			} `json:"conversations"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		list := env.Conversations
		out := []string{}
		for _, c := range list {
			out = append(out, c.Title)
		}
		return out
	}

	if got := titles(); len(got) != 2 || got[0] != "newer" || got[1] != "older" {
		t.Fatalf("listed %v, want [newer older]", got)
	}

	// A turn on the older conversation moves it to the top, which is the half
	// of the ordering that depends on the append touching updated_at.
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/"+ids["older"]+"/messages",
		`{"role":"user","content":"still here"}`); w.Code != 201 {
		t.Fatalf("append = %d: %s", w.Code, w.Body.String())
	}
	if got := titles(); len(got) != 2 || got[0] != "older" || got[1] != "newer" {
		t.Errorf("after a new turn listed %v, want [older newer]", got)
	}
}

func TestPlaygroundConversationsGateStopsWritesAndNotReads(t *testing.T) {
	// Section 8.2: flipping the key stops the playground keeping anything new.
	// It does not delete what is already there, and an operator who has just
	// turned it off still needs to see and remove that.
	s, db := testServerFullWithConfig(t, "playground:\n  save_conversations: false\n")
	cookie, token := login(t, s)

	existing, err := db.CreatePlaygroundConversation(
		t.Context(), "from before", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	create := `{"title":"x","dialect":"openai","model":"gpt","config":{}}`
	if w := do(t, s, cookie, token, "POST", "/api/playground/conversations", create); w.Code != 403 {
		t.Errorf("create with saving off = %d, want 403", w.Code)
	} else if !strings.Contains(w.Body.String(), "playground.save_conversations") {
		// The status alone would pass against a 403 raised for any other
		// reason -- a CSRF miss, say. The body is what tells the operator
		// which setting to change, and it names the key deliberately.
		t.Errorf("403 body = %s, want it to name the setting", w.Body.String())
	}
	if w := do(t, s, cookie, token, "PATCH",
		"/api/playground/conversations/"+existing.ID, create); w.Code != 403 {
		t.Errorf("patch with saving off = %d, want 403", w.Code)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/"+existing.ID+"/messages",
		`{"role":"user","content":"x"}`); w.Code != 403 {
		t.Errorf("append with saving off = %d, want 403", w.Code)
	}

	if w := do(t, s, cookie, token, "GET", "/api/playground/conversations", ""); w.Code != 200 {
		t.Errorf("list with saving off = %d, want 200", w.Code)
	}
	if w := do(t, s, cookie, token, "GET",
		"/api/playground/conversations/"+existing.ID, ""); w.Code != 200 {
		t.Errorf("read with saving off = %d, want 200", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE",
		"/api/playground/conversations/"+existing.ID, ""); w.Code != 204 {
		t.Errorf("delete with saving off = %d, want 204", w.Code)
	}
	// The purge is the settings screen's answer to "delete what you already
	// kept". Behind the gate it would be unreachable exactly when an operator
	// most wants it.
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations", ""); w.Code != 200 {
		t.Errorf("purge with saving off = %d, want 200", w.Code)
	}
}

func TestPlaygroundConversationsPurgeEmptiesEverything(t *testing.T) {
	// The purge is what the settings screen offers when an operator decides
	// the playground should not have kept their prompts. It must reach the
	// messages too, or it is a lie told with a confirmation dialog.
	s, db := testServerFull(t)
	cookie, token := login(t, s)

	for _, title := range []string{"one", "two"} {
		c, err := db.CreatePlaygroundConversation(
			t.Context(), title, "openai", "gpt", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AppendPlaygroundTurn(t.Context(), c.ID, "user", "hello", ""); err != nil {
			t.Fatal(err)
		}
	}

	w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations", "")
	if w.Code != 200 {
		t.Fatalf("purge = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deleted != 2 {
		t.Errorf("purge reported %d, want 2", out.Deleted)
	}

	for _, table := range []string{"playground_conversations", "playground_messages"} {
		var count int
		if err := db.Read.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows after the purge", table, count)
		}
	}

	// The purge and the single delete share a path prefix. ServeMux prefers
	// the exact literal, and a regression that routed the purge through the
	// wildcard would answer 404 for an id of "" rather than emptying anything.
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations", ""); w.Code != 200 {
		t.Errorf("purge on an empty table = %d, want 200", w.Code)
	}
}
