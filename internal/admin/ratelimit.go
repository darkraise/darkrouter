package admin

import (
	"container/list"
	"math"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	// loginRate is sustained attempts per second per address: five a minute.
	loginRate = 5.0 / 60
	// loginBurst is how many attempts an address may make at once before the
	// rate applies. Ten covers an operator who mistypes a few times.
	loginBurst = 10
	// loginConcurrency caps bcrypt verifications in flight across every
	// address. Each costs a quarter of a core for a quarter of a second, so
	// a flood from many addresses would otherwise be a CPU denial of service
	// long before any bucket empties.
	loginConcurrency = 4
	// limiterMaxBuckets bounds the address map. Past it, the address seen
	// least recently is dropped: one that has not tried recently costs little
	// to recreate, and an address can only be pushed out by this many others
	// arriving after it — a sender with that many addresses already has that
	// many buckets.
	limiterMaxBuckets = 4096
)

// loginLimiter is a token bucket per client address plus a global
// concurrency cap. Hand-rolled: the whole of it is thirty lines, and a
// dependency for that is one more thing to audit.
type loginLimiter struct {
	rate  float64
	burst float64
	sem   chan struct{}
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*list.Element
	// recency orders the buckets most recently seen first, so making room is
	// one removal rather than a scan of every address under the lock.
	recency *list.List
}

type bucket struct {
	addr   string
	tokens float64
	last   time.Time
}

func newLoginLimiter(rate, burst float64, concurrency int) *loginLimiter {
	return &loginLimiter{
		rate: rate, burst: burst,
		sem:     make(chan struct{}, concurrency),
		now:     time.Now,
		buckets: map[string]*list.Element{},
		recency: list.New(),
	}
}

// localPrefixFactor is how many times one address's allowance a whole local
// /64 gets. Its addresses are keyed one by one, so a device mistyping its
// password does not lock out its neighbours; a host there can still give
// itself any number of addresses, and this is all that rotating them buys.
const localPrefixFactor = 10

// allow spends one attempt for the client at remoteAddr: from its own bucket
// and, on a local IPv6 network, from its /64's shared one as well. Neither is
// spent unless both have a token.
func (l *loginLimiter) allow(remoteAddr string) (ok bool, retryAfter time.Duration) {
	key, prefix := clientKeys(remoteAddr)
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refill(key, l.burst, l.rate, now)
	if b.tokens < 1 {
		return false, wait(b, l.rate)
	}
	if prefix != "" {
		rate := l.rate * localPrefixFactor
		p := l.refill("net "+prefix, l.burst*localPrefixFactor, rate, now)
		if p.tokens < 1 {
			return false, wait(p, rate)
		}
		p.tokens--
	}
	b.tokens--
	return true, 0
}

// take spends one token for addr. When the bucket is empty it reports how
// long until the next token, which is what Retry-After carries.
func (l *loginLimiter) take(addr string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refill(addr, l.burst, l.rate, now)
	if b.tokens < 1 {
		return false, wait(b, l.rate)
	}
	b.tokens--
	return true, 0
}

// refill finds or makes key's bucket and tops it up for the time since it was
// last seen. The caller holds l.mu.
func (l *loginLimiter) refill(key string, burst, rate float64, now time.Time) *bucket {
	el, found := l.buckets[key]
	if found {
		l.recency.MoveToFront(el)
	} else {
		if len(l.buckets) >= limiterMaxBuckets {
			oldest := l.recency.Back()
			l.recency.Remove(oldest)
			delete(l.buckets, oldest.Value.(*bucket).addr)
		}
		el = l.recency.PushFront(&bucket{addr: key, tokens: burst, last: now})
		l.buckets[key] = el
	}
	b := el.Value.(*bucket)
	b.tokens = math.Min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	return b
}

func wait(b *bucket, rate float64) time.Duration {
	return time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// acquire claims one of the global verification slots without waiting. The
// release is returned rather than exposed as a method so a caller cannot
// release a slot it never took.
func (l *loginLimiter) acquire() (release func(), ok bool) {
	select {
	case l.sem <- struct{}{}:
		return func() { <-l.sem }, true
	default:
		return nil, false
	}
}

// clientAddr is the address a bucket is keyed on: the peer's IP without its
// port, so one client's connections share a bucket. The forwarded header is
// deliberately not consulted; a client that can set it could choose its own
// bucket.
//
// Global IPv6 is keyed by its /64: that is the smallest allocation a client is
// handed, so per address a single sender would hold 2^64 buckets. Unique local
// and link-local addresses are the exception. Those prefixes are the operator's
// own network, where SLAAC puts every device in one /64, and a /64 bucket would
// let one mistyping device lock out the whole LAN. A link-local address keeps
// its zone: the same address on two links is two hosts, and the zone is the
// receiving interface, which the sender cannot choose.
func clientAddr(remoteAddr string) string {
	key, _ := clientKeys(remoteAddr)
	return key
}

// clientKeys is clientAddr plus, for a unique local or link-local IPv6
// address, the /64 it shares with its neighbours (with a link-local zone
// kept). The prefix is empty for everything else.
func clientKeys(remoteAddr string) (key, localPrefix string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr, ""
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host, ""
	}
	zone := addr.Zone()
	plain := addr.WithZone("")
	if !plain.Is6() || plain.Is4In6() {
		return plain.String(), ""
	}
	prefix := netip.PrefixFrom(plain, 64).Masked().String()
	switch {
	case addr.IsLinkLocalUnicast():
		if zone != "" {
			prefix += "%" + zone
		}
		return addr.String(), prefix
	case plain.IsPrivate():
		return plain.String(), prefix
	}
	return prefix, ""
}
