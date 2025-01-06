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
			n.events = append(n.events, types.Event{
				Type: types.NotifySync,
				From: n.opts.NodeId,
				To:   replicaId,
				Term: n.cfg.Term,
			})
		}
	} else {
		n.events = append(n.events, types.Event{
			Type: types.NotifySync,
			From: n.opts.NodeId,
			To:   to,
			Term: n.cfg.Term,
		})
	}

}

func (n *Node) sendSyncReq() {

	if n.truncating { // 如果在截断中，则发起同步没用
		return
	}

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
				Term:  n.cfg.Term,
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
	n.events = append(n.events, types.Event{
		Type:               types.StoreReq,
		Term:               n.cfg.Term,
		To:                 types.LocalNode,
		Logs:               logs,
		TermStartIndexInfo: termStartIndexInfo,
	})
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

func (n *Node) sendSyncResp(to uint64, syncIndex uint64, logs []types.Log, reson types.Reason) {
	n.events = append(n.events, types.Event{
		Type:           types.SyncResp,
		From:           n.opts.NodeId,
		To:             to,
		Term:           n.cfg.Term,
		Index:          syncIndex,
		Logs:           logs,
		CommittedIndex: n.queue.committedIndex,
		Reason:         reson,
	})
}

func (n *Node) sendTruncateReq(index uint64) {
	n.events = append(n.events, types.Event{
		Type:  types.TruncateReq,
		To:    types.LocalNode,
		Term:  n.lastTermStartIndex.Term,
		Index: index,
	})
}
