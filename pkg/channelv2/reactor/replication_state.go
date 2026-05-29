package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

// replicationState tracks follower pull/apply/ack scheduling owned by one reactor channel.
type replicationState struct {
	// pullInflight records whether one follower pull RPC is currently running.
	pullInflight bool
	// pullOpID fences the currently running pull RPC.
	pullOpID ch.OpID

	// dirty marks the follower as needing immediate replication work.
	dirty bool
	// parked records whether the follower is intentionally waiting for the leader-provided pull delay.
	parked bool
	// nextPullAt is the earliest time a new pull may be submitted.
	nextPullAt time.Time
	// nextPullAfter is the leader-provided delay from the latest empty pull response.
	nextPullAfter time.Duration
	// lastActivityVersion is the latest accepted leader activity version.
	lastActivityVersion uint64
	// hintedLeaderLEO is the highest leader LEO observed in accepted PullHint requests.
	hintedLeaderLEO uint64
	// backoff is the current exponential retry delay.
	backoff time.Duration
	// lastLeaderHW is the leader commit frontier from the latest accepted pull.
	lastLeaderHW uint64
	// lastError keeps the last replication scheduling or worker error for diagnostics.
	lastError error

	// pendingPull is the single unapplied pull response retained across apply backpressure.
	pendingPull *transport.PullResponse
	// applyBlocked records store-apply backpressure or retry delay for pendingPull.
	applyBlocked bool
	// applyRetryAt is the earliest time to resubmit a blocked pendingPull.
	applyRetryAt time.Time
	// applyOpID fences the currently running store-apply task.
	applyOpID ch.OpID
}

// markDirty makes the follower eligible for immediate pull scheduling.
func (s *replicationState) markDirty(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	s.dirty = true
	s.parked = false
	s.nextPullAt = time.Time{}
	s.nextPullAfter = 0
}

// reset clears follower-only replication state when metadata moves away from an active follower role.
func (s *replicationState) reset() {
	*s = replicationState{}
}

// applyPullResult applies only the currently fenced pull result and leaves newer inflight state intact on stale completions.
func (s *replicationState) applyPullResult(result worker.Result, current ch.Fence, _ time.Time) bool {
	if result.Fence.ChannelKey != current.ChannelKey || result.Fence.Generation != current.Generation || result.Fence.Epoch != current.Epoch || result.Fence.LeaderEpoch != current.LeaderEpoch || result.Fence.OpID != current.OpID {
		return false
	}
	if !s.pullInflight || s.pullOpID != current.OpID {
		return false
	}
	s.pullInflight = false
	s.pullOpID = 0
	if result.Err != nil {
		s.lastError = result.Err
		return true
	}
	s.lastError = nil
	return true
}

// nextReplicationBackoff doubles the current delay while respecting configured bounds.
func nextReplicationBackoff(current, minBackoff, maxBackoff time.Duration) time.Duration {
	if minBackoff <= 0 {
		minBackoff = time.Millisecond
	}
	if maxBackoff <= 0 {
		maxBackoff = 100 * time.Millisecond
	}
	if maxBackoff < minBackoff {
		maxBackoff = minBackoff
	}
	if current <= 0 {
		return minBackoff
	}
	next := current * 2
	if next < minBackoff {
		next = minBackoff
	}
	if next > maxBackoff {
		next = maxBackoff
	}
	return next
}
