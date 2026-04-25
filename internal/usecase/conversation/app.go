package conversation

import (
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultActiveScanLimit       = 2000
	defaultChannelProbeBatchSize = 512
	defaultColdThreshold         = 30 * 24 * time.Hour
	defaultFlushInterval         = 200 * time.Millisecond
	defaultFlushDirtyLimit       = 1024
	defaultSubscriberPageSize    = 512
)

type Options struct {
	States                ConversationStateStore
	ChannelUpdate         ChannelUpdateStore
	Facts                 MessageFactsStore
	Now                   func() time.Time
	ColdThreshold         time.Duration
	ActiveScanLimit       int
	ChannelProbeBatchSize int
	Async                 func(func())
	Logger                wklog.Logger
}

type App struct {
	states                ConversationStateStore
	channelUpdate         ChannelUpdateStore
	facts                 MessageFactsStore
	now                   func() time.Time
	coldThreshold         time.Duration
	activeScanLimit       int
	channelProbeBatchSize int
	async                 func(func())
	logger                wklog.Logger
	demotionMu            sync.Mutex
	pendingDemotions      map[string]*pendingUIDDemotion
}

type pendingUIDDemotion struct {
	running bool
	keys    map[metadb.ConversationKey]struct{}
}

func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ColdThreshold <= 0 {
		opts.ColdThreshold = defaultColdThreshold
	}
	if opts.ActiveScanLimit <= 0 {
		opts.ActiveScanLimit = defaultActiveScanLimit
	}
	if opts.ChannelProbeBatchSize <= 0 {
		opts.ChannelProbeBatchSize = defaultChannelProbeBatchSize
	}
	if opts.Async == nil {
		opts.Async = func(fn func()) { go fn() }
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}

	return &App{
		states:                opts.States,
		channelUpdate:         opts.ChannelUpdate,
		facts:                 opts.Facts,
		now:                   opts.Now,
		coldThreshold:         opts.ColdThreshold,
		activeScanLimit:       opts.ActiveScanLimit,
		channelProbeBatchSize: opts.ChannelProbeBatchSize,
		async:                 opts.Async,
		logger:                opts.Logger,
		pendingDemotions:      make(map[string]*pendingUIDDemotion),
	}
}

func (a *App) enqueuePendingDemotions(uid string, keys []metadb.ConversationKey) bool {
	if a == nil || len(keys) == 0 {
		return false
	}

	a.demotionMu.Lock()
	defer a.demotionMu.Unlock()

	state, ok := a.pendingDemotions[uid]
	if !ok {
		state = &pendingUIDDemotion{
			keys: make(map[metadb.ConversationKey]struct{}, len(keys)),
		}
		a.pendingDemotions[uid] = state
	}
	for _, key := range keys {
		state.keys[key] = struct{}{}
	}
	if state.running {
		return false
	}
	state.running = true
	return true
}

func (a *App) dequeuePendingDemotions(uid string) []metadb.ConversationKey {
	if a == nil {
		return nil
	}

	a.demotionMu.Lock()
	defer a.demotionMu.Unlock()

	state := a.pendingDemotions[uid]
	if state == nil || len(state.keys) == 0 {
		return nil
	}

	keys := make([]metadb.ConversationKey, 0, len(state.keys))
	for key := range state.keys {
		keys = append(keys, key)
	}
	state.keys = make(map[metadb.ConversationKey]struct{})
	return keys
}

func (a *App) stopPendingDemotions(uid string) bool {
	if a == nil {
		return true
	}

	a.demotionMu.Lock()
	defer a.demotionMu.Unlock()

	state := a.pendingDemotions[uid]
	if state == nil {
		return true
	}
	if len(state.keys) > 0 {
		return false
	}
	delete(a.pendingDemotions, uid)
	return true
}

func (a *App) failPendingDemotions(uid string, keys []metadb.ConversationKey) {
	if a == nil || len(keys) == 0 {
		return
	}

	a.demotionMu.Lock()
	defer a.demotionMu.Unlock()

	state, ok := a.pendingDemotions[uid]
	if !ok {
		state = &pendingUIDDemotion{
			keys: make(map[metadb.ConversationKey]struct{}, len(keys)),
		}
		a.pendingDemotions[uid] = state
	}
	for _, key := range keys {
		state.keys[key] = struct{}{}
	}
	state.running = false
}
