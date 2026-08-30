package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPlaygroundPresetRoundTripsItsBlobUntouched(t *testing.T) {
	// The blob is the operator's saved request. A store that reshaped it
	// would make every preset quietly lossy.
	ctx := context.Background()
	db := migrated(t)

	blob := json.RawMessage(`{"system":"be brief","topK":"40","unknownFutureField":7}`)
	got, err := db.CreatePlaygroundPreset(ctx, "terse", "anthropic", "claude", blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("no id assigned")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps not set")
	}

	list, err := db.PlaygroundPresets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d presets, want 1", len(list))
	}
	if string(list[0].Config) != string(blob) {
		t.Errorf("config = %s, want %s", list[0].Config, blob)
	}
	if list[0].Name != "terse" || list[0].Dialect != "anthropic" || list[0].Model != "claude" {
		t.Errorf("columns did not round-trip: %+v", list[0])
	}
}

func TestPlaygroundPresetReportsADuplicateNameRatherThanFailing(t *testing.T) {
	// The save dialog offers to overwrite, so it needs the clashing preset's
	// id -- a bare error would leave it nothing to PATCH.
	ctx := context.Background()
	db := migrated(t)

	first, err := db.CreatePlaygroundPreset(ctx, "terse", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	found, ok, err := db.PlaygroundPresetByName(ctx, "terse")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("existing preset not found by name")
	}
	if found.ID != first.ID {
		t.Errorf("found id %s, want %s", found.ID, first.ID)
	}

	if _, _, err := db.PlaygroundPresetByName(ctx, "nothing-saved-here"); err != nil {
		t.Errorf("absent name errored: %v", err)
	}
	if _, ok, _ := db.PlaygroundPresetByName(ctx, "nothing-saved-here"); ok {
		t.Error("absent name reported as found")
	}
}

func TestPlaygroundPresetUpdatesAndDeletesReportWhetherARowMoved(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)

	made, err := db.CreatePlaygroundPreset(ctx, "terse", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	ok, err := db.UpdatePlaygroundPreset(ctx, made.ID, "verbose", "gemini", "flash",
		json.RawMessage(`{"system":"explain"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("update reported no row")
	}
	list, _ := db.PlaygroundPresets(ctx)
	if list[0].Name != "verbose" || list[0].Dialect != "gemini" || list[0].Model != "flash" {
		t.Errorf("update did not land: %+v", list[0])
	}
	if string(list[0].Config) != `{"system":"explain"}` {
		t.Errorf("config = %s", list[0].Config)
	}

	// An unknown id is not an error, it is a 404 the caller must be able to
	// tell apart from a successful write.
	if ok, err := db.UpdatePlaygroundPreset(ctx, "nope", "x", "openai", "m", json.RawMessage(`{}`)); err != nil || ok {
		t.Errorf("update of unknown id = %v, %v; want false, nil", ok, err)
	}
	if ok, err := db.DeletePlaygroundPreset(ctx, made.ID); err != nil || !ok {
		t.Errorf("delete = %v, %v; want true, nil", ok, err)
	}
	if ok, _ := db.DeletePlaygroundPreset(ctx, made.ID); ok {
		t.Error("second delete reported a row")
	}
}

func TestPlaygroundPresetStoresTheExactBytesItWasGiven(t *testing.T) {
	// The sibling round-trip test uses a blob whose keys are already in
	// marshal order and whose scalars are already canonical, so a store that
	// decoded into a map and re-marshalled would reproduce it and pass. This
	// blob cannot survive that: the keys are unsorted, the whitespace is
	// irregular, the seed is wider than a float64 can hold without rounding,
	// and 1.50 renormalises to 1.5.
	ctx := context.Background()
	db := migrated(t)

	blob := json.RawMessage("{\"zeta\": 1,\n  \"alpha\":  {\"b\":2,\"a\":3},\n" +
		"\"seed\":12345678901234567890,\"temperature\":1.50,\"system\":\"caf\\u00e9\"}")

	made, err := db.CreatePlaygroundPreset(ctx, "exact", "openai", "gpt", blob)
	if err != nil {
		t.Fatal(err)
	}
	if string(made.Config) != string(blob) {
		t.Errorf("returned config = %s, want %s", made.Config, blob)
	}

	// Read the column itself, not just the value the store hands back: this is
	// what proves nothing reshapes the JSON on the way into SQLite.
	var raw string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT config FROM playground_presets WHERE id = ?`, made.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != string(blob) {
		t.Errorf("stored column = %s, want %s", raw, blob)
	}

	list, err := db.PlaygroundPresets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(list[0].Config) != string(blob) {
		t.Errorf("listed config = %s, want %s", list[0].Config, blob)
	}
	byName, ok, err := db.PlaygroundPresetByName(ctx, "exact")
	if err != nil || !ok {
		t.Fatalf("lookup by name = %v, %v", ok, err)
	}
	if string(byName.Config) != string(blob) {
		t.Errorf("looked-up config = %s, want %s", byName.Config, blob)
	}

	updated := json.RawMessage("{\"zeta\":0,\"alpha\":\t9,\"seed\":98765432109876543210}")
	if ok, err := db.UpdatePlaygroundPreset(ctx, made.ID, "exact", "openai", "gpt", updated); err != nil || !ok {
		t.Fatalf("update = %v, %v", ok, err)
	}
	after, _, err := db.PlaygroundPresetByName(ctx, "exact")
	if err != nil {
		t.Fatal(err)
	}
	if string(after.Config) != string(updated) {
		t.Errorf("updated config = %s, want %s", after.Config, updated)
	}
}

func TestPlaygroundConversationRoundTripsItsBlobUntouched(t *testing.T) {
	// A conversation reopened next week has to restore the system prompt that
	// produced its transcript, so the blob is stored exactly as it arrived --
	// unknown fields included.
	ctx := context.Background()
	db := migrated(t)

	blob := json.RawMessage(`{"system":"be brief","topK":"40","unknownFutureField":7}`)
	made, err := db.CreatePlaygroundConversation(ctx, "New chat", "anthropic", "claude", blob)
	if err != nil {
		t.Fatal(err)
	}
	if made.ID == "" {
		t.Fatal("no id assigned")
	}

	got, turns, found, err := db.PlaygroundConversationByID(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("conversation not found after create")
	}
	if len(turns) != 0 {
		t.Errorf("new conversation has %d turns, want 0", len(turns))
	}
	if string(got.Config) != string(blob) {
		t.Errorf("config = %s, want %s", got.Config, blob)
	}
	if got.Title != "New chat" || got.Dialect != "anthropic" || got.Model != "claude" {
		t.Errorf("columns did not round-trip: %+v", got)
	}
}

func TestPlaygroundTurnsTakeTheNextSeqAndBumpTheConversation(t *testing.T) {
	// seq is what orders a transcript, and the unique index means the store
	// has to hand out the next one rather than the caller guessing it.
	ctx := context.Background()
	db := migrated(t)

	c, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	second, err := db.AppendPlaygroundTurn(ctx, c.ID, "assistant", "hi", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != 1 {
		t.Errorf("second turn seq = %d, want 1", second.Seq)
	}

	_, turns, _, err := db.PlaygroundConversationByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("read %d turns, want 2", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Content != "hello" {
		t.Errorf("first turn = %+v", turns[0])
	}
	// A turn stored before the log writer's batch lands has no trace, and the
	// UI has to treat that as ordinary rather than as a missing row.
	if turns[0].RequestID != "" {
		t.Errorf("first turn request id = %q, want empty", turns[0].RequestID)
	}
	if turns[1].RequestID != "req-1" {
		t.Errorf("second turn request id = %q, want req-1", turns[1].RequestID)
	}

	list, err := db.PlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d conversations, want 1", len(list))
	}
	// The rail shows the most recent user turn under the title.
	if list[0].Preview != "hello" {
		t.Errorf("preview = %q, want hello", list[0].Preview)
	}
	// Backdated first, because the bump is what orders the history rail and
	// both writes otherwise land in the same whole second -- an assertion
	// that compares them as they stand can never see the touch happen. Both
	// columns move, because the touch overwrites updated_at with the current
	// second and only an older created_at leaves the bump visible.
	backdated := time.Now().UTC().Add(-time.Hour).Unix()
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE playground_conversations SET created_at = ?, updated_at = ? WHERE id = ?`,
		backdated, backdated, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "again", ""); err != nil {
		t.Fatal(err)
	}
	list, err = db.PlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].UpdatedAt.After(list[0].CreatedAt) {
		t.Errorf("appending did not move updated_at %v past created_at %v",
			list[0].UpdatedAt, list[0].CreatedAt)
	}
	if list[0].Preview != "again" {
		t.Errorf("preview = %q, want the most recent user turn", list[0].Preview)
	}
}

func TestPlaygroundConversationDeleteTakesItsTurnsWithIt(t *testing.T) {
	// ON DELETE CASCADE only fires because PRAGMA foreign_keys is in the DSN.
	// A test that only checked the parent row would pass with the pragma off
	// and leave orphaned prompt text in the database.
	ctx := context.Background()
	db := migrated(t)

	c, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	removed, err := db.DeletePlaygroundConversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("delete reported no row")
	}

	var orphans int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM playground_messages`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d messages survived their conversation", orphans)
	}
}

func TestPlaygroundConversationPurgeEmptiesBothTables(t *testing.T) {
	// The purge is what the settings screen offers when an operator decides
	// the playground should not have kept their prompts. Leaving messages
	// behind would make it a lie.
	ctx := context.Background()
	db := migrated(t)

	for _, title := range []string{"one", "two"} {
		c, err := db.CreatePlaygroundConversation(ctx, title, "openai", "gpt", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.PurgePlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d conversations, want 2", n)
	}
	for _, table := range []string{"playground_conversations", "playground_messages"} {
		var count int
		if err := db.Read.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows", table, count)
		}
	}
}

func TestPlaygroundReapSparesAConversationStillBeingWritten(t *testing.T) {
	// The reap exists for a client that created a conversation and died before
	// its first turn. A conversation created a moment ago is the ordinary case
	// and reaping it would delete the one the operator is typing into.
	ctx := context.Background()
	db := migrated(t)

	fresh, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := db.CreatePlaygroundConversation(ctx, "has a turn", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, kept.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}

	n, err := db.ReapEmptyPlaygroundConversations(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("reaped %d conversations, want 0", n)
	}

	n, err = db.ReapEmptyPlaygroundConversations(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reaped %d conversations, want 1", n)
	}
	if _, _, found, err := db.PlaygroundConversationByID(ctx, fresh.ID); err != nil || found {
		t.Errorf("empty conversation survived the reap (found=%v, err=%v)", found, err)
	}
	if _, _, found, err := db.PlaygroundConversationByID(ctx, kept.ID); err != nil || !found {
		t.Errorf("conversation with a turn was reaped (found=%v, err=%v)", found, err)
	}
}
