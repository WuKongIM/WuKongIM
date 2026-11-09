package replication

import (
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// DurableProgressReader exposes process-local durability proofs per voter for
// retention safety. A durable-quorum round reaches only a write quorum in the
// foreground and writes trailing voters afterwards, so the leader learns every
// ISR member's position only from these proofs. Proofs never grant quorum,
// commit or read authority, and a voter without a proof is unknown (0).
type DurableProgressReader interface {
	// DurableProgress returns the authority the proofs belong to and a copy of
	// each proven voter's highest durable log index. ok is false when the
	// Channel has no loaded quorum state on this node.
	DurableProgress(key ch.ChannelKey) (authority AuthorityID, through map[ch.NodeID]uint64, ok bool)
}

// durableProofSink receives one voter's exact durable proof through an index.
type durableProofSink func(voter ch.NodeID, through uint64)

// durableProgress records the highest proven durable index per voter under one
// authority. It uses its own lock so retention reads never wait for Install,
// which holds the Channel sequencer lock across recovery. Entries are bounded
// by the authority's voters (MaxVoters) and cleared on every authority advance.
type durableProgress struct {
	mu        sync.Mutex
	authority AuthorityID
	through   map[ch.NodeID]uint64
}

// reset discards proofs from any previous authority. A new leader may
// truncate divergent suffixes, so older proofs are not evidence for it.
func (p *durableProgress) reset(authority AuthorityID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authority = authority
	clear(p.through)
}

// record advances voter to through when the proof belongs to the current
// authority. Proofs never move a voter backwards.
func (p *durableProgress) record(authority AuthorityID, voter ch.NodeID, through uint64) {
	if voter == 0 || through == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if authority != p.authority || authority == (AuthorityID{}) {
		return
	}
	if p.through == nil {
		p.through = make(map[ch.NodeID]uint64)
	}
	if through > p.through[voter] {
		p.through[voter] = through
	}
}

// sink binds proofs to authority, so late proofs from a fenced round are ignored.
func (p *durableProgress) sink(authority AuthorityID) durableProofSink {
	return func(voter ch.NodeID, through uint64) { p.record(authority, voter, through) }
}

// snapshot returns the current authority and a copy of its proofs.
func (p *durableProgress) snapshot() (AuthorityID, map[ch.NodeID]uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[ch.NodeID]uint64, len(p.through))
	for voter, through := range p.through {
		out[voter] = through
	}
	return p.authority, out
}

// DurableProgress implements DurableProgressReader for loaded Channels.
func (l *quorumLog) DurableProgress(key ch.ChannelKey) (AuthorityID, map[ch.NodeID]uint64, bool) {
	if l == nil {
		return AuthorityID{}, nil, false
	}
	state := l.existingChannel(key)
	if state == nil {
		return AuthorityID{}, nil, false
	}
	authority, through := state.progress.snapshot()
	return authority, through, authority != (AuthorityID{})
}
