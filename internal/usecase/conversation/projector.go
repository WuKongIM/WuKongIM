package conversation

import (
	"context"
	"errors"
	"sync"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultProjectorActiveHintQueueSize       = 1024
	defaultProjectorGroupActiveFanoutInterval = 5 * time.Minute
)

type Projector interface {
	Start() error
	Stop() error
	SubmitCommitted(ctx context.Context, msg channel.Message) error
	Flush(ctx context.Context) error
}

// ProjectorOptions configures best-effort active hint projection from committed messages.
type ProjectorOptions struct {
	Store ProjectorStore
	// FlushInterval controls periodic retry flushing for queued active hints.
	FlushInterval time.Duration
	// ActiveHintQueueSize bounds pending conversations retained before hints are dropped.
	ActiveHintQueueSize int
	// GroupActiveFanoutInterval throttles subscriber fanout per group channel.
	GroupActiveFanoutInterval time.Duration
	// GroupActiveFanoutMaxSubscribers caps subscribers touched per group fanout; zero disables group fanout.
	GroupActiveFanoutMaxSubscribers int
	// SubscriberPageSize controls subscriber scan page size in the async worker.
	SubscriberPageSize int
	// Deprecated: retained for app wiring compatibility while durable projection tuning is removed.
	DirtyLimit int
	// Deprecated: retained for app wiring compatibility while durable projection tuning is removed.
	ColdThreshold time.Duration
	Now           func() time.Time
	Async         func(func())
	Logger        wklog.Logger
}

type projector struct {
	store                           ProjectorStore
	flushInterval                   time.Duration
	activeHintQueueSize             int
	groupActiveFanoutInterval       time.Duration
	groupActiveFanoutMaxSubscribers int
	subscriberPageSize              int
	now                             func() time.Time
	async                           func(func())
	logger                          wklog.Logger

	flushMu sync.Mutex
	flushWG sync.WaitGroup
	mu      sync.Mutex
	pending map[metadb.ConversationKey]channel.Message
	// lastGroupFanoutAt tracks the last attempted fanout time per group channel.
	lastGroupFanoutAt map[metadb.ConversationKey]time.Time
	workerRunning     bool
	running           bool
	flushCtx          context.Context
	flushCancel       context.CancelFunc
	stopCh            chan struct{}
	doneCh            chan struct{}
}

func NewProjector(opts ProjectorOptions) Projector {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	if opts.ActiveHintQueueSize <= 0 {
		opts.ActiveHintQueueSize = defaultProjectorActiveHintQueueSize
	}
	if opts.GroupActiveFanoutInterval <= 0 {
		opts.GroupActiveFanoutInterval = defaultProjectorGroupActiveFanoutInterval
	}
	if opts.SubscriberPageSize <= 0 {
		opts.SubscriberPageSize = defaultSubscriberPageSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Async == nil {
		opts.Async = func(fn func()) { go fn() }
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}

	return &projector{
		store:                           opts.Store,
		flushInterval:                   opts.FlushInterval,
		activeHintQueueSize:             opts.ActiveHintQueueSize,
		groupActiveFanoutInterval:       opts.GroupActiveFanoutInterval,
		groupActiveFanoutMaxSubscribers: opts.GroupActiveFanoutMaxSubscribers,
		subscriberPageSize:              opts.SubscriberPageSize,
		now:                             opts.Now,
		async:                           opts.Async,
		logger:                          opts.Logger,
		pending:                         make(map[metadb.ConversationKey]channel.Message),
		lastGroupFanoutAt:               make(map[metadb.ConversationKey]time.Time),
	}
}

func (p *projector) Start() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return nil
	}

	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.running = true
	go p.run(p.stopCh, p.doneCh)
	return nil
}

func (p *projector) Stop() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	if !p.running {
		cancel := p.flushCancel
		p.flushCtx = nil
		p.flushCancel = nil
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		p.flushWG.Wait()
		return p.Flush(context.Background())
	}
	stopCh := p.stopCh
	doneCh := p.doneCh
	cancel := p.flushCancel
	p.running = false
	p.flushCtx = nil
	p.flushCancel = nil
	p.stopCh = nil
	p.doneCh = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	close(stopCh)
	<-doneCh
	p.flushWG.Wait()
	return p.Flush(context.Background())
}

func (p *projector) SubmitCommitted(_ context.Context, msg channel.Message) error {
	if p == nil || p.store == nil {
		return nil
	}
	if runtimechannelid.IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce {
		return nil
	}
	if !p.enqueuePending(msg) {
		return nil
	}
	p.scheduleFlush()
	return nil
}

func (p *projector) enqueuePending(msg channel.Message) bool {
	key := metadb.ConversationKey{ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType)}
	p.mu.Lock()
	defer p.mu.Unlock()

	if current, ok := p.pending[key]; ok {
		if messageNewer(msg, current) {
			p.pending[key] = msg
		}
		return true
	}
	if len(p.pending) >= p.activeHintQueueSize {
		return false
	}
	p.pending[key] = msg
	return true
}

func (p *projector) scheduleFlush() {
	p.mu.Lock()
	if p.workerRunning {
		p.mu.Unlock()
		return
	}
	ctx := p.ensureFlushContextLocked()
	p.workerRunning = true
	p.flushWG.Add(1)
	p.mu.Unlock()

	go p.async(func() {
		defer p.flushWG.Done()
		defer func() {
			p.mu.Lock()
			p.workerRunning = false
			hasPending := len(p.pending) > 0
			p.mu.Unlock()
			if hasPending {
				p.scheduleFlush()
			}
		}()
		if err := p.Flush(ctx); err != nil {
			p.logger.Warn("conversation active hint flush failed", wklog.Error(err))
		}
	})
}

func (p *projector) ensureFlushContextLocked() context.Context {
	if p.flushCtx == nil || p.flushCancel == nil {
		p.flushCtx, p.flushCancel = context.WithCancel(context.Background())
	}
	return p.flushCtx
}

func (p *projector) Flush(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if p.store == nil {
		_ = p.drainPending()
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	items := p.drainPending()
	var result error
	for _, item := range items {
		if err := p.flushMessage(ctx, item); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (p *projector) drainPending() []channel.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) == 0 {
		return nil
	}
	items := make([]channel.Message, 0, len(p.pending))
	for key, msg := range p.pending {
		items = append(items, msg)
		delete(p.pending, key)
	}
	return items
}

func (p *projector) flushMessage(ctx context.Context, msg channel.Message) error {
	if msg.ChannelType == frame.ChannelTypePerson {
		hints, ok := personConversationActiveHints(msg)
		if !ok {
			return nil
		}
		return p.store.SubmitUserConversationActiveHints(ctx, hints)
	}
	return p.flushGroupMessage(ctx, msg)
}

func (p *projector) flushGroupMessage(ctx context.Context, msg channel.Message) error {
	if p.groupActiveFanoutMaxSubscribers <= 0 {
		return nil
	}
	key := metadb.ConversationKey{ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType)}
	now := p.now()
	if !p.allowGroupFanout(key, now) {
		return nil
	}

	remaining := p.groupActiveFanoutMaxSubscribers
	cursor := ""
	activeAt := activeAtFromMessage(msg)
	for remaining > 0 {
		pageLimit := p.subscriberPageSize
		if pageLimit > remaining {
			pageLimit = remaining
		}
		uids, nextCursor, done, err := p.store.ListChannelSubscribers(ctx, msg.ChannelID, int64(msg.ChannelType), cursor, pageLimit)
		if err != nil {
			return err
		}
		if len(uids) > 0 {
			hints := make([]metadb.UserConversationActiveHint, 0, len(uids))
			for _, uid := range uids {
				hints = append(hints, metadb.UserConversationActiveHint{
					UID: uid, ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAt, MessageSeq: msg.MessageSeq,
				})
			}
			if err := p.store.SubmitUserConversationActiveHints(ctx, hints); err != nil {
				return err
			}
			remaining -= len(uids)
		}
		if done || len(uids) == 0 || remaining <= 0 {
			return nil
		}
		cursor = nextCursor
	}
	return nil
}

func (p *projector) allowGroupFanout(key metadb.ConversationKey, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	last, ok := p.lastGroupFanoutAt[key]
	if ok && now.Sub(last) < p.groupActiveFanoutInterval {
		return false
	}
	if !ok && len(p.lastGroupFanoutAt) >= p.activeHintQueueSize {
		p.evictOldestGroupFanoutLocked()
	}
	p.lastGroupFanoutAt[key] = now
	return true
}

func (p *projector) evictOldestGroupFanoutLocked() {
	var oldestKey metadb.ConversationKey
	var oldest time.Time
	set := false
	for key, at := range p.lastGroupFanoutAt {
		if !set || at.Before(oldest) {
			oldestKey = key
			oldest = at
			set = true
		}
	}
	if set {
		delete(p.lastGroupFanoutAt, oldestKey)
	}
}

func (p *projector) run(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := p.Flush(p.currentFlushContext()); err != nil {
				p.logger.Warn("conversation active hint flush failed", wklog.Error(err))
			}
		case <-stopCh:
			return
		}
	}
}

func (p *projector) currentFlushContext() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureFlushContextLocked()
}

func personConversationActiveHints(msg channel.Message) ([]metadb.UserConversationActiveHint, bool) {
	left, right, err := runtimechannelid.DecodePersonChannel(msg.ChannelID)
	if err != nil {
		return nil, false
	}
	activeAt := activeAtFromMessage(msg)
	return []metadb.UserConversationActiveHint{
		{UID: left, ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAt, MessageSeq: msg.MessageSeq},
		{UID: right, ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAt, MessageSeq: msg.MessageSeq},
	}, true
}

func activeAtFromMessage(msg channel.Message) int64 {
	return time.Unix(int64(msg.Timestamp), 0).UnixNano()
}

func messageNewer(left, right channel.Message) bool {
	if left.MessageSeq != 0 && right.MessageSeq != left.MessageSeq {
		return left.MessageSeq > right.MessageSeq
	}
	if left.Timestamp != right.Timestamp {
		return left.Timestamp > right.Timestamp
	}
	return left.ClientMsgNo > right.ClientMsgNo
}
