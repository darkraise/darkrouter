package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

func TestHealthRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	cooling := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)

	in := []health.Entry{
		{Key: health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"},
			CoolingUntil: cooling, BackoffLevel: 2, ConsecutiveFailures: 5},
		{Key: health.Key{ProviderID: "groq", KeyID: "k1"},
			BackoffLevel: 1, ConsecutiveFailures: 3},
	}
	if err := db.SaveHealth(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}

	byKey := map[health.Key]health.Entry{}
	for _, e := range out {
		byKey[e.Key] = e
	}
	got := byKey[health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"}]
	if !got.CoolingUntil.Equal(cooling) {
		t.Errorf("CoolingUntil = %s, want %s", got.CoolingUntil, cooling)
	}
	if got.BackoffLevel != 2 || got.ConsecutiveFailures != 5 {
		t.Errorf("entry = %+v", got)
	}

	// The credential-level entry has no cooldown; it must come back as a zero
	// time rather than the Unix epoch, or rehydration treats it as
	// expired-long-ago and the distinction stops being visible.
	cred := byKey[health.Key{ProviderID: "groq", KeyID: "k1"}]
	if !cred.CoolingUntil.IsZero() {
		t.Errorf("CoolingUntil = %s, want the zero time", cred.CoolingUntil)
	}
}

func TestSaveHealthReplacesPreviousState(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
		{Key: health.Key{ProviderID: "b", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// Provider b's breaker closed, so it is absent from the second snapshot.
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 2},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Key.ProviderID != "a" || out[0].ConsecutiveFailures != 2 {
		t.Fatalf("got %+v, want only provider a at 2 failures", out)
	}
}

func TestSaveHealthWithNoEntriesClearsTheTable(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHealth(ctx, nil); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("got %+v, want none", out)
	}
}
