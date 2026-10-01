package replication

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type storageFundingDispatcher interface {
	fundStorage(context.Context, Authority, durableProposal) error
}

// runtimeStorageFunding uses the existing bounded peer owner and the caller's
// owned append worker. It creates no per-Channel goroutine or durability vote.
type runtimeStorageFunding struct {
	store   storageFundingStore
	peers   *peerBatcher
	timeout time.Duration
	repairs followerRepairAuthorityOwner
}

func (f *runtimeStorageFunding) fundStorage(ctx context.Context, a Authority, p durableProposal) error {
	protected, err := f.store.storageProtected(ctx, LoadRequest{ChannelKey: p.channelKey, ChannelID: p.channelID}, p.committed)
	if err != nil || !protected {
		return err
	}
	mutation := Mutation{ChannelKey: p.channelKey, ChannelID: p.channelID, Manifest: p.manifest, Records: p.records, Committed: p.committed, Class: MutationClassLeaderQuorum, ServerAllocatedMessageIDs: p.serverAllocatedMessageIDs}
	local, err := f.store.prepareStorage(ctx, mutation, 0, false)
	nonce := local.Nonce
	participants := append(append(make([]ch.NodeID, 0, len(a.Voters)+len(a.Learners)), a.Voters...), a.Learners...)
	success := false
	// No original dispatcher is entered before this returns success. A separate
	// bounded cancel persists nonce floors even after caller cancellation.
	defer func() {
		if !success && nonce != 0 {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), f.timeout)
			defer cancel()
			_, _ = f.store.prepareStorage(cancelCtx, mutation, nonce, true)
			for _, node := range participants {
				if node != a.Leader {
					_, _ = f.exchange(cancelCtx, node, p, nonce, true)
				}
			}
		}
	}()
	if err != nil {
		return err
	}
	if !local.Prepared || nonce == 0 {
		return ch.ErrLogConflict
	}
	for _, node := range participants {
		if node == a.Leader {
			continue
		}
		out, err := f.exchange(ctx, node, p, nonce, false)
		if err != nil {
			return err
		}
		if !out.Prepared {
			if out.NeedFrom > 0 {
				f.repairs.RecordFollowerRepair(followerRepairFor(p, node, out.NeedFrom))
				return errors.Join(ch.ErrNotReady, errReplicaNeedsRepair)
			}
			return ch.ErrBackpressured
		}
	}
	success = true
	return nil
}
func (f *runtimeStorageFunding) exchange(ctx context.Context, node ch.NodeID, p durableProposal, nonce uint64, cancel bool) (StorageFundingResult, error) {
	request := ReplicateRequest{ChannelKey: p.channelKey, ChannelID: p.channelID, Leader: p.leader, Follower: node, Manifest: p.manifest, Records: p.records, Committed: p.committed, ServerAllocatedMessageIDs: p.serverAllocatedMessageIDs}
	type completion struct {
		out StorageFundingResult
		err error
	}
	results := make(chan completion, 1)
	if err := f.peers.submitFunding(ctx, node, request, nonce, cancel, func(out StorageFundingResult, err error) { results <- completion{out, err} }); err != nil {
		return StorageFundingResult{}, err
	}
	select {
	case result := <-results:
		return result.out, result.err
	case <-ctx.Done():
		return StorageFundingResult{}, ctx.Err()
	}
}
