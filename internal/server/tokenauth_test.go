package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

type countingTokens struct {
	valid   map[string]bool
	checks  int
	listing int
	listErr error
	// revokedAll reports "never issued" even with tokens in valid, to prove
	// a latched answer is not re-read.
	revokedAll bool
}

func (c *countingTokens) ProxyTokenValid(_ context.Context, secret string) (bool, error) {
	c.checks++
	return c.valid[secret], nil
}

func (c *countingTokens) ProxyTokensIssued(context.Context) (bool, error) {
	c.listing++
	if c.listErr != nil {
		return false, c.listErr
	}
	return len(c.valid) > 0 && !c.revokedAll, nil
}

func TestAValidTokenIsCheckedOncePerWindow(t *testing.T) {
	src := &countingTokens{valid: map[string]bool{"good": true}}
	now := time.Now()
	ta := newTokenAuth(src)
	ta.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		if !ta.accept(context.Background(), "good") {
			t.Fatal("a valid token was refused")
		}
	}
	if src.checks != 1 {
		t.Errorf("store checked %d times for 100 requests, want 1", src.checks)
	}
	// Past the window the store is asked again, which is also what records
	// the token's last use and what bounds how long a revoked one survives.
	now = now.Add(tokenCacheTTL + time.Millisecond)
	src.valid["good"] = false
	if ta.accept(context.Background(), "good") {
		t.Error("a revoked token was accepted past the cache window")
	}
	if src.checks != 2 {
		t.Errorf("store checked %d times, want 2", src.checks)
	}
}

func TestAnInvalidTokenIsNotCached(t *testing.T) {
	src := &countingTokens{valid: map[string]bool{}}
	ta := newTokenAuth(src)
	for i := 0; i < 3; i++ {
		if ta.accept(context.Background(), "bad") {
			t.Fatal("an unknown token was accepted")
		}
	}
	if src.checks != 3 {
		t.Errorf("a refusal must be re-checked each time: %d checks", src.checks)
	}
	if ta.accept(context.Background(), "") {
		t.Error("an empty token was accepted")
	}
}

func TestWhetherTokensExistIsCached(t *testing.T) {
	src := &countingTokens{valid: map[string]bool{"good": true}}
	now := time.Now()
	ta := newTokenAuth(src)
	ta.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		if !ta.configured(context.Background()) {
			t.Fatal("tokens exist and were not reported")
		}
	}
	if src.listing != 1 {
		t.Errorf("store listed %d times for 100 requests, want 1", src.listing)
	}
}

func TestAStoreThatCannotAnswerRefusesWithoutLatching(t *testing.T) {
	src := &countingTokens{valid: map[string]bool{}, listErr: errors.New("down")}
	now := time.Now()
	ta := newTokenAuth(src)
	ta.now = func() time.Time { return now }
	if !ta.configured(context.Background()) {
		t.Error("a store that cannot answer must be treated as configured")
	}
	now = now.Add(tokenCacheTTL + time.Millisecond)
	src.listErr = nil
	if ta.configured(context.Background()) {
		t.Error("a recovered store that never issued a token still reads as configured")
	}
}

func TestIssuanceOnceSeenIsNeverForgotten(t *testing.T) {
	src := &countingTokens{valid: map[string]bool{"good": true}}
	now := time.Now()
	ta := newTokenAuth(src)
	ta.now = func() time.Time { return now }
	if !ta.configured(context.Background()) {
		t.Fatal("an issued token was not reported")
	}
	now = now.Add(tokenCacheTTL + time.Millisecond)
	src.revokedAll = true
	if !ta.configured(context.Background()) {
		t.Error("past the cache window, authentication was turned back off")
	}
	if src.listing != 1 {
		t.Errorf("store asked %d times, want 1: issuance is permanent", src.listing)
	}
}
