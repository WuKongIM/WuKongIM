package reactor

import (
	"hash/fnv"
	"math"
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
	// pullSubmittedAt records when the current pull RPC was submitted.
	pullSubmittedAt time.Time
	// pullSubmittedAckOffset is the AckOffset carried by the current pull RPC.
	pullSubmittedAckOffset uint64

	// dirty marks the follower as needing immediate replication work.
	dirty bool
	// parked records whether the follower is intentionally waiting for the leader-provided pull delay.
	parked bool
	// recoveryProbe records whether nextPullAt is a parked-follower recovery probe.
	recoveryProbe bool
	// recoveryProbeInflight records whether the current pull RPC is a recovery probe.
	recoveryProbeInflight bool
	// nextPullAt is the earliest time a new pull may be submitted.
	nextPullAt time.Time
	// nextPullAfter is the leader-provided delay from the latest empty pull response.
	nextPullAfter time.Duration
	// lastActivityVersion is the latest accepted leader activity version.
	lastActivityVersion uint64
	// hintedLeaderLEO is the highest leader LEO observed in accepted PullHint requests.
	hintedLeaderLEO uint64
	// hintedAt records when the current highest PullHint leader LEO was accepted.
	hintedAt time.Time
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
	// applySubmittedAt records when the current store-apply task was submitted.
	applySubmittedAt time.Time
	// ackReturnStartedAt records when a successful apply started waiting for an AckOffset return to the leader.
	ackReturnStartedAt time.Time
	// ackReturnOffset is the follower LEO that must be carried back to the leader.
	ackReturnOffset uint64
}

// markDirty makes the follower eligible for immediate pull scheduling.
func (s *replicationState) markDirty(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	s.dirty = true
	s.parked = false
	s.recoveryProbe = false
	s.nextPullAt = time.Time{}
	s.nextPullAfter = 0
}

// parkWithRecovery parks the follower and schedules a deterministic recovery probe when enabled.
func (s *replicationState) parkWithRecovery(key ch.ChannelKey, now time.Time, interval time.Duration, jitter time.Duration) {
	s.dirty = false
	s.parked = true
	s.nextPullAfter = 0
	s.recoveryProbe = false
	s.nextPullAt = time.Time{}
	if interval > 0 {
		s.recoveryProbe = true
		s.nextPullAt = now.Add(followerRecoveryProbeDelay(key, interval, jitter))
	}
}

// clearParkedForHint makes a parked follower eligible for immediate PullHint-driven work.
func (s *replicationState) clearParkedForHint(now time.Time) {
	s.markDirty(now)
	s.recoveryProbe = false
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

func followerRecoveryProbeDelay(key ch.ChannelKey, interval time.Duration, jitter time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	if jitter <= 0 {
		return interval
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	extra := time.Duration(h.Sum64() % uint64(jitter+1))
	if interval > time.Duration(math.MaxInt64)-extra {
		return time.Duration(math.MaxInt64)
	}
	return interval + extra
}
