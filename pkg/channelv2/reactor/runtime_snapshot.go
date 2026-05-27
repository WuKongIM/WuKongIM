package reactor

import (
	"sort"
	"time"
)

func runtimeViewFromChannel(rc *runtimeChannel, now time.Time, appendFence AppendFenceView) RuntimeView {
	if rc == nil {
		return RuntimeView{AppendFence: appendFence}
	}
	view := RuntimeView{
		AppendFence: appendFence,
		PendingWork: pendingWorkViewFromChannel(rc),
	}
	if rc.state == nil {
		return view
	}
	view.Key = rc.state.Key
	view.Role = rc.state.Role
	view.Status = rc.state.Status
	view.LEO = rc.state.LEO
	view.HW = rc.state.HW
	view.ActivityVersion = rc.lifecycle.ActivityVersion
	view.IdleSince = leaderIdleSince(rc)
	for node, follower := range rc.followers {
		if follower == nil {
			continue
		}
		view.Followers = append(view.Followers, FollowerView{
			Node:           node,
			Match:          follower.Match,
			Stopped:        follower.Stopped,
			StopAckVersion: follower.StopAckVersion,
			StopOffered:    follower.StopOffered,
		})
	}
	sort.Slice(view.Followers, func(i, j int) bool { return view.Followers[i].Node < view.Followers[j].Node })
	return view
}

func pendingWorkViewFromChannel(rc *runtimeChannel) PendingWorkView {
	if rc == nil {
		return PendingWorkView{}
	}
	replication := rc.replication
	view := PendingWorkView{
		Waiters:              len(rc.waiters),
		PullWaiters:          len(rc.pullWaiters),
		AppendQueued:         len(rc.appendQ.pending),
		AppendQueueBlocked:   rc.appendQ.storeBlocked,
		AppendInflight:       rc.appendInflight != nil,
		AppendStoreBlocked:   rc.appendStoreBlocked,
		AppendRetryScheduled: !rc.appendRetryAt.IsZero(),
		AppendCancelContexts: len(rc.appendCancelContexts),
		AppendTimings:        len(rc.appendTimings),
		PullInflight:         replication.pullInflight,
		AckInflight:          replication.ackInflight,
		PendingAck:           replication.pendingAck,
		PendingPull:          replication.pendingPull != nil,
		ApplyBlocked:         replication.applyBlocked,
		ApplyInflight:        replication.applyOpID != 0,
		CheckpointInflight:   replication.checkpointInflight || replication.checkpointOpID != 0,
		CheckpointRetry:      !replication.nextCheckpointAt.IsZero(),
		AckRetry:             !replication.nextAckAt.IsZero(),
		LifecycleCheckpoint:  rc.lifecycle.CheckpointInflight || rc.lifecycle.CheckpointOpID != 0,
		LifecycleRetry:       !rc.lifecycle.CheckpointRetryAt.IsZero(),
	}
	if rc.state != nil {
		view.MachineAppendPending = rc.state.InflightAppend != nil || len(rc.state.PendingAppends) != 0
	}
	return view
}
