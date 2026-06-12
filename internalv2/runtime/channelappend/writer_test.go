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
