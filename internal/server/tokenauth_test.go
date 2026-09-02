package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

type countingTokens struct {
	valid   map[string]bool
	checks  int
	listing int
	listErr error
}

func (c *countingTokens) ProxyTokenValid(_ context.Context, secret string) (bool, error) {
	c.checks++
	return c.valid[secret], nil
}

func (c *countingTokens) ProxyTokens(context.Context) ([]store.ProxyToken, error) {
	c.listing++
	if c.listErr != nil {
		return nil, c.listErr
	}
	out := make([]store.ProxyToken, 0, len(c.valid))
	for range c.valid {
		out = append(out, store.ProxyToken{})
	}
	return out, nil
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
	now = now.Add(tokenCacheTTL + time.Millisecond)
	src.listErr = errors.New("down")
	if !ta.configured(context.Background()) {
		t.Error("a store that cannot answer must be treated as configured")
	}
}
