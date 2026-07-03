package channelplane

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

type reactorEventKind uint8

const (
	reactorEventAppend reactorEventKind = iota + 1
	reactorEventResolveComplete
	reactorEventAppendComplete
	reactorEventInspect
)

type reactorEvent struct {
	kind       reactorEventKind
	cmd        *appendCommand
	completion effectCompletion
	inspect    func()
}

type effectCompletion struct {
	key   channel.ChannelID
	cmd   *appendCommand
	route ChannelRoute
	res   channel.AppendBatchResult
	err   error
}

// AppendEvent describes one append request lifecycle transition.
type AppendEvent struct {
	// ChannelID is the channel being appended to.
	ChannelID channel.ChannelID
	// RouteGeneration is the route version used by the append effect when known.
	RouteGeneration uint64
	// Err is the terminal error for completion events.
	Err error
}
