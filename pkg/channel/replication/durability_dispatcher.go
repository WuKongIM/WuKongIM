package replication

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var errReplicaNeedsRepair = errors.New("channel replication: follower needs repair")

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
	if d == nil || d.peers == nil || d.repairs == nil || complete == nil {
		return ch.ErrInvalidConfig
	}
	request := ReplicateRequest{
		ChannelKey: proposal.channelKey,
		ChannelID:  proposal.channelID,
		Leader:     proposal.leader,
		Follower:   follower,
		Manifest:   proposal.manifest,
		Records:    proposal.records,
		Committed:  proposal.committed,
	}
	return d.peers.submit(ctx, follower, request, func(result ReplicateResult, err error) {
		if err != nil {
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
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: ch.ErrStaleMeta})
		case ReplicateConflict:
			complete(durabilityCompletion{outcome: ch.AppendOutcomeConflict, err: ch.ErrLogConflict})
		case ReplicateBackpressured:
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: ch.ErrBackpressured})
		default:
			complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown})
		}
	})
}
