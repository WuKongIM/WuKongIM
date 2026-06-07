package message

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// Appender owns durable channel append routing.
type Appender interface {
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
}

// ChannelMessageReader owns compatible channel message sync reads.
type ChannelMessageReader interface {
	// SyncMessages returns one authoritative channel message page.
	SyncMessages(context.Context, ChannelMessageQuery) (ChannelMessagePage, error)
}

// MessageIDAllocator allocates durable message ids.
type MessageIDAllocator interface {
	Next() uint64
}

// Decision is the result of send authorization.
type Decision struct {
	// Allowed reports whether the send may proceed.
	Allowed bool
	// Reason explains rejected decisions.
	Reason Reason
}

// Authorizer decides whether a send may enter durable append.
type Authorizer interface {
	AuthorizeSend(context.Context, SendCommand) (Decision, error)
}

// CommittedSink receives durable append events.
// Implementations must be safe for concurrent Submit calls from separate
// SendBatch invocations.
type CommittedSink interface {
	Submit(context.Context, messageevents.MessageCommitted) error
}

// Observer receives non-fatal send path observations.
type Observer interface {
	CommittedSinkError(SendCommand, error)
}

// AppendObserver receives per-message durable append observations.
type AppendObserver interface {
	AppendFinished(path string, err error, dur time.Duration)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}
