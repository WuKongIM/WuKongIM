package cluster

import (
	"context"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// channelDataPlaneLeaseGuard gates local ChannelV2 leader appends on recent control visibility.
type channelDataPlaneLeaseGuard struct {
	// now supplies the current time for freshness checks.
	now func() time.Time
	// ttl is the maximum age accepted for the latest successful visibility mark.
	ttl time.Duration

	// lastOKNanos stores the last successful visibility timestamp as Unix nanoseconds.
	lastOKNanos atomic.Int64
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

func (g *channelDataPlaneLeaseGuard) MarkVisible(at time.Time) {
	if g == nil {
		return
	}
	g.lastOKNanos.Store(at.UnixNano())
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
	last := g.lastOKNanos.Load()
	if last == 0 {
		return channelDataPlaneLeaseSnapshot{ttl: g.ttl}
	}
	lastVisibleAt := time.Unix(0, last).UTC()
	return channelDataPlaneLeaseSnapshot{
		lastVisibleAt: lastVisibleAt,
		ttl:           g.ttl,
		ready:         g.freshAt(last, g.now()),
	}
}

func (g *channelDataPlaneLeaseGuard) fresh(now time.Time) bool {
	if g == nil || g.ttl <= 0 {
		return false
	}
	return g.freshAt(g.lastOKNanos.Load(), now)
}

func (g *channelDataPlaneLeaseGuard) freshAt(last int64, now time.Time) bool {
	if g == nil || g.ttl <= 0 {
		return false
	}
	if last == 0 {
		return false
	}
	return now.Sub(time.Unix(0, last)) <= g.ttl
}
