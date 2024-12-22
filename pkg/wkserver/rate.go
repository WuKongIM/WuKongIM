package wkserver

import (
	"math"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// RateLimiter is the struct used to keep tracking consumed memory size.
type RateLimiter struct {
	size    uint64
	maxSize uint64
	wklog.Log
}

// NewRateLimiter creates and returns a rate limiter instance.
func NewRateLimiter(max uint64) *RateLimiter {
	return &RateLimiter{
		maxSize: max,
		Log:     wklog.NewWKLog("rateLimiter"),
	}
}

// Enabled returns a boolean flag indicating whether the rate limiter is
// enabled.
func (r *RateLimiter) Enabled() bool {
	return r.maxSize > 0 && r.maxSize != math.MaxUint64
}

// Increase increases the recorded in memory log size by sz bytes.
func (r *RateLimiter) Increase(sz uint64) {
	atomic.AddUint64(&r.size, sz)
}

// Decrease decreases the recorded in memory log size by sz bytes.
func (r *RateLimiter) Decrease(sz uint64) {
	atomic.AddUint64(&r.size, ^(sz - 1))
}

// Set sets the recorded in memory log size to sz bytes.
func (r *RateLimiter) Set(sz uint64) {
	atomic.StoreUint64(&r.size, sz)
}

// Get returns the recorded in memory log size.
func (r *RateLimiter) Get() uint64 {
	return atomic.LoadUint64(&r.size)
}

// RateLimited returns a boolean flag indicating whether the node is rate
// limited.
func (r *RateLimiter) RateLimited() bool {
	if !r.Enabled() {
		return false
	}
	v := r.Get()
	if v > r.maxSize {
		r.Info("rate limited", zap.Uint64("v", v), zap.Uint64("maxSize", r.maxSize))
		return true
	}
	return false
}
