package raft

import "go.etcd.io/raft/v3/raftpb"

type proposalTracker struct {
	queue   []trackedProposal
	byIndex map[uint64]trackedProposal
}

func newProposalTracker() *proposalTracker {
	return &proposalTracker{queue: make([]trackedProposal, 0, 8), byIndex: make(map[uint64]trackedProposal)}
}

func (t *proposalTracker) enqueue(p trackedProposal) {
	t.queue = append(t.queue, p)
}

func (t *proposalTracker) bindAppended(entries []raftpb.Entry) {
	for _, entry := range entries {
		if entry.Type != raftpb.EntryNormal || len(t.queue) == 0 {
			continue
		}
		isProbeEntry := len(entry.Data) == 0
		if t.queue[0].probe != isProbeEntry {
			continue
		}
		tracked := t.queue[0]
		t.queue = t.queue[1:]
		t.byIndex[entry.Index] = tracked
	}
}

func (t *proposalTracker) complete(index uint64, err error) {
	tracked, ok := t.byIndex[index]
	if !ok {
		return
	}
	tracked.resp <- err
	delete(t.byIndex, index)
}

func (t *proposalTracker) failAll(err error) {
	for _, tracked := range t.queue {
		tracked.resp <- err
	}
	t.queue = t.queue[:0]
	for index, tracked := range t.byIndex {
		tracked.resp <- err
		delete(t.byIndex, index)
	}
}

func (t *proposalTracker) failUnbound(err error) {
	for _, tracked := range t.queue {
		tracked.resp <- err
	}
	t.queue = t.queue[:0]
}

func (t *proposalTracker) failFrom(index uint64, err error) {
	for idx, tracked := range t.byIndex {
		if idx >= index {
			tracked.resp <- err
			delete(t.byIndex, idx)
		}
	}
}
