package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

// Appender owns durable channel append routing.
type Appender interface {
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
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
type CommittedSink interface {
	Submit(context.Context, messageevents.MessageCommitted) error
}

// Observer receives non-fatal send path observations.
type Observer interface {
	CommittedSinkError(SendCommand, error)
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

type noopCommittedSink struct{}

func (noopCommittedSink) Submit(context.Context, messageevents.MessageCommitted) error {
	return nil
}
