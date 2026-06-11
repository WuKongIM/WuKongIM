package channelwrite

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
