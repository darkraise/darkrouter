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

func TestAnIPv6SenderSharesOneBucketPerSlash64(t *testing.T) {
	// One allocation hands a client a whole /64, so a bucket per address is no
	// limit at all. IPv4, and IPv4 carried in IPv6, stay per address.
	if a, b := clientAddr("[2001:db8:1:2::1]:4000"),
		clientAddr("[2001:db8:1:2:ffff:ffff:ffff:fffe]:4001"); a != b {
		t.Errorf("one /64 keyed %q and %q; want one bucket", a, b)
	}
	distinct := [][2]string{
		{"[2001:db8:1:2::1]:4000", "[2001:db8:1:3::1]:4000"},
		{"192.0.2.1:4000", "192.0.2.2:4000"},
		{"[::ffff:192.0.2.1]:4000", "[::ffff:192.0.2.2]:4000"},
	}
	for _, pair := range distinct {
		if a, b := clientAddr(pair[0]), clientAddr(pair[1]); a == b {
			t.Errorf("%s and %s share the bucket %q; want one each", pair[0], pair[1], a)
		}
	}
}

func TestALANsIPv6DevicesDoNotShareABucket(t *testing.T) {
	// SLAAC puts every device on a LAN in one /64. On a unique local or
	// link-local prefix that /64 is the operator's own network, not one
	// sender's allocation, so one mistyping laptop would lock out the rest.
	distinct := [][2]string{
		{"[fd12:3456:789a:1::10]:4000", "[fd12:3456:789a:1::11]:4000"},
		{"[fc00:1:2:3::10]:4000", "[fc00:1:2:3::11]:4000"},
		{"[fe80::1c2d:3e4f:5a6b:7c8d]:4000", "[fe80::9a8b:7c6d:5e4f:3a2b]:4000"},
		// The same link-local address on two links is two hosts.
		{"[fe80::1%eth0]:4000", "[fe80::1%eth1]:4000"},
	}
	for _, pair := range distinct {
		if a, b := clientAddr(pair[0]), clientAddr(pair[1]); a == b {
			t.Errorf("%s and %s share the bucket %q; want one each", pair[0], pair[1], a)
		}
	}
	if a, b := clientAddr("[fd12:3456:789a:1::10]:4000"), clientAddr("[fd12:3456:789a:1::10]:5000"); a != b {
		t.Errorf("one address keyed %q and %q; want one bucket", a, b)
	}
}
