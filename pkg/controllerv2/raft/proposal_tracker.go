package raft

import "go.etcd.io/raft/v3/raftpb"

type proposalTracker struct {
	queue   []trackedProposal
	byIndex map[uint64]trackedProposal
}

type proposalBindResult struct {
	membershipRejected bool
}

func newProposalTracker() *proposalTracker {
	return &proposalTracker{queue: make([]trackedProposal, 0, 8), byIndex: make(map[uint64]trackedProposal)}
}

func (t *proposalTracker) enqueue(p trackedProposal) {
	t.queue = append(t.queue, p)
}

func (t *proposalTracker) bindAppended(entries []raftpb.Entry) proposalBindResult {
	var result proposalBindResult
	for _, entry := range entries {
		if len(t.queue) == 0 {
			continue
		}
		if entry.Type == raftpb.EntryConfChange || entry.Type == raftpb.EntryConfChangeV2 {
			if !t.queue[0].confChange {
				continue
			}
			tracked := t.queue[0]
			t.queue = t.queue[1:]
			t.byIndex[entry.Index] = tracked
			continue
		}
		if entry.Type != raftpb.EntryNormal {
			continue
		}
		isProbeEntry := len(entry.Data) == 0
		if t.queue[0].confChange && isProbeEntry {
			tracked := t.queue[0]
			t.queue = t.queue[1:]
			tracked.resp <- proposalResponse{err: ErrMembershipChangePending}
			result.membershipRejected = true
			continue
		}
		if t.queue[0].confChange || t.queue[0].probe != isProbeEntry {
			continue
		}
		tracked := t.queue[0]
		t.queue = t.queue[1:]
		t.byIndex[entry.Index] = tracked
	}
	return result
}

func (t *proposalTracker) complete(index uint64, result ProposalResult, err error) {
	tracked, ok := t.byIndex[index]
	if !ok {
		return
	}
	tracked.resp <- proposalResponse{result: result, err: err}
	delete(t.byIndex, index)
}

func (t *proposalTracker) completeMembership(index uint64, result MembershipChangeResult, err error) {
	tracked, ok := t.byIndex[index]
	if !ok {
		return
	}
	tracked.resp <- proposalResponse{membership: result, err: err}
	delete(t.byIndex, index)
}

func (t *proposalTracker) failAll(err error) {
	for _, tracked := range t.queue {
		tracked.resp <- proposalResponse{err: err}
	}
	t.queue = t.queue[:0]
	for index, tracked := range t.byIndex {
		tracked.resp <- proposalResponse{err: err}
		delete(t.byIndex, index)
	}
}

func (t *proposalTracker) failUnbound(err error) {
	for _, tracked := range t.queue {
		tracked.resp <- proposalResponse{err: err}
	}
	t.queue = t.queue[:0]
}

func (t *proposalTracker) failFrom(index uint64, err error) {
	for idx, tracked := range t.byIndex {
		if idx >= index {
			tracked.resp <- proposalResponse{err: err}
			delete(t.byIndex, idx)
		}
	}
}
