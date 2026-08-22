package health

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter reads both wire forms RFC 9110 permits: delta-seconds and an
// HTTP-date. Providers use both, and treating a date as unparseable would
// silently downgrade a precise instruction to the generic ladder.
func ParseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return 0, false
	}
	d := t.Sub(now)
	if d < 0 {
		// A date already in the past means retry now, not a negative wait.
		d = 0
	}
	return d, true
}
