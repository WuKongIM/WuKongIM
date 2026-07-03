package replica

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const fenceAndDrainPollInterval = time.Millisecond

// FenceAndDrain fail-closes append admission and returns a stable drain proof.
func (r *replica) FenceAndDrain(ctx context.Context, req channel.FenceAndDrainRequest) (channel.DrainResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		result := r.submitLoopCommand(ctx, machineFenceAndDrainCommand{Request: req})
		if result.Err == nil {
			if result.Drain == nil {
				return channel.DrainResult{}, channel.ErrInvalidArgument
			}
			return *result.Drain, nil
		}
		if !errors.Is(result.Err, channel.ErrNotReady) {
			return channel.DrainResult{}, result.Err
		}
		timer := time.NewTimer(fenceAndDrainPollInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return channel.DrainResult{}, ctx.Err()
		}
	}
}

func (r *replica) applyFenceAndDrainCommand(cmd machineFenceAndDrainCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	req := cmd.Request
	if req.ChannelKey == "" || req.ChannelKey != r.state.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if req.ExpectedLeader == 0 || req.ExpectedLeader != r.state.Leader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if req.ExpectedChannelEpoch != r.state.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if req.ExpectedLeaderEpoch != r.meta.LeaderEpoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if req.WriteFenceToken == "" || req.WriteFenceVersion == 0 {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if !r.drainRequestFenceAcceptedLocked(req) {
		return machineResult{Err: channel.ErrStaleMeta}
	}

	// The request carries an authoritative migration fence. If local metadata
	// has not observed it yet, the loop-owned drained marker still fail-closes
	// future appends before returning the proof.
	if len(r.appendPending) > 0 {
		r.failPendingAppendRequestsLocked(channel.ErrWriteFenced)
	}
	if r.hasUnsettledDrainWorkLocked() {
		return machineResult{Err: channel.ErrNotReady}
	}
	r.drainedFence = drainedFenceState{
		token:   req.WriteFenceToken,
		version: req.WriteFenceVersion,
		active:  true,
	}
	drain := channel.DrainResult{
		ChannelKey:        r.state.ChannelKey,
		LEO:               r.state.LEO,
		HW:                r.state.HW,
		CheckpointHW:      r.state.CheckpointHW,
		ChannelEpoch:      r.state.Epoch,
		LeaderEpoch:       r.meta.LeaderEpoch,
		WriteFenceVersion: req.WriteFenceVersion,
	}
	return machineResult{Drain: &drain}
}

func (r *replica) drainRequestFenceAcceptedLocked(req channel.FenceAndDrainRequest) bool {
	if r.meta.WriteFence.Token == req.WriteFenceToken && r.meta.WriteFence.Version == req.WriteFenceVersion {
		return true
	}
	if r.meta.WriteFence.Token == req.WriteFenceToken {
		return r.meta.WriteFence.Version < req.WriteFenceVersion
	}
	if r.meta.WriteFence.Token != "" {
		return false
	}
	return r.meta.WriteFence.Version < req.WriteFenceVersion
}

func (r *replica) drainedFenceBlocksAppendLocked() bool {
	return r.drainedFence.active && r.meta.WriteFence.Version <= r.drainedFence.version
}

func (r *replica) hasUnsettledDrainWorkLocked() bool {
	return len(r.appendInFlightIDs) > 0 ||
		len(r.waiters) > 0 ||
		r.checkpointQueued ||
		r.checkpointInFlight ||
		r.pendingReconcileEffectID != 0 ||
		r.state.LEO > r.state.HW ||
		r.state.CheckpointHW < r.state.HW
}
