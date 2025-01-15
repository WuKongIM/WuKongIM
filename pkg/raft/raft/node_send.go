package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

func (n *Node) sendNotifySync(to uint64) {
	if to == All {
		for _, replicaId := range n.cfg.Replicas {
			if replicaId == n.opts.NodeId {
				continue
			}
			// 解除挂起
			syncInfo := n.replicaSync[replicaId]
			if syncInfo != nil {
				syncInfo.emptySyncTick = 0
				syncInfo.suspend = false
			}
			n.events = append(n.events, types.Event{
				Type: types.NotifySync,
				From: n.opts.NodeId,
				To:   replicaId,
				Term: n.cfg.Term,
			})
		}
	} else {
		// 解除挂起
		syncInfo := n.replicaSync[to]
		if syncInfo != nil {
			syncInfo.emptySyncTick = 0
			syncInfo.suspend = false
		}
		n.events = append(n.events, types.Event{
			Type: types.NotifySync,
			From: n.opts.NodeId,
			To:   to,
			Term: n.cfg.Term,
		})
	}

}

func (n *Node) sendSyncReq() {

	if n.truncating || n.syncing { // 如果在截断中，则发起同步没用
		return
	}
	n.syncing = true
	n.syncElapsed = 0
	var reason types.Reason
	if n.onlySync {
		reason = types.ReasonOnlySync
	}
	n.events = append(n.events, types.Event{
		Type:        types.SyncReq,
		From:        n.opts.NodeId,
		To:          n.cfg.Leader,
		Term:        n.cfg.Term,
		StoredIndex: n.queue.storedIndex + 1,
		Index:       n.queue.lastLogIndex + 1,
		LastLogTerm: n.lastTermStartIndex.Term,
		Reason:      reason,
	})
}

func (n *Node) sendVoteReq(to uint64) {
	n.events = append(n.events, types.Event{
		Type: types.VoteReq,
		From: n.opts.NodeId,
		To:   to,
		Term: n.cfg.Term,
		Logs: []types.Log{
			{
				Term:  n.lastTermStartIndex.Term,
				Index: n.queue.lastLogIndex,
			},
		},
	})
}

func (n *Node) sendVoteResp(to uint64, reason types.Reason) {
	n.events = append(n.events, types.Event{
		Type:   types.VoteResp,
		From:   n.opts.NodeId,
		To:     to,
		Term:   n.cfg.Term,
		Reason: reason,
		Index:  n.queue.storedIndex,
	})
}

func (n *Node) sendPing(to uint64) {
	if !n.IsLeader() {
		return
	}
	if to != All {
		n.events = append(n.events, types.Event{
			Type:           types.Ping,
			From:           n.opts.NodeId,
			To:             to,
			Term:           n.cfg.Term,
			Index:          n.queue.lastLogIndex,
			CommittedIndex: n.queue.committedIndex,
			ConfigVersion:  n.cfg.Version,
		})
		return
	}
	for _, replicaId := range n.cfg.Replicas {
		if replicaId == n.opts.NodeId {
			continue
		}
		n.events = append(n.events, types.Event{
			Type:  types.Ping,
			From:  n.opts.NodeId,
			To:    replicaId,
			Term:  n.cfg.Term,
			Index: n.queue.lastLogIndex,
		})
	}
	if len(n.cfg.Learners) > 0 {
		for _, replicaId := range n.cfg.Learners {
			if replicaId == n.opts.NodeId {
				continue
			}
			n.events = append(n.events, types.Event{
				Type:  types.Ping,
				From:  n.opts.NodeId,
				To:    replicaId,
				Term:  n.cfg.Term,
				Index: n.queue.lastLogIndex,
			})
		}
	}
}

func (n *Node) sendStoreReq(logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) {
	e := types.Event{
		Type: types.StoreReq,
		Term: n.cfg.Term,
		To:   types.LocalNode,
		Logs: logs,
	}

	if termStartIndexInfo != nil {
		e.TermStartIndexInfo = termStartIndexInfo.Clone()
	}
	n.events = append(n.events, e)
}

func (n *Node) sendApplyReq(start, end uint64) {
	n.events = append(n.events, types.Event{
		Type:       types.ApplyReq,
		From:       n.opts.NodeId,
		To:         types.LocalNode,
		StartIndex: start,
		EndIndex:   end,
	})
}

func (n *Node) sendGetLogsReq(syncEvent types.Event) {
	n.events = append(n.events, types.Event{
		Type:        types.GetLogsReq,
		From:        syncEvent.From,
		To:          types.LocalNode,
		Term:        syncEvent.Term,
		Index:       syncEvent.Index,
		StoredIndex: n.queue.storedIndex,
		LastLogTerm: syncEvent.LastLogTerm,
		Reason:      syncEvent.Reason,
	})
}

func (n *Node) sendSyncResp(to uint64, syncIndex uint64, logs []types.Log, reson types.Reason, speed types.Speed) {
	n.events = append(n.events, types.Event{
		Type:           types.SyncResp,
		From:           n.opts.NodeId,
		To:             to,
		Term:           n.cfg.Term,
		Index:          syncIndex,
		Logs:           logs,
		CommittedIndex: n.queue.committedIndex,
		Reason:         reson,
		Speed:          speed,
	})
}

func (n *Node) sendTruncateReq(index uint64) {
	n.events = append(n.events, types.Event{
		Type:        types.TruncateReq,
		To:          types.LocalNode,
		LastLogTerm: n.lastTermStartIndex.Term,
		Index:       index,
	})
}

func (n *Node) sendLearnerToLeaderReq(from uint64) {

	n.events = append(n.events, types.Event{
		Type: types.LearnerToLeaderReq,
		From: from,
	})
}

func (n *Node) sendLearnerToFollowerReq(from uint64) {
	n.events = append(n.events, types.Event{
		Type: types.LearnerToFollowerReq,
		From: from,
	})
}

func (n *Node) sendFollowToLeaderReq(from uint64) {
	n.events = append(n.events, types.Event{
		Type: types.FollowerToLeaderReq,
		From: from,
	})
}

func (n *Node) sendConfigReq() {
	n.events = append(n.events, types.Event{
		Type: types.ConfigReq,
		Term: n.cfg.Term,
		From: n.opts.NodeId,
		To:   n.cfg.Leader,
	})
}

func (n *Node) sendConfigResp(to uint64, cfg types.Config) {
	n.events = append(n.events, types.Event{
		Type:   types.ConfigResp,
		From:   n.opts.NodeId,
		To:     to,
		Term:   n.cfg.Term,
		Config: cfg,
	})
}

func (n *Node) sendDestory() {
	n.events = append(n.events, types.Event{
		Type: types.Destory,
		To:   types.LocalNode,
	})
}
