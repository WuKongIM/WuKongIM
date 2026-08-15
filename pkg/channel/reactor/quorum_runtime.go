package reactor

import (
	"context"
	"slices"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

func (r *Reactor) requiresQuorumInstall(state *machine.ChannelState) bool {
	return r != nil && r.cfg.QuorumLog != nil && state != nil && state.Role == ch.RoleLeader &&
		(state.Status == ch.StatusActive || state.Status == ch.StatusCreating)
}

// quorumInstallRequired reports whether a loaded state must prove recovery
// and a current-authority barrier before append admission can open.
func (r *Reactor) quorumInstallRequired(rc *runtimeChannel) bool {
	return rc != nil && r.requiresQuorumInstall(rc.state)
}

func (r *Reactor) quorumMetaWouldFence(rc *runtimeChannel, meta ch.Meta) bool {
	if r == nil || r.cfg.QuorumLog == nil || rc == nil || rc.state == nil || meta.Leader != r.cfg.LocalNode {
		return false
	}
	authority, err := quorumAuthorityFromMeta(meta)
	if err != nil {
		return true
	}
	if rc.quorumInstall != nil {
		return !sameQuorumAuthority(rc.quorumInstall.authority, authority)
	}
	return rc.quorumAuthority.ID != (replication.AuthorityID{}) && !sameQuorumAuthority(rc.quorumAuthority, authority)
}

func quorumAuthorityFromMeta(meta ch.Meta) (replication.Authority, error) {
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if meta.Key == "" || meta.ID.ID == "" || meta.Epoch == 0 || meta.LeaderEpoch == 0 ||
		meta.RouteGeneration == 0 || meta.Leader == 0 || meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return replication.Authority{}, ch.ErrInvalidConfig
	}
	leaderVoter := false
	seen := make(map[ch.NodeID]struct{}, len(meta.ISR))
	for _, voter := range meta.ISR {
		if voter == 0 {
			return replication.Authority{}, ch.ErrInvalidConfig
		}
		if _, exists := seen[voter]; exists {
			return replication.Authority{}, ch.ErrInvalidConfig
		}
		seen[voter] = struct{}{}
		leaderVoter = leaderVoter || voter == meta.Leader
	}
	if !leaderVoter {
		return replication.Authority{}, ch.ErrInvalidConfig
	}
	return replication.Authority{
		Key: meta.Key, ChannelID: meta.ID,
		ID:     replication.AuthorityID{ChannelEpoch: meta.Epoch, LeaderTerm: meta.LeaderEpoch, FenceVersion: meta.RouteGeneration},
		Leader: meta.Leader, Voters: append([]ch.NodeID(nil), meta.ISR...), WriteQuorum: meta.MinISR, WriteFence: meta.WriteFence,
	}, nil
}

func sameQuorumAuthority(left, right replication.Authority) bool {
	return left.Key == right.Key && left.ChannelID == right.ChannelID && left.ID == right.ID &&
		left.Leader == right.Leader && left.WriteQuorum == right.WriteQuorum && left.WriteFence == right.WriteFence &&
		slices.Equal(left.Voters, right.Voters)
}

func (r *Reactor) startQuorumInstall(rc *runtimeChannel, meta ch.Meta, futures []*Future) error {
	if !r.quorumInstallRequired(rc) {
		return ch.ErrInvalidConfig
	}
	authority, err := quorumAuthorityFromMeta(meta)
	if err != nil {
		return err
	}
	if rc.quorumInstall != nil {
		if sameQuorumAuthority(rc.quorumInstall.authority, authority) {
			rc.quorumInstall.futures = append(rc.quorumInstall.futures, futures...)
			return nil
		}
		r.completeFutures(rc.quorumInstall.futures, Result{Err: ch.ErrStaleMeta})
		rc.quorumInstall = nil
	}
	if sameQuorumAuthority(rc.quorumAuthority, authority) {
		rc.state.CommitReady = !authority.WriteFence.Set()
		r.completeFutures(futures, Result{})
		return nil
	}
	opID := r.nextOpID()
	fence := ch.Fence{
		ChannelKey: rc.state.Key, Generation: rc.state.Generation,
		Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID,
	}
	rc.quorumInstall = &quorumInstallState{opID: opID, authority: authority, futures: append([]*Future(nil), futures...)}
	if err := r.submitQuorumInstall(context.Background(), fence, authority); err != nil {
		rc.quorumInstall = nil
		return err
	}
	return nil
}

func (r *Reactor) handleQuorumInstallResult(result worker.Result) {
	rc := r.channels[result.Fence.ChannelKey]
	if rc == nil || rc.state == nil || rc.quorumInstall == nil {
		return
	}
	pending := rc.quorumInstall
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch ||
		result.Fence.LeaderEpoch != rc.state.LeaderEpoch || result.Fence.OpID != pending.opID {
		return
	}
	err := result.Err
	if err == nil {
		if result.QuorumInstall == nil || result.QuorumInstall.Installed.Authority != pending.authority.ID ||
			result.QuorumInstall.Installed.HW > result.QuorumInstall.Installed.LEO {
			err = ch.ErrLogConflict
		}
	}
	rc.quorumInstall = nil
	if err != nil {
		rc.state.CommitReady = false
		r.completeFutures(pending.futures, Result{Err: err})
		return
	}
	installed := result.QuorumInstall.Installed
	rc.quorumAuthority = pending.authority
	rc.state.LEO = installed.LEO
	rc.state.HW = installed.HW
	rc.state.CheckpointHW = max(rc.state.CheckpointHW, installed.HW)
	rc.state.Progress[r.cfg.LocalNode] = machine.ReplicaProgress{Match: installed.LEO}
	rc.state.CommitReady = !pending.authority.WriteFence.Set()
	rc.lifecycle.version = max(rc.lifecycle.version, installed.LEO)
	r.scheduleLifecycleFromState(rc, time.Now())
	r.completeFutures(pending.futures, Result{})
}
