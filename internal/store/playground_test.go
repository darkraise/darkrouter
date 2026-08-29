package store

import (
	"context"
	"encoding/json"
	"testing"
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
