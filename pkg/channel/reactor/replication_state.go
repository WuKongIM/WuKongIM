package reactor

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"net"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

// replicationState tracks follower pull/apply scheduling owned by one reactor channel.
type replicationState struct {
	// pullInflight records whether one follower pull RPC is currently running.
	pullInflight bool
	// pullOpID fences the currently running pull RPC.
	pullOpID ch.OpID
	// pullCancel releases the current pull RPC timeout context.
	pullCancel context.CancelFunc
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
	// recoveryProbeWindow is the deterministic anti-entropy window retained across ordinary PullHint activity.
	recoveryProbeWindow time.Duration
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

// pendingMetaState is a reactor-owned follower bootstrap shell awaiting authoritative metadata.
type pendingMetaState struct {
	key             ch.ChannelKey
	id              ch.ChannelID
	generation      uint64
	epoch           uint64
	leaderEpoch     uint64
	leader          ch.NodeID
	leaderLEO       uint64
	activityVersion uint64
	deadline        time.Time
	initial         storeInitialState

	pullInflight           bool
	pullOpID               ch.OpID
	pullSubmittedAt        time.Time
	pullSubmittedAckOffset uint64
	retryCount             int
	lastError              error
}

type storeInitialState struct {
	LEO                         uint64
	HW                          uint64
	CheckpointHW                uint64
	LocalRetentionThroughSeq    uint64
	PhysicalRetentionThroughSeq uint64
}

func (p *pendingMetaState) fence() ch.Fence {
	if p == nil {
		return ch.Fence{}
	}
	return ch.Fence{ChannelKey: p.key, Generation: p.generation, Epoch: p.epoch, LeaderEpoch: p.leaderEpoch, OpID: p.pullOpID}
}

func (p *pendingMetaState) sameFence(req transport.PullHintRequest) bool {
	return p != nil &&
		req.ChannelKey == p.key &&
		req.ChannelID == p.id &&
		req.Epoch == p.epoch &&
		req.LeaderEpoch == p.leaderEpoch &&
		req.Leader == p.leader
}

func (p *pendingMetaState) compareFence(req transport.PullHintRequest) int {
	if p == nil {
		return 1
	}
	if req.Epoch < p.epoch || (req.Epoch == p.epoch && req.LeaderEpoch < p.leaderEpoch) {
		return -1
	}
	if req.Epoch == p.epoch && req.LeaderEpoch == p.leaderEpoch {
		if req.ChannelKey != p.key || req.ChannelID != p.id || req.Leader != p.leader {
			return -1
		}
		return 0
	}
	return 1
}

func nonRetryablePendingPullError(err error) bool {
	return errors.Is(err, ch.ErrNotReady) ||
		errors.Is(err, ch.ErrNotLeader) ||
		errors.Is(err, ch.ErrStaleMeta) ||
		errors.Is(err, ch.ErrChannelNotFound) ||
		errors.Is(err, ch.ErrNotReplica) ||
		errors.Is(err, ch.ErrBackpressured)
}

func retryablePendingPullError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ch.ErrBackpressured) {
		return true
	}
	if netErr, ok := err.(net.Error); ok && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	return false
}

func contextDoneError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
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
		initialWindow := followerRecoveryProbeInitialWindow(interval, jitter)
		if s.recoveryProbeWindow < initialWindow {
			s.recoveryProbeWindow = initialWindow
		}
		s.recoveryProbe = true
		s.nextPullAt = now.Add(followerRecoveryProbeBackoffDelay(key, interval, s.recoveryProbeWindow))
	}
}

// advanceRecoveryProbeBackoff widens the next caught-up anti-entropy window without weakening the initial lost-hint bound.
func (s *replicationState) advanceRecoveryProbeBackoff(interval time.Duration, jitter time.Duration) {
	initialWindow := followerRecoveryProbeInitialWindow(interval, jitter)
	if initialWindow <= 0 {
		return
	}
	if s.recoveryProbeWindow < initialWindow {
		s.recoveryProbeWindow = initialWindow
	}
	maxWindow := max(defaultFollowerRecoveryProbeMaxWindow, initialWindow)
	if s.recoveryProbeWindow >= maxWindow {
		s.recoveryProbeWindow = maxWindow
		return
	}
	if s.recoveryProbeWindow > maxWindow/2 {
		s.recoveryProbeWindow = maxWindow
		return
	}
	s.recoveryProbeWindow *= 2
}

// resetRecoveryProbeBackoff restores the initial recovery cadence after anti-entropy finds missed data.
func (s *replicationState) resetRecoveryProbeBackoff() {
	s.recoveryProbeWindow = 0
}

// clearParkedForHint makes a parked follower eligible for immediate PullHint-driven work.
func (s *replicationState) clearParkedForHint(now time.Time) {
	s.markDirty(now)
	s.recoveryProbe = false
}

// reset clears follower-only replication state when metadata moves away from an active follower role.
func (s *replicationState) reset() {
	s.cancelPull()
	*s = replicationState{}
}

func (s *replicationState) cancelPull() {
	if s.pullCancel != nil {
		s.pullCancel()
		s.pullCancel = nil
	}
}

// applyPullResult applies only the currently fenced pull result and leaves newer inflight state intact on stale completions.
func (s *replicationState) applyPullResult(result worker.Result, current ch.Fence, _ time.Time) bool {
	if result.Fence.ChannelKey != current.ChannelKey || result.Fence.Generation != current.Generation || result.Fence.Epoch != current.Epoch || result.Fence.LeaderEpoch != current.LeaderEpoch || result.Fence.OpID != current.OpID {
		return false
	}
	if !s.pullInflight || s.pullOpID != current.OpID {
		return false
	}
	s.cancelPull()
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

func followerRecoveryProbeInitialWindow(interval time.Duration, jitter time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	if jitter <= 0 {
		return interval
	}
	if interval > time.Duration(math.MaxInt64)-jitter {
		return time.Duration(math.MaxInt64)
	}
	return interval + jitter
}

// followerRecoveryProbeBackoffDelay deterministically spreads one idle follower across the upper half of its current window.
func followerRecoveryProbeBackoffDelay(key ch.ChannelKey, interval time.Duration, window time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	if window < interval {
		window = interval
	}
	floor := max(interval, window/2)
	return followerRecoveryProbeDelay(key, floor, window-floor)
}

// committedCheckpointDelay spreads one bounded checkpoint deadline by channel key.
func committedCheckpointDelay(key ch.ChannelKey, interval time.Duration) time.Duration {
	return deterministicUpperHalfDelay(key, interval, 0)
}

// replicationRPCPressureDelay spreads a deferred follower Pull across the
// upper half of its configured recovery-jitter window.
func replicationRPCPressureDelay(key ch.ChannelKey, interval time.Duration) time.Duration {
	return deterministicUpperHalfDelay(key, interval, 1)
}

// pullHintRPCPressureDelay spreads one follower's deferred PullHint across the
// upper half of the configured retry window.
func pullHintRPCPressureDelay(key ch.ChannelKey, node ch.NodeID, interval time.Duration) time.Duration {
	return deterministicUpperHalfDelay(key, interval, uint64(node)+2)
}

func storeApplyPressureDelay(key ch.ChannelKey, maxBackoff time.Duration) time.Duration {
	if maxBackoff <= 0 {
		maxBackoff = 100 * time.Millisecond
	}
	return deterministicUpperHalfDelay(key, maxBackoff, 0x6170706c79)
}

func deterministicUpperHalfDelay(key ch.ChannelKey, interval time.Duration, salt uint64) time.Duration {
	if interval <= 0 {
		return 0
	}
	minDelay := interval/2 + interval%2
	spread := interval - minDelay
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	var encodedSalt [8]byte
	binary.LittleEndian.PutUint64(encodedSalt[:], salt)
	_, _ = h.Write(encodedSalt[:])
	extra := time.Duration(h.Sum64() % uint64(spread+1))
	return minDelay + extra
}
