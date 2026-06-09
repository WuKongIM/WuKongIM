package channelwrite

import (
	"context"
	"time"
)

const (
	defaultReactorCount             = 1
	defaultMailboxSize              = 1024
	defaultPendingItemHighWatermark = 1024
	defaultAppendInflightLimit      = 1
	defaultEffectWorkerCount        = 1
)

// Clock provides the time source used by channel write runtime state.
type Clock interface {
	// Now returns the current wall-clock time.
	Now() time.Time
}

// Authorizer decides whether a send may enter the pre-append pending queue.
type Authorizer interface {
	// AuthorizeSend returns a send decision for the canonical command shape known so far.
	AuthorizeSend(context.Context, SendCommand) (Decision, error)
}

// MessageIDAllocator allocates durable message ids before append.
type MessageIDAllocator interface {
	// Next returns the next globally unique message id.
	Next() uint64
}

// IdempotencyStore recovers previously committed sends before allocating a new message id.
type IdempotencyStore interface {
	// LookupSend returns a prior successful result for a canonical sender/client/channel key.
	LookupSend(context.Context, IdempotencyQuery) (SendResult, bool, error)
}

// SenderFenceValidator validates sender-scoped fencing before a send is prepared.
type SenderFenceValidator interface {
	// ValidateSender returns an error when the sender fence rejects the command.
	ValidateSender(context.Context, SendCommand) error
}

// Options configures the channel write reactor group.
type Options struct {
	// LocalNodeID is the node id allowed to own local channel authority state.
	LocalNodeID uint64
	// MessageID allocates message ids for non-idempotent gateway-origin sends.
	MessageID MessageIDAllocator
	// Authorizer decides whether a send may enter the pending append queue.
	Authorizer Authorizer
	// Idempotency recovers successful sends before allocating a new message id.
	Idempotency IdempotencyStore
	// SenderFence validates sender-scoped fencing before authorization.
	SenderFence SenderFenceValidator
	// ReactorCount is the number of channel-hashed reactors. Values <= 0 use one reactor.
	ReactorCount int
	// MailboxSize bounds each reactor mailbox. Values <= 0 use a conservative bounded default.
	MailboxSize int
	// PendingItemHighWatermark is reserved for per-channel admission pressure. Values <= 0 use a bounded default.
	PendingItemHighWatermark int
	// AppendInflightLimit bounds future append effects per channel. Values <= 0 use one append inflight.
	AppendInflightLimit int
	// EffectWorkerCount is reserved for future bounded effect workers. Values <= 0 use one worker.
	EffectWorkerCount int
	// Clock supplies runtime timestamps. Nil uses the system clock.
	Clock Clock
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func applyDefaults(opts Options) Options {
	if opts.ReactorCount <= 0 {
		opts.ReactorCount = defaultReactorCount
	}
	if opts.MailboxSize <= 0 {
		opts.MailboxSize = defaultMailboxSize
	}
	if opts.PendingItemHighWatermark <= 0 {
		opts.PendingItemHighWatermark = defaultPendingItemHighWatermark
	}
	if opts.AppendInflightLimit <= 0 {
		opts.AppendInflightLimit = defaultAppendInflightLimit
	}
	if opts.EffectWorkerCount <= 0 {
		opts.EffectWorkerCount = defaultEffectWorkerCount
	}
	if opts.Authorizer == nil {
		opts.Authorizer = allowAllAuthorizer{}
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	return opts
}

func preparePortsFromOptions(opts Options) preparePorts {
	return preparePorts{
		messageID:   opts.MessageID,
		authorizer:  opts.Authorizer,
		idempotency: opts.Idempotency,
		senderFence: opts.SenderFence,
		clock:       opts.Clock,
	}
}

func stateLimitsFromOptions(opts Options) channelStateLimits {
	return channelStateLimits{
		pendingItemHighWatermark: opts.PendingItemHighWatermark,
		appendInflightLimit:      opts.AppendInflightLimit,
	}
}
