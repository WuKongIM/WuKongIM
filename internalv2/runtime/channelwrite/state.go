package channelwrite

// channelState holds in-memory state for one locally authoritative channel.
type channelState struct {
	target AuthorityTarget

	pendingItemHighWatermark int
	appendInflightLimit      int
	pendingItems             []preparedSend
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

func (s *channelState) enqueuePrepared(items []preparedSend) {
	if len(items) == 0 {
		return
	}
	s.pendingItems = append(s.pendingItems, items...)
}
