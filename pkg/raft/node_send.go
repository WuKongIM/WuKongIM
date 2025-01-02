package raft

func (n *Node) sendNotifySync(to uint64) {
	if to == All {
		for _, replicaId := range n.cfg.Replicas {
			if replicaId == n.opts.NodeId {
				continue
			}
			n.events = append(n.events, Event{
				Type: NotifySync,
				From: n.opts.NodeId,
				To:   replicaId,
				Term: n.cfg.Term,
			})
		}
	} else {
		n.events = append(n.events, Event{
			Type: NotifySync,
			From: n.opts.NodeId,
			To:   to,
			Term: n.cfg.Term,
		})
	}

}

func (n *Node) sendSyncReq() {
	n.syncElapsed = 0
	var reason Reason
	if n.onlySync {
		reason = ReasonOnlySync
	}
	n.events = append(n.events, Event{
		Type:        SyncReq,
		From:        n.opts.NodeId,
		To:          n.leaderId,
		Term:        n.cfg.Term,
		StoredIndex: n.queue.storedIndex + 1,
		Index:       n.queue.lastLogIndex + 1,
		LastLogTerm: n.lastTermStartIndex.Term,
		Reason:      reason,
	})
}

func (n *Node) sendVoteReq(to uint64) {
	n.events = append(n.events, Event{
		Type: VoteReq,
		From: n.opts.NodeId,
		To:   to,
		Term: n.cfg.Term,
		Logs: []Log{
			{
				Term:  n.cfg.Term,
				Index: n.queue.lastLogIndex,
			},
		},
	})
}

func (n *Node) sendVoteResp(to uint64, reason Reason) {
	n.events = append(n.events, Event{
		Type:   VoteResp,
		From:   n.opts.NodeId,
		To:     to,
		Term:   n.cfg.Term,
		Reason: reason,
		Index:  n.queue.storedIndex,
	})
}

func (n *Node) sendPing(to uint64) {
	if !n.isLeader() {
		return
	}
	if to != All {
		n.events = append(n.events, Event{
			Type:           Ping,
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
		n.events = append(n.events, Event{
			Type:  Ping,
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
			n.events = append(n.events, Event{
				Type:  Ping,
				From:  n.opts.NodeId,
				To:    replicaId,
				Term:  n.cfg.Term,
				Index: n.queue.lastLogIndex,
			})
		}
	}
}

func (n *Node) sendStoreReq(logs []Log) {
	n.events = append(n.events, Event{
		Type: StoreReq,
		Term: n.cfg.Term,
		Logs: logs,
	})
}

func (n *Node) sendGetLogsReq(syncEvent Event) {
	n.events = append(n.events, Event{
		Type:        GetLogsReq,
		From:        syncEvent.From,
		Term:        syncEvent.Term,
		Index:       syncEvent.Index,
		StoredIndex: syncEvent.StoredIndex,
		LastLogTerm: syncEvent.LastLogTerm,
		Reason:      syncEvent.Reason,
	})
}

func (n *Node) sendSyncResp(to uint64, syncIndex uint64, logs []Log, reson Reason) {
	n.events = append(n.events, Event{
		Type:           SyncResp,
		From:           n.opts.NodeId,
		To:             to,
		Term:           n.cfg.Term,
		Index:          syncIndex,
		Logs:           logs,
		CommittedIndex: n.queue.committedIndex,
		Reason:         reson,
	})
}
