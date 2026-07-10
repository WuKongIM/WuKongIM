package cluster

import (
	"context"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// channelDataPlaneLeaseGuard gates local Channel leader appends on recent control visibility.
type channelDataPlaneLeaseGuard struct {
	// now supplies the current time for freshness checks.
	now func() time.Time
	// ttl is the maximum age accepted for the latest successful visibility mark.
	ttl time.Duration

	// lastOK stores the latest successful visibility timestamp, including Go's monotonic clock reading.
	lastOK atomic.Pointer[time.Time]
}

// channelDataPlaneLeaseSnapshot is the package-private management view of the lease guard.
type channelDataPlaneLeaseSnapshot struct {
	// lastVisibleAt is the last successful visibility timestamp.
	lastVisibleAt time.Time
	// ttl is the maximum accepted age for lastVisibleAt.
	ttl time.Duration
	// ready reports whether lastVisibleAt is currently fresh.
	ready bool
}

func newChannelDataPlaneLeaseGuard(now func() time.Time, ttl time.Duration) *channelDataPlaneLeaseGuard {
	if now == nil {
		now = time.Now
	}
	return &channelDataPlaneLeaseGuard{now: now, ttl: ttl}
}

// MarkVisible advances the lease evidence monotonically so delayed reports cannot replace newer successes.
func (g *channelDataPlaneLeaseGuard) MarkVisible(at time.Time) {
	if g == nil {
		return
	}
	next := at
	for {
		current := g.lastOK.Load()
		if current != nil && !next.After(*current) {
			// A wall-clock rollback can leave the prior evidence in the future once
			// monotonic data is unavailable. Replace it with the new successful
			// observation so the guard fails closed without remaining wedged.
			if !current.After(g.now()) {
				return
			}
		}
		if g.lastOK.CompareAndSwap(current, &next) {
			return
		}
	}
}

func (g *channelDataPlaneLeaseGuard) AllowChannelAppend(ctx context.Context, _ ch.AppendAdmissionRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil || !g.fresh(g.now()) {
		return ch.ErrNotReady
	}
	return nil
}

func (g *channelDataPlaneLeaseGuard) snapshot() channelDataPlaneLeaseSnapshot {
	if g == nil {
		return channelDataPlaneLeaseSnapshot{}
	}
	last := g.lastOK.Load()
	if last == nil {
		return channelDataPlaneLeaseSnapshot{ttl: g.ttl}
	}
	return channelDataPlaneLeaseSnapshot{
		lastVisibleAt: *last,
		ttl:           g.ttl,
		ready:         g.freshAt(*last, g.now()),
	}
}

func (g *channelDataPlaneLeaseGuard) fresh(now time.Time) bool {
	if g == nil || g.ttl <= 0 {
		return false
	}
	last := g.lastOK.Load()
	if last == nil {
		return false
	}
	return g.freshAt(*last, now)
}

func (g *channelDataPlaneLeaseGuard) freshAt(last time.Time, now time.Time) bool {
	if g == nil || g.ttl <= 0 {
		return false
	}
	age := now.Sub(last)
	if age < 0 {
		return false
	}
	return age <= g.ttl
}
