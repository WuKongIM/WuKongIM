package channelwrite

// channelState holds in-memory state for one locally authoritative channel.
type channelState struct {
	target AuthorityTarget

	pendingItemHighWatermark int
	appendInflightLimit      int
}

type channelStateLimits struct {
	pendingItemHighWatermark int
	appendInflightLimit      int
}

func newChannelState(target AuthorityTarget, limits channelStateLimits) *channelState {
	return &channelState{
		target:                   target,
		pendingItemHighWatermark: limits.pendingItemHighWatermark,
		appendInflightLimit:      limits.appendInflightLimit,
	}
}
