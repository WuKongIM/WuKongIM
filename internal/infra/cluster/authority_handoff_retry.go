package cluster

import "time"

// boundedAuthorityHandoffBackoff returns exponential retry delay capped at max.
// Attempt numbers start at one so the first retry uses base unchanged.
func boundedAuthorityHandoffBackoff(base, max time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	if max <= 0 || base >= max {
		return base
	}
	delay := base
	for i := 1; i < attempt && delay < max; i++ {
		if delay > max/2 {
			return max
		}
		delay *= 2
	}
	if delay > max {
		return max
	}
	return delay
}
