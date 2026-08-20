package replication

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var errReplicaNeedsRepair = errors.New("channel replication: follower needs repair")

// replicaHedgeDelay bounds how long a quorum round waits for its preferred
// follower before admitting the trailing follower on the foreground path.
const replicaHedgeDelay = 100 * time.Millisecond

type localDurabilitySubmitter interface {
	submitLocal(context.Context, durableProposal, func(durabilityCompletion)) error
}

// followerRepairSink synchronously transfers one exact gap to a lifecycle
// owner. Implementations must coalesce by Channel/follower within fixed
// membership bounds and must not block the peer completion owner.
type followerRepairSink interface {
	RecordFollowerRepair(followerRepair)
}

// batchingDurabilityDispatcher keeps local durability and peer transport as
// separate adapters while presenting one commit-round admission surface.
type batchingDurabilityDispatcher struct {
	ownerContext context.Context
	local        localDurabilitySubmitter
	peers        *peerBatcher
	repairs      followerRepairSink
}

func (d *batchingDurabilityDispatcher) submitLocal(_ context.Context, proposal durableProposal, complete func(durabilityCompletion)) error {
	if d == nil || d.ownerContext == nil || d.local == nil {
		return ch.ErrInvalidConfig
	}
	if err := d.ownerContext.Err(); err != nil {
		return err
	}
	return d.local.submitLocal(d.ownerContext, proposal, complete)
}

func (d *batchingDurabilityDispatcher) submitReplica(ctx context.Context, follower ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	return d.submitReplicaWithMode(ctx, follower, proposal, complete, false, true)
}

func (d *batchingDurabilityDispatcher) replicaHedgeDelay() time.Duration {
	return replicaHedgeDelay
}

func (d *batchingDurabilityDispatcher) submitReplicaHedged(ctx context.Context, follower ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	return d.submitReplicaWithMode(ctx, follower, proposal, complete, false, true)
}

func (d *batchingDurabilityDispatcher) submitReplicaDeferred(ctx context.Context, follower ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	return d.submitReplicaWithMode(ctx, follower, proposal, complete, true, true)
}

func (d *batchingDurabilityDispatcher) submitReplicaWithMode(ctx context.Context, follower ch.NodeID, proposal durableProposal, complete func(durabilityCompletion), deferred bool, repairOnFailure bool) error {
	if d == nil || d.peers == nil || d.repairs == nil || complete == nil {
		return ch.ErrInvalidConfig
	}
	request := ReplicateRequest{
		ChannelKey:                proposal.channelKey,
		ChannelID:                 proposal.channelID,
		Leader:                    proposal.leader,
		Follower:                  follower,
		Manifest:                  proposal.manifest,
		Records:                   proposal.records,
		Committed:                 proposal.committed,
		ServerAllocatedMessageIDs: proposal.serverAllocatedMessageIDs,
	}
	finish := func(result ReplicateResult, err error) {
		if err != nil {
			if repairOnFailure {
				d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
			}
			complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: err})
			return
		}
		switch result.Status {
		case ReplicateDurable:
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		case ReplicateAlreadyDurable:
			complete(durabilityCompletion{outcome: ch.AppendOutcomeAlreadyDurable})
		case ReplicateNeedFrom:
			d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, result.NeedFrom))
			complete(durabilityCompletion{
				outcome: ch.AppendOutcomeDefinitelyNotWritten, err: errReplicaNeedsRepair,
				follower: follower, needFrom: result.NeedFrom,
			})
		case ReplicateStaleFence:
			if repairOnFailure {
				d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
			}
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: ch.ErrStaleMeta})
		case ReplicateConflict:
			if repairOnFailure {
				d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
			}
			complete(durabilityCompletion{outcome: ch.AppendOutcomeConflict, err: ch.ErrLogConflict})
		case ReplicateBackpressured:
			if repairOnFailure {
				d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
			}
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: ch.ErrBackpressured})
		default:
			if repairOnFailure {
				d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
			}
			complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown})
		}
	}
	var err error
	if deferred {
		err = d.peers.submitDeferred(ctx, follower, request, finish)
	} else {
		err = d.peers.submit(ctx, follower, request, finish)
	}
	if err != nil && repairOnFailure {
		d.repairs.RecordFollowerRepair(followerRepairFor(proposal, follower, proposal.first))
	}
	return err
}
