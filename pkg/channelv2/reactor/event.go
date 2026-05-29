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
	// Control events manage metadata, runtime lookup, cancellation, and close.
	EventApplyMeta EventKind = iota + 1
	// EventCheckState asks the owning reactor whether it has channel state loaded.
	EventCheckState
	// EventAppend is the client write event handled by the local leader.
	EventAppend
	// EventWorkerResult carries a blocking worker completion back to its reactor.
	EventWorkerResult
	// EventTick asks a reactor to perform low-priority maintenance work.
	EventTick
	// EventCancelWaiter cooperatively cancels a previously admitted waiter.
	EventCancelWaiter
	// EventPull is an inbound follower pull handled by the local leader.
	EventPull
	// EventAck is an inbound follower progress report handled by the local leader.
	EventAck
	// EventNotify accepts legacy transport compatibility nudges.
	EventNotify
	// EventPullHint wakes a local follower after leader progress.
	EventPullHint
	// EventLeaderEvictReady performs the final normal-priority leader eviction recheck.
	EventLeaderEvictReady
	EventClose
)

// Event is the mailbox envelope consumed by reactors.
type Event struct {
	Kind    EventKind
	Key     ch.ChannelKey
	Meta    ch.Meta
	Append  ch.AppendBatchRequest
	Context context.Context
	Future  *Future
	Worker  worker.Result
	Pull    transport.PullRequest
	Ack     transport.AckRequest
	// Notify is the legacy transport compatibility nudge payload.
	Notify    transport.NotifyRequest
	PullHint  transport.PullHintRequest
	OpID      ch.OpID
	CancelOp  ch.OpID
	CancelErr error
	TickNow   time.Time
	// LeaderEvictAppendSeq fences final leader eviction behind same-channel Append submissions.
	LeaderEvictAppendSeq uint64
}
