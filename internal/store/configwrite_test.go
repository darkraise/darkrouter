package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

func TestWriteConfigStoresAKeyAndReportsIt(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	written, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if len(written) != 1 || written[0] != "log.retention" {
		t.Fatalf("written = %v", written)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if rows["log.retention"] != "96h" {
		t.Errorf("stored row = %q, want 96h", rows["log.retention"])
	}
}

// An absent row is what makes reset-to-default work, so a reset deletes rather
// than writing the current default into the table.
func TestWriteConfigResetDeletesTheRow(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Reset: []string{"log.retention"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["log.retention"]; ok {
		t.Error("the row survived a reset")
	}
}

// The settings table would store "" happily and it would read back as a value
// the operator chose.
func TestWriteConfigTreatsAnEmptyValueAsAReset(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": "https://example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": ""},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["server.public_url"]; ok {
		t.Error("an empty value left a row behind")
	}
}

func TestWriteConfigRefusesABootstrapKeyByName(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.proxy_token": "sekrit"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "DARKROUTER_PROXY_TOKEN") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

func TestWriteConfigRefusesAnUnknownKey(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.nonsense": "1"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
}

// Refused whole. A person is waiting and can be told what is wrong, which is
// the whole difference between this path and the loader's.
func TestWriteConfigCommitsNothingWhenOneKeyIsBad(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{
			"log.retention":     "96h",
			"capture.retention": "not-a-duration",
		},
	})
	if err == nil {
		t.Fatal("WriteConfig accepted an unparseable value")
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a refused write left rows behind: %v", rows)
	}
}

func TestWriteConfigRefusesABrokenCrossKeyRule(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.timeout.total": "5s"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "policy.timeout.total") {
		t.Errorf("the refusal does not name the rule it broke: %v", err)
	}
	// The margin as well as the rule. An operator told only which rule broke
	// has to work out what to set the value to.
	if !strings.Contains(err.Error(), "must be at least connect + first_byte") {
		t.Errorf("the refusal does not say by how much: %v", err)
	}
}

// The bug this phase exists to close. Both writes pass against a snapshot
// taken before either ran; only a base read inside the transaction sees the
// other one land.
func TestConcurrentWritesCannotBreakTheTimeoutBudgetTogether(t *testing.T) {
	db, ctx := migrated(t), context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	patches := []config.Patch{
		{Set: map[string]string{"policy.timeout.total": "70s"}},
		{Set: map[string]string{"policy.timeout.first_byte": "65s"}},
	}
	for i := range patches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = WriteConfig(ctx, db, config.Bootstrap{}, patches[i])
		}(i)
	}
	wg.Wait()

	// Whichever order they ran in, what is in the database now must load.
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	c, _, skipped, err := buildConfig(rows, config.Bootstrap{}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("the committed rows do not load: %v (errs %v)", skipped, errs)
	}
	if c.Policy.Timeout.Total < c.Policy.Timeout.Connect+c.Policy.Timeout.FirstByte {
		t.Errorf("total %s does not cover connect %s plus first_byte %s",
			c.Policy.Timeout.Total, c.Policy.Timeout.Connect, c.Policy.Timeout.FirstByte)
	}
	// One of them had to lose, or the rule was never enforced -- and the loser
	// must have been refused for the rule rather than for a database failure,
	// which is the difference between a 400 an operator can act on and a 500.
	if errs[0] == nil && errs[1] == nil {
		t.Error("both writes committed a pair that together breaks the rule")
	}
	for i, err := range errs {
		if err == nil {
			continue
		}
		var rejected config.RejectedError
		if !errors.As(err, &rejected) {
			t.Errorf("errs[%d] = %v, want a RejectedError", i, err)
		}
	}
}

func TestWriteConfigWritesAliasesInTheSameTransaction(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Aliases: map[string][]string{"fast": {"groq/llama"}},
		Set:     map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatal(err)
	}
	aliases, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases["fast"]) != 1 {
		t.Errorf("aliases = %v", aliases)
	}
}

// A save that breaks nothing must not be refused because of a row that was
// already unusable before it ran.
func TestWriteConfigIgnoresAnUnusableRowItDoesNotTouch(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if err := putSetting(ctx, db.Write, "capture.retention", "not-a-duration"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if rows["log.retention"] != "96h" {
		t.Errorf("the save did not land: %v", rows)
	}
	// Untouched, not repaired. Fixing it is a save that names it.
	if rows["capture.retention"] != "not-a-duration" {
		t.Errorf("the save rewrote a row it was not given: %v", rows)
	}
}

func TestWriteConfigRefusesAKeySetAndResetAtOnce(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set:   map[string]string{"log.retention": "96h"},
		Reset: []string{"log.retention"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
}

// The per-key validator from the registry, on the path a person is waiting on.
func TestWriteConfigRefusesAnOutOfRangeRetryCount(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.retry.max_attempts": "20"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "between 1 and 10") {
		t.Errorf("the refusal does not state the bound: %v", err)
	}
}

// The defect this construction exists to prevent: a save judged against the
// compiled default of the key it is writing, rather than against the value
// that will sit beside it. Both stored timeouts are usable together, and the
// new first_byte is not.
func TestWriteConfigRefusesAValueThatBreaksARuleWithAStoredNeighbour(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{
			"policy.timeout.total":      "40s",
			"policy.timeout.first_byte": "30s",
		},
	}); err != nil {
		t.Fatalf("the starting pair is valid: %v", err)
	}

	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.timeout.first_byte": "35s"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}

	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if rows["policy.timeout.first_byte"] != "30s" {
		t.Errorf("first_byte = %q, want the refused write to have changed nothing", rows["policy.timeout.first_byte"])
	}
	// What the rejection is for: the pair that would have been stored must not
	// be one the next load throws away.
	c, _, skipped, err := buildConfig(rows, config.Bootstrap{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Errorf("the stored rows do not load: %v", skipped)
	}
	if c.Policy.Timeout.Total != 40*time.Second {
		t.Errorf("total = %s, want the stored 40s", c.Policy.Timeout.Total)
	}
}

// The pre-check exists because buildConfig reverts keys until the config
// validates and no key can fix a broken chain, so an unchecked bad set comes
// back as a "compiled defaults do not validate" server fault -- a 500 naming
// nothing an operator can act on, for something they typed and can fix.
func TestWriteConfigRefusesABrokenAliasChain(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Aliases: map[string][]string{"fast": {""}},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "fast") {
		t.Errorf("the refusal does not name the alias: %v", err)
	}
}

// refusalFor matches warnings the loader's way, by prefix, because a stored
// value is quoted into its own warning. Here a row that was already unusable
// quotes the key this save is writing, and a substring match would answer with
// that row's reason rather than the operator's own.
func TestWriteConfigRefusalNamesTheKeyItRefused(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if err := putSetting(ctx, db.Write, "server.max_body_bytes", "policy.retry.max_attempts"); err != nil {
		t.Fatal(err)
	}
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.retry.max_attempts": "20"},
	})
	if err == nil {
		t.Fatal("WriteConfig accepted an out-of-range retry count")
	}
	if !strings.Contains(err.Error(), "between 1 and 10") {
		t.Errorf("the refusal does not give the reason the save was refused: %v", err)
	}
	if strings.Contains(err.Error(), "server.max_body_bytes") {
		t.Errorf("the refusal answers with an untouched row's message: %v", err)
	}
}

// A bare domain is how an operator writes this setting; the registry
// normalises it on the way into the Config, and the row keeps what was typed.
func TestWriteConfigAcceptsABareDomainForThePublicURL(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": "llm.example.test"},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, err := buildConfig(rows, config.Bootstrap{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "https://llm.example.test" {
		t.Errorf("public_url = %q, want the normalised URL", c.Server.PublicURL)
	}
}
