package admin

import (
	"math"
	"net"
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
	// limiterMaxBuckets bounds the address map. Past it, full buckets are
	// dropped; an address that has not tried recently costs nothing to
	// recreate.
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
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLoginLimiter(rate, burst float64, concurrency int) *loginLimiter {
	return &loginLimiter{
		rate: rate, burst: burst,
		sem:     make(chan struct{}, concurrency),
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// take spends one token for addr. When the bucket is empty it reports how
// long until the next token, which is what Retry-After carries.
func (l *loginLimiter) take(addr string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, found := l.buckets[addr]
	if !found {
		if len(l.buckets) >= limiterMaxBuckets {
			l.evictFull(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[addr] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		return false, wait
	}
	b.tokens--
	return true, 0
}

// evictFull drops every bucket that has refilled completely. Called with the
// lock held.
func (l *loginLimiter) evictFull(now time.Time) {
	for addr, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, addr)
		}
	}
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
func clientAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
