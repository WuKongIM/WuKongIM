package channelwrite

import "time"

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

// Options configures the channel write reactor group.
type Options struct {
	// LocalNodeID is the node id allowed to own local channel authority state.
	LocalNodeID uint64
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
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	return opts
}

func stateLimitsFromOptions(opts Options) channelStateLimits {
	return channelStateLimits{
		pendingItemHighWatermark: opts.PendingItemHighWatermark,
		appendInflightLimit:      opts.AppendInflightLimit,
	}
}
