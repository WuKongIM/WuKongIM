package conversation

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultActiveScanLimit    = 2000
	defaultFlushInterval      = 200 * time.Millisecond
	defaultSubscriberPageSize = 512
)

type Options struct {
	States                ConversationStateStore
	Deletes               ConversationDeleteStore
	Facts                 MessageFactsStore
	Now                   func() time.Time
	ActiveScanLimit       int
	ChannelProbeBatchSize int
	ColdThreshold         time.Duration
	Async                 func(func())
	Logger                wklog.Logger
}

type App struct {
	states          ConversationStateStore
	deletes         ConversationDeleteStore
	facts           MessageFactsStore
	now             func() time.Time
	activeScanLimit int
}

func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ActiveScanLimit <= 0 {
		opts.ActiveScanLimit = defaultActiveScanLimit
	}
	if opts.Deletes == nil {
		if deletes, ok := opts.States.(ConversationDeleteStore); ok {
			opts.Deletes = deletes
		}
	}

	return &App{
		states:          opts.States,
		deletes:         opts.Deletes,
		facts:           opts.Facts,
		now:             opts.Now,
		activeScanLimit: opts.ActiveScanLimit,
	}
}
