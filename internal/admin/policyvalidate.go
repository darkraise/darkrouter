package admin

import (
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// maxRetryAttempts bounds policy.retry.max_attempts. Past ten, a failing
// request walks the whole candidate list several times over and a client
// waits minutes for an error it could have had in seconds.
const maxRetryAttempts = 10

// validatePolicy checks a merged policy the way the file loader checks a
// loaded one, plus the bounds the file loader leaves to the operator. It runs
// before the write, so a bad value is refused rather than stored and then
// refused by the next reload — which would leave the previous configuration
// serving with an error nobody sees until they open the settings screen.
func validatePolicy(p config.PolicyConfig) error {
	if p.Cooldown.TripAfter != nil && *p.Cooldown.TripAfter < 1 {
		return fmt.Errorf("policy.cooldown.trip_after must be at least 1")
	}
	if p.Retry.MaxAttempts < 1 || p.Retry.MaxAttempts > maxRetryAttempts {
		return fmt.Errorf("policy.retry.max_attempts must be between 1 and %d", maxRetryAttempts)
	}
	for _, d := range []struct {
		name string
		val  time.Duration
	}{
		{"policy.cooldown.max", p.Cooldown.Max},
		{"policy.timeout.connect", p.Timeout.Connect},
		{"policy.timeout.first_byte", p.Timeout.FirstByte},
		{"policy.timeout.total", p.Timeout.Total},
		{"policy.timeout.idle", p.Timeout.Idle},
	} {
		if d.val <= 0 {
			return fmt.Errorf("%s must be positive", d.name)
		}
	}
	if p.Timeout.Total < p.Timeout.Connect+p.Timeout.FirstByte {
		return fmt.Errorf("policy.timeout.total (%s) must cover connect plus first_byte (%s)",
			p.Timeout.Total, p.Timeout.Connect+p.Timeout.FirstByte)
	}
	return nil
}
