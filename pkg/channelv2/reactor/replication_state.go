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

	// ackInflight records whether one follower ACK RPC is currently running.
	ackInflight bool
	// ackOpID fences the currently running ACK RPC.
	ackOpID ch.OpID
	// ackMatch is the exact match offset carried by the inflight ACK.
	ackMatch uint64
	// ackStopped records whether the inflight ACK is a stopped-follower acknowledgement.
	ackStopped bool
	// ackActivityVersion fences a stopped ACK to the accepted leader activity version.
	ackActivityVersion uint64

	// pendingAck means an ACK should be retried before issuing another pull.
	pendingAck bool
	// pendingAckMatch is the stable match offset reused by ACK retries.
	pendingAckMatch uint64
	// pendingAckStopped records whether the pending ACK retry must keep Stopped=true.
	pendingAckStopped bool
	// pendingAckActivityVersion is the activity fence reused by stopped ACK retries.
	pendingAckActivityVersion uint64
	// nextAckAt is the earliest retry time after ACK backpressure or errors.
	nextAckAt time.Time

	// stopping records a follower that accepted leader stop control but has not unloaded yet.
	stopping bool
	// stopActivityVersion fences the checkpoint and stopped ACK for an accepted stop.
	stopActivityVersion uint64
	// checkpointInflight records a pending checkpoint before the stopped ACK.
	checkpointInflight bool
	// checkpointOpID fences the checkpoint worker result.
	checkpointOpID ch.OpID
	// nextCheckpointAt is the earliest checkpoint retry after backpressure or errors.
	nextCheckpointAt time.Time
	// deleteAfterStoppedAck allows runtime deletion after the stopped ACK succeeds.
	deleteAfterStoppedAck bool
	// stopAcked records that the stopped ACK succeeded and only runtime deletion remains.
	stopAcked bool
	// nextStopEvictAt is the earliest retry time when transient work blocks stopped runtime deletion.
	nextStopEvictAt time.Time

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

// applyAckResult applies only the currently fenced ACK result and keeps the failed match offset for retry.
func (s *replicationState) applyAckResult(result worker.Result, current ch.Fence, _ time.Time) bool {
	if result.Fence.ChannelKey != current.ChannelKey || result.Fence.Generation != current.Generation || result.Fence.Epoch != current.Epoch || result.Fence.LeaderEpoch != current.LeaderEpoch || result.Fence.OpID != current.OpID {
		return false
	}
	if !s.ackInflight || s.ackOpID != current.OpID {
		return false
	}
	match := s.ackMatch
	stopped := s.ackStopped
	activityVersion := s.ackActivityVersion
	s.ackInflight = false
	s.ackOpID = 0
	s.ackMatch = 0
	s.ackStopped = false
	s.ackActivityVersion = 0
	if result.Err != nil {
		s.pendingAck = true
		s.pendingAckMatch = match
		s.pendingAckStopped = stopped
		s.pendingAckActivityVersion = activityVersion
		s.lastError = result.Err
		return true
	}
	s.pendingAck = false
	s.pendingAckMatch = 0
	s.pendingAckStopped = false
	s.pendingAckActivityVersion = 0
	s.lastError = nil
	return true
}

// cancelStopping clears a stale follower stop sequence when newer leader activity arrives.
func (s *replicationState) cancelStopping() {
	s.stopping = false
	s.stopActivityVersion = 0
	s.checkpointInflight = false
	s.checkpointOpID = 0
	s.nextCheckpointAt = time.Time{}
	s.deleteAfterStoppedAck = false
	s.stopAcked = false
	s.nextStopEvictAt = time.Time{}
	if s.pendingAckStopped {
		s.pendingAck = false
		s.pendingAckMatch = 0
		s.pendingAckStopped = false
		s.pendingAckActivityVersion = 0
		s.nextAckAt = time.Time{}
	}
	if s.ackStopped {
		s.ackInflight = false
		s.ackOpID = 0
		s.ackMatch = 0
		s.ackStopped = false
		s.ackActivityVersion = 0
	}
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
