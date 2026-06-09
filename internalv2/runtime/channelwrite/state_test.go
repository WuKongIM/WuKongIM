package channelwrite

import "testing"

func TestNewChannelStateCapturesAuthorityIdentityAndLimits(t *testing.T) {
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "room", Type: 2},
		ChannelKey:   "2:room",
		LeaderNodeID: 1,
		Epoch:        10,
		LeaderEpoch:  3,
	}

	state := newChannelState(target, channelStateLimits{
		pendingItemHighWatermark: 8,
		appendInflightLimit:      2,
	})

	if state.target != target {
		t.Fatalf("target = %+v, want %+v", state.target, target)
	}
	if state.pendingItemHighWatermark != 8 {
		t.Fatalf("pendingItemHighWatermark = %d, want 8", state.pendingItemHighWatermark)
	}
	if state.appendInflightLimit != 2 {
		t.Fatalf("appendInflightLimit = %d, want 2", state.appendInflightLimit)
	}
}
