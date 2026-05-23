package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

// EventKind identifies a reactor event.
type EventKind uint8

const (
	EventApplyMeta EventKind = iota + 1
	EventAppend
	EventFetch
	// EventWorkerResult carries a blocking worker completion back to its reactor.
	EventWorkerResult
	// EventTick asks a reactor to perform low-priority maintenance work.
	EventTick
	// EventCancelWaiter cooperatively cancels a previously admitted waiter.
	EventCancelWaiter
	EventPull
	EventAck
	EventClose
)

// Event is the mailbox envelope consumed by reactors.
type Event struct {
	Kind      EventKind
	Key       ch.ChannelKey
	Meta      ch.Meta
	Append    ch.AppendBatchRequest
	Context   context.Context
	Fetch     ch.FetchRequest
	Future    *Future
	Worker    worker.Result
	Pull      transport.PullRequest
	Ack       transport.AckRequest
	OpID      ch.OpID
	CancelOp  ch.OpID
	CancelErr error
	TickNow   time.Time
}
