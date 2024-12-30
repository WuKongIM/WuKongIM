package raft

func (n *Node) sendSyncReq(to uint64) {
	n.events = append(n.events, Event{
		Type: SyncReq,
		From: n.opts.NodeId,
		To:   to,
		Term: n.cfg.Term,
	})
}

func (n *Node) sendVoteReq(to uint64) {
	n.events = append(n.events, Event{
		Type: VoteReq,
		From: n.opts.NodeId,
		To:   to,
		Term: n.cfg.Term,
	})
}

func (n *Node) sendVoteResp(to uint64, reject bool) {
	n.events = append(n.events, Event{
		Type:   VoteResp,
		From:   n.opts.NodeId,
		To:     to,
		Term:   n.cfg.Term,
		Reject: reject,
	})
}
