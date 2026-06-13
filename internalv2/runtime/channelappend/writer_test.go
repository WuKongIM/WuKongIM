package channelappend

import "testing"

func TestNewChannelWriterStartsIdle(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	if w.scheduled.Load() {
		t.Fatal("new writer must not be scheduled")
	}
	if w.state == nil {
		t.Fatal("new writer must own a channelState")
	}
	if w.key != target.ChannelKey {
		t.Fatalf("writer key = %q, want %q", w.key, target.ChannelKey)
	}
}

func TestWriterEnqueueActivatesOnce(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})

	first := w.enqueue(submittedBatch{target: target, future: newFuture(0)})
	if !first {
		t.Fatal("first enqueue must request activation")
	}
	second := w.enqueue(submittedBatch{target: target, future: newFuture(0)})
	if second {
		t.Fatal("second enqueue while scheduled must not request activation")
	}
	if got := len(w.inbox); got != 2 {
		t.Fatalf("inbox len = %d, want 2", got)
	}
}

func TestWriterDeactivateLockedReportsRunnableInbox(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	w.scheduled.Store(true)

	w.mu.Lock()
	w.inbox = append(w.inbox, submittedBatch{target: target, future: newFuture(1)})
	more := w.deactivateLocked()
	w.mu.Unlock()

	if !more {
		t.Fatal("deactivateLocked must report runnable inbox work")
	}
	if w.scheduled.Load() {
		t.Fatal("deactivateLocked must clear scheduled before reporting more work")
	}
	if idleAt := w.lastIdleUnixNano.Load(); idleAt != 0 {
		t.Fatalf("lastIdleUnixNano = %d, want 0 while work is runnable", idleAt)
	}
	if !w.tryActivate() {
		t.Fatal("current advance owner should be able to reactivate after lock-held runnable work")
	}
}

func TestWriterDeactivateLockedMarksIdleWhenNoRunnableWork(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	w.scheduled.Store(true)

	w.mu.Lock()
	more := w.deactivateLocked()
	w.mu.Unlock()

	if more {
		t.Fatal("deactivateLocked reported runnable work for an idle writer")
	}
	if w.scheduled.Load() {
		t.Fatal("deactivateLocked must clear scheduled for idle writer")
	}
	if idleAt := w.lastIdleUnixNano.Load(); idleAt == 0 {
		t.Fatal("deactivateLocked must record idle timestamp")
	}
}
