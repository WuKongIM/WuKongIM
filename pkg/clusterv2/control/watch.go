package control

import "sync"

type snapshotWatchPublisher struct {
	mu           sync.Mutex
	ch           chan SnapshotEvent
	lastRevision uint64
	hasLast      bool
}

func newSnapshotWatchPublisher(ch chan SnapshotEvent) *snapshotWatchPublisher {
	return &snapshotWatchPublisher{ch: ch}
}

func (p *snapshotWatchPublisher) publish(snapshot Snapshot) {
	if p == nil || p.ch == nil {
		return
	}
	clone := snapshot.Clone()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasLast && clone.Revision < p.lastRevision {
		return
	}
	p.hasLast = true
	p.lastRevision = clone.Revision
	publishLatestSnapshotEvent(p.ch, clone)
}

func publishLatestSnapshotEvent(ch chan SnapshotEvent, snapshot Snapshot) {
	if ch == nil {
		return
	}
	event := SnapshotEvent{Snapshot: snapshot.Clone()}
	select {
	case ch <- event:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- event:
	default:
	}
}
