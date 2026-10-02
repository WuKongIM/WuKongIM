package replication

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// CommittedReplicaRefresher requests bounded native tail replay to propagate a
// sequencer-proven commit without waiting for another append. It takes no caller
// HW and returns no durable receipt; existing repair admission may coalesce work.
type CommittedReplicaRefresher interface {
	RequestCommittedReplicaRefresh(context.Context, Authority) error
}

func (l *quorumLog) RequestCommittedReplicaRefresh(ctx context.Context, a Authority) error {
	if l == nil || ctx == nil || !validAuthority(a) || a.Leader != l.cfg.Local {
		return ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := l.existingChannel(a.Key)
	if state == nil {
		return ch.ErrNotReady
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.released || !state.ready {
		return ch.ErrNotReady
	}
	if !sameAuthority(state.authority, a) {
		return ch.ErrStaleMeta
	}
	// Propagating already-proven progress is recovery, not a business append.
	// sameAuthority above still requires the exact installed write fence.
	if state.hw == 0 {
		return nil
	}
	manifest := state.frontier.Manifest
	if !manifest.StructurallyValid() || manifest.LastOffset != state.hw || !frontierUsesAuthority(state.frontier, a.ID) {
		return ch.ErrLogConflict
	}
	sink := l.cfg.RepairAuthorities
	if sink == nil {
		return ch.ErrInvalidConfig
	}
	for _, voter := range state.authority.Voters {
		if voter == l.cfg.Local {
			continue
		}
		sink.RecordFollowerRepair(followerRepair{channelKey: a.Key, channelID: a.ChannelID, leader: a.Leader, follower: voter, manifest: manifest, needFrom: manifest.BaseOffset + 1, committed: state.hw})
	}
	return nil
}

var _ CommittedReplicaRefresher = (*quorumLog)(nil)
