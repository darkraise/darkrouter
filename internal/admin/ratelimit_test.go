package admin

import (
	"fmt"
	"testing"
	"time"
)

// Eviction dropped only buckets that had refilled completely and then inserted
// regardless, so addresses arriving faster than buckets refill — an IPv6 range
// makes that free — grew the map without bound.
func TestTheLimiterHoldsAtMostItsBucketBound(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newLoginLimiter(loginRate, loginBurst, loginConcurrency)
	l.now = func() time.Time { return now }

	for i := 0; i < 2*limiterMaxBuckets; i++ {
		l.take(fmt.Sprintf("2001:db8::%x", i))
	}
	if n := len(l.buckets); n > limiterMaxBuckets {
		t.Errorf("buckets = %d, want at most %d", n, limiterMaxBuckets)
	}
}

// Making room must not hand an address that is actively being refused a fresh
// bucket: the least recently seen address goes first.
func TestMakingRoomKeepsAnActiveAddressLimited(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newLoginLimiter(loginRate, loginBurst, loginConcurrency)
	l.now = func() time.Time { return now }

	for i := 0; i < loginBurst; i++ {
		l.take("198.51.100.7")
	}
	for i := 0; len(l.buckets) < limiterMaxBuckets; i++ {
		l.take(fmt.Sprintf("2001:db8::%x", i))
	}
	if ok, _ := l.take("198.51.100.7"); ok {
		t.Fatal("the drained address was allowed before any eviction")
	}
	l.take("203.0.113.1")

	if ok, _ := l.take("198.51.100.7"); ok {
		t.Error("the drained address got a fresh bucket when room was made for a new one")
	}
}
