package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

// EventKind identifies a reactor event.
type EventKind uint8

const (
	// Control events manage metadata, runtime lookup, cancellation, and close.
	EventApplyMeta EventKind = iota + 1
	// EventCheckState asks the owning reactor whether it has channel state loaded.
	EventCheckState
	// EventLookupCommittedMessage asks the owning reactor to verify one committed message id.
	EventLookupCommittedMessage
	// EventRuntimeSnapshot asks one reactor to summarize loaded runtime state.
	EventRuntimeSnapshot
	// EventRuntimeProbe asks one reactor to inspect selected loaded runtimes.
	EventRuntimeProbe
	// EventDrainChannel asks one reactor to check whether a fenced local leader has drained accepted appends.
	EventDrainChannel
	// EventRuntimeEvict asks one reactor to evict selected safe runtimes.
	EventRuntimeEvict
	// EventRetentionView asks one reactor for one channel's retention view.
	EventRetentionView
	// EventApplyRetentionBoundary adopts and safely applies one local retention boundary.
	EventApplyRetentionBoundary
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
	Notify         transport.NotifyRequest
	PullHint       transport.PullHintRequest
	RetentionApply ch.RetentionApplyRequest
	DrainChannel   ch.DrainChannelRequest
	OpID           ch.OpID
	// MessageID selects a durable message for EventLookupCommittedMessage.
	MessageID uint64
	CancelOp  ch.OpID
	CancelErr error
	// TickNow carries the requested EventTick clock and is internally reused for observed EventPull admission time.
	TickNow time.Time
	// RuntimeChannelIDs selects concrete channel IDs for runtime probe and eviction events.
	RuntimeChannelIDs []ch.ChannelID
	// LeaderEvictAppendSeq fences final leader eviction behind same-channel Append submissions.
	LeaderEvictAppendSeq uint64
}

func eventKindName(kind EventKind) string {
	switch kind {
	case EventApplyMeta:
		return "EventApplyMeta"
	case EventCheckState:
		return "EventCheckState"
	case EventLookupCommittedMessage:
		return "EventLookupCommittedMessage"
	case EventRuntimeSnapshot:
		return "EventRuntimeSnapshot"
	case EventRuntimeProbe:
		return "EventRuntimeProbe"
	case EventDrainChannel:
		return "EventDrainChannel"
	case EventRuntimeEvict:
		return "EventRuntimeEvict"
	case EventRetentionView:
		return "EventRetentionView"
	case EventApplyRetentionBoundary:
		return "EventApplyRetentionBoundary"
	case EventAppend:
		return "EventAppend"
	case EventWorkerResult:
		return "EventWorkerResult"
	case EventTick:
		return "EventTick"
	case EventCancelWaiter:
		return "EventCancelWaiter"
	case EventPull:
		return "EventPull"
	case EventAck:
		return "EventAck"
	case EventNotify:
		return "EventNotify"
	case EventPullHint:
		return "EventPullHint"
	case EventLeaderEvictReady:
		return "EventLeaderEvictReady"
	case EventClose:
		return "EventClose"
	default:
		return "EventUnknown"
	}
}
