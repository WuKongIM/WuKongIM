package reactor

import (
	"container/heap"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type dueKind uint8

const (
	dueAppendFlush dueKind = iota + 1
	dueReplication
	dueLifecycle
	duePendingMeta
	dueLoadedMetaRefresh
)

// dueItem records one scheduled maintenance attempt for a channel.
type dueItem struct {
	key     ch.ChannelKey
	kind    dueKind
	due     time.Time
	version uint64
	index   int
}

type dueSlot struct {
	kind dueKind
	key  ch.ChannelKey
}

// dueScheduler is a min-heap of channel maintenance work ordered by due time.
type dueScheduler struct {
	items []dueItem
	slots map[dueSlot]int
}

func (s *dueScheduler) push(item dueItem) {
	if item.due.IsZero() {
		item.due = time.Now()
	}
	if s.slots == nil {
		s.slots = make(map[dueSlot]int)
	}
	slot := dueSlot{kind: item.kind, key: item.key}
	if index, ok := s.slots[slot]; ok && index >= 0 && index < len(s.items) {
		item.index = index
		s.items[index] = item
		heap.Fix(s, index)
		return
	}
	heap.Push(s, item)
}

func (s *dueScheduler) popDue(now time.Time) (dueItem, bool) {
	if s == nil || len(s.items) == 0 || now.Before(s.items[0].due) {
		return dueItem{}, false
	}
	item, _ := heap.Pop(s).(dueItem)
	return item, true
}

func (s *dueScheduler) remove(kind dueKind, key ch.ChannelKey) bool {
	if s == nil || s.slots == nil {
		return false
	}
	index, ok := s.slots[dueSlot{kind: kind, key: key}]
	if !ok || index < 0 || index >= len(s.items) {
		return false
	}
	heap.Remove(s, index)
	return true
}

func (s *dueScheduler) nextWait(now time.Time) time.Duration {
	if s == nil || len(s.items) == 0 {
		return time.Hour
	}
	wait := s.items[0].due.Sub(now)
	if wait < 0 {
		return 0
	}
	return wait
}

func (s dueScheduler) Len() int { return len(s.items) }

func (s dueScheduler) Less(i, j int) bool {
	left := s.items[i]
	right := s.items[j]
	if !left.due.Equal(right.due) {
		return left.due.Before(right.due)
	}
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	if left.key != right.key {
		return left.key < right.key
	}
	return left.version < right.version
}

func (s dueScheduler) Swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
	s.items[i].index = i
	s.items[j].index = j
	if s.slots != nil {
		s.slots[dueSlot{kind: s.items[i].kind, key: s.items[i].key}] = i
		s.slots[dueSlot{kind: s.items[j].kind, key: s.items[j].key}] = j
	}
}

func (s *dueScheduler) Push(x any) {
	item := x.(dueItem)
	item.index = len(s.items)
	s.items = append(s.items, item)
	if s.slots == nil {
		s.slots = make(map[dueSlot]int)
	}
	s.slots[dueSlot{kind: item.kind, key: item.key}] = item.index
}

func (s *dueScheduler) Pop() any {
	old := s.items
	n := len(old)
	item := old[n-1]
	item.index = -1
	old[n-1] = dueItem{}
	s.items = old[:n-1]
	if s.slots != nil {
		delete(s.slots, dueSlot{kind: item.kind, key: item.key})
	}
	return item
}

func (r *Reactor) processDue(now time.Time) {
	if r == nil {
		return
	}
	for {
		item, ok := r.due.popDue(now)
		if !ok {
			return
		}
		r.processDueItem(item, now)
	}
}

func (r *Reactor) processDueItem(item dueItem, now time.Time) {
	var started time.Time
	if r != nil && r.cfg.SlowDueThreshold > 0 {
		started = time.Now()
		defer func() { r.observeSlowDue(item.kind, time.Since(started)) }()
	}
	if item.kind == dueLoadedMetaRefresh {
		r.releaseExpiredLoadedMetaRefresh(item.key, item.version, now)
		return
	}
	rc := r.channels[item.key]
	if rc == nil {
		return
	}
	switch item.kind {
	case dueAppendFlush:
		if item.version != rc.appendFlushDueVersion {
			return
		}
		r.tryFlushAppend(rc, now)
	case dueReplication:
		if item.version != rc.replicationDueVersion {
			return
		}
		r.tickFollowerReplication(rc, now)
	case dueLifecycle:
		if item.version != rc.lifecycleDueVersion {
			return
		}
		r.tickLifecycleController(rc, now)
	case duePendingMeta:
		r.releaseExpiredPendingMeta(item.key, rc, now)
	}
}

func dueKindName(kind dueKind) string {
	switch kind {
	case dueAppendFlush:
		return "dueAppendFlush"
	case dueReplication:
		return "dueReplication"
	case dueLifecycle:
		return "dueLifecycle"
	case duePendingMeta:
		return "duePendingMeta"
	case dueLoadedMetaRefresh:
		return "dueLoadedMetaRefresh"
	default:
		return "dueUnknown"
	}
}

func (r *Reactor) scheduleAppendFlushFromState(rc *runtimeChannel) {
	if r == nil || rc == nil || rc.state == nil {
		return
	}
	due, ok := r.nextAppendFlushDue(rc)
	if !ok {
		return
	}
	rc.appendFlushDueVersion++
	r.due.push(dueItem{key: rc.state.Key, kind: dueAppendFlush, due: due, version: rc.appendFlushDueVersion})
}

func (r *Reactor) nextAppendFlushDue(rc *runtimeChannel) (time.Time, bool) {
	if rc == nil || rc.appendInflight != nil || len(rc.appendQ.pending) == 0 {
		return time.Time{}, false
	}
	if rc.appendStoreBlocked {
		if rc.appendRetryAt.IsZero() {
			return time.Now(), true
		}
		return rc.appendRetryAt, true
	}
	if rc.appendQ.storeBlocked {
		return time.Time{}, false
	}
	if rc.appendQ.cfg.MaxRecords > 0 && rc.appendQ.records >= rc.appendQ.cfg.MaxRecords {
		return time.Now(), true
	}
	if rc.appendQ.cfg.MaxBytes > 0 && rc.appendQ.bytes >= rc.appendQ.cfg.MaxBytes {
		return time.Now(), true
	}
	if !rc.appendQ.flushDue.IsZero() {
		return rc.appendQ.flushDue, true
	}
	return time.Time{}, false
}

func (r *Reactor) scheduleReplicationFromState(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || rc.state == nil || rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive {
		return
	}
	due, ok := r.nextReplicationDue(rc, now)
	if !ok {
		return
	}
	rc.replicationDueVersion++
	r.due.push(dueItem{key: rc.state.Key, kind: dueReplication, due: due, version: rc.replicationDueVersion})
}

func (r *Reactor) nextReplicationDue(rc *runtimeChannel, now time.Time) (time.Time, bool) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive {
		return time.Time{}, false
	}
	replication := rc.replication
	if replication.pullInflight || replication.applyOpID != 0 ||
		rc.lifecycle.checkpoint.inflight || rc.lifecycle.stoppedAck.inflight {
		return time.Time{}, false
	}
	if rc.lifecycle.stage == lifecycleFollowerReadyToEvict {
		if !rc.lifecycle.finalCheck.retryAt.IsZero() {
			return rc.lifecycle.finalCheck.retryAt, true
		}
		return now, true
	}
	if rc.lifecycle.followerStop.accepted {
		if rc.lifecycle.stage == lifecycleFollowerCheckpointing {
			if !rc.lifecycle.checkpoint.retryAt.IsZero() {
				return rc.lifecycle.checkpoint.retryAt, true
			}
			return now, true
		}
		if rc.lifecycle.stage == lifecycleFollowerStoppedAcking {
			if !rc.lifecycle.stoppedAck.retryAt.IsZero() {
				return rc.lifecycle.stoppedAck.retryAt, true
			}
			return now, true
		}
		return now, true
	}
	if replication.pendingPull != nil {
		if replication.applyBlocked && !replication.applyRetryAt.IsZero() {
			return replication.applyRetryAt, true
		}
		return now, true
	}
	if replication.dirty {
		if !replication.nextPullAt.IsZero() && now.Before(replication.nextPullAt) {
			return replication.nextPullAt, true
		}
		return now, true
	}
	if !replication.nextPullAt.IsZero() {
		return replication.nextPullAt, true
	}
	return time.Time{}, false
}

func (r *Reactor) scheduleLifecycleFromState(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || rc.state.Status != ch.StatusActive {
		return
	}
	due, ok := r.nextLifecycleDue(rc, now)
	if !ok {
		return
	}
	rc.lifecycleDueVersion++
	r.due.push(dueItem{key: rc.state.Key, kind: dueLifecycle, due: due, version: rc.lifecycleDueVersion})
}

func (r *Reactor) schedulePendingMetaDeadline(rc *runtimeChannel) {
	if r == nil || rc == nil || rc.pending == nil || rc.pending.deadline.IsZero() {
		return
	}
	r.due.push(dueItem{key: rc.pending.key, kind: duePendingMeta, due: rc.pending.deadline, version: rc.pending.generation})
}

func (r *Reactor) nextLifecycleDue(rc *runtimeChannel, now time.Time) (time.Time, bool) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || rc.state.Status != ch.StatusActive {
		return time.Time{}, false
	}
	var due time.Time
	add := func(candidate time.Time) {
		if candidate.IsZero() {
			return
		}
		if due.IsZero() || candidate.Before(due) {
			due = candidate
		}
	}
	if !rc.lifecycle.checkpoint.inflight && !rc.lifecycle.checkpoint.retryAt.IsZero() {
		if now.Before(rc.lifecycle.checkpoint.retryAt) {
			add(rc.lifecycle.checkpoint.retryAt)
		} else {
			rc.lifecycle.checkpoint.retryAt = time.Time{}
		}
	}
	if rc.lifecycle.finalCheck.inflight &&
		!rc.lifecycle.finalCheck.queued &&
		rc.lifecycle.checkpoint.retryAt.IsZero() &&
		rc.state.HW >= rc.state.LEO &&
		r.allFollowersStopped(rc) &&
		!r.hasPendingRuntimeWork(rc) {
		add(now)
	}
	for _, follower := range rc.lifecycle.followers {
		if follower == nil || follower.hint.inflight {
			continue
		}
		add(follower.hint.retryAt)
	}
	idleSince := leaderIdleSince(rc)
	if !idleSince.IsZero() {
		if r.cfg.IdleSlowdownAfter > 0 && now.Before(idleSince.Add(r.cfg.IdleSlowdownAfter)) {
			add(idleSince.Add(r.cfg.IdleSlowdownAfter))
		}
		if now.Before(idleSince.Add(r.cfg.IdleEvictAfter)) {
			add(idleSince.Add(r.cfg.IdleEvictAfter))
		} else if !rc.lifecycle.checkpoint.inflight {
			add(now.Add(r.cfg.IdleEvictCheckInterval))
		}
	}
	if due.IsZero() {
		return time.Time{}, false
	}
	return due, true
}
