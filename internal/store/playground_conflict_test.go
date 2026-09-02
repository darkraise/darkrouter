package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPresetNamesConflictWithTheExistingID(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	cfg := json.RawMessage(`{}`)
	first, err := db.CreatePlaygroundPreset(ctx, "taken", "openai", "m", cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.CreatePlaygroundPreset(ctx, "taken", "openai", "m", cfg)
	var conflict *PresetNameConflict
	if !errors.As(err, &conflict) || conflict.ExistingID != first.ID {
		t.Fatalf("second create err = %v, want PresetNameConflict naming %s", err, first.ID)
	}
	if !errors.Is(err, ErrConflict) {
		t.Error("PresetNameConflict does not match ErrConflict")
	}

	other, err := db.CreatePlaygroundPreset(ctx, "other", "openai", "m", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdatePlaygroundPreset(ctx, other.ID, "taken", "openai", "m", cfg); !errors.Is(err, ErrConflict) {
		t.Errorf("rename onto a taken name err = %v, want ErrConflict", err)
	}
}
