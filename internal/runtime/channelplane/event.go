package channelplane

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type reactorEventKind uint8

const (
	reactorEventAppend reactorEventKind = iota + 1
	reactorEventResolveComplete
	reactorEventAppendComplete
)

type reactorEvent struct {
	kind       reactorEventKind
	cmd        *appendCommand
	completion effectCompletion
}

type effectCompletion struct {
	key   channel.ChannelKey
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
