package cmdsync

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultProjectorQueueSize     = 1024
	defaultProjectorPageSize      = 512
	defaultProjectorFlushInterval = 200 * time.Millisecond
)

// Projector projects durable command-channel commits into UID-owned CMD sync state.
type Projector interface {
	Start() error
	Stop() error
	Flush(ctx context.Context) error
	SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error
	ProjectCommitted(ctx context.Context, event messageevents.MessageCommitted) error
}

// ProjectorStore persists projected CMD conversation state.
type ProjectorStore interface {
	UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
}

// ProjectorOptions configures durable CMD conversation projection.
type ProjectorOptions struct {
	Store         ProjectorStore
	Subscribers   delivery.SubscriberResolver
	QueueSize     int
	PageSize      int
	FlushInterval time.Duration
	Now           func() time.Time
	Async         func(func())
	Logger        wklog.Logger
}

type cmdProjector struct {
	store         ProjectorStore
	subscribers   delivery.SubscriberResolver
	queue         chan messageevents.MessageCommitted
	pageSize      int
	flushInterval time.Duration
	now           func() time.Time
	async         func(func())
	logger        wklog.Logger

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewProjector creates a bounded, best-effort durable CMD projector.
func NewProjector(opts ProjectorOptions) Projector {
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultProjectorQueueSize
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultProjectorPageSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultProjectorFlushInterval
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
	return &cmdProjector{
		store:         opts.Store,
		subscribers:   opts.Subscribers,
		queue:         make(chan messageevents.MessageCommitted, opts.QueueSize),
		pageSize:      opts.PageSize,
		flushInterval: opts.FlushInterval,
		now:           opts.Now,
		async:         opts.Async,
		logger:        opts.Logger,
	}
}

func (p *cmdProjector) Start() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	p.stopCh = stopCh
	p.doneCh = doneCh
	p.running = true
	p.mu.Unlock()

	go p.async(func() { p.run(stopCh, doneCh) })
	return nil
}

func (p *cmdProjector) Stop() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return p.Flush(context.Background())
	}
	stopCh := p.stopCh
	doneCh := p.doneCh
	p.running = false
	p.stopCh = nil
	p.doneCh = nil
	p.mu.Unlock()

	close(stopCh)
	<-doneCh
	return p.Flush(context.Background())
}

func (p *cmdProjector) run(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case event := <-p.queue:
			if err := p.project(context.Background(), event); err != nil {
				p.logger.Warn("cmd sync projection failed", wklog.Error(err))
			}
		case <-ticker.C:
			if err := p.Flush(context.Background()); err != nil {
				p.logger.Warn("cmd sync projector flush failed", wklog.Error(err))
			}
		}
	}
}

// SubmitCommitted validates and enqueues projection work without subscriber paging inline.
func (p *cmdProjector) SubmitCommitted(_ context.Context, event messageevents.MessageCommitted) error {
	if p == nil || p.store == nil {
		return nil
	}
	if !isDurableCMDProjectionMessage(event.Message) {
		return nil
	}
	cloned := event.Clone()
	select {
	case p.queue <- cloned:
	default:
		p.logger.Warn("cmd sync projection queue full; dropping event")
	}
	return nil
}

// ProjectCommitted synchronously persists one committed CMD projection.
func (p *cmdProjector) ProjectCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if p == nil || p.store == nil {
		return nil
	}
	return p.project(ctx, event.Clone())
}

// Flush drains queued projection work synchronously.
func (p *cmdProjector) Flush(ctx context.Context) error {
	if p == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-p.queue:
			if err := p.project(ctx, event); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (p *cmdProjector) project(ctx context.Context, event messageevents.MessageCommitted) error {
	msg := event.Message
	if !isDurableCMDProjectionMessage(msg) || p.store == nil {
		return nil
	}
	commandChannelID := msg.ChannelID
	if !runtimechannelid.IsCommandChannel(commandChannelID) {
		commandChannelID = runtimechannelid.ToCommandChannel(commandChannelID)
	}
	if msg.ChannelType == frame.ChannelTypeTemp && len(event.MessageScopedUIDs) == 0 {
		return nil
	}

	updatedAt := p.now().UnixNano()
	activeAt := activeAtFromMessage(msg, p.now)
	buildStates := func(uids []string) []metadb.CMDConversationState {
		states := make([]metadb.CMDConversationState, 0, len(uids))
		for _, uid := range uniqueNonEmptyStrings(uids) {
			state := metadb.CMDConversationState{
				UID:         uid,
				ChannelID:   commandChannelID,
				ChannelType: int64(msg.ChannelType),
				ActiveAt:    activeAt,
				UpdatedAt:   updatedAt,
			}
			if uid == msg.FromUID {
				state.ReadSeq = msg.MessageSeq
			}
			states = append(states, state)
		}
		return states
	}

	if len(event.MessageScopedUIDs) > 0 {
		return p.upsertStates(ctx, buildStates(event.MessageScopedUIDs))
	}
	if p.subscribers == nil {
		return nil
	}

	token, err := p.subscribers.BeginSnapshotWithRequest(ctx, channel.ChannelID{ID: commandChannelID, Type: msg.ChannelType}, delivery.SubscriberSnapshotRequest{})
	if err != nil {
		return err
	}
	cursor := ""
	for {
		uids, nextCursor, done, err := p.subscribers.NextPage(ctx, token, cursor, p.pageSize)
		if err != nil {
			return err
		}
		if err := p.upsertStates(ctx, buildStates(uids)); err != nil {
			return err
		}
		if done {
			return nil
		}
		cursor = nextCursor
	}
}

func (p *cmdProjector) upsertStates(ctx context.Context, states []metadb.CMDConversationState) error {
	if len(states) == 0 || p.store == nil {
		return nil
	}
	return p.store.UpsertCMDConversationStates(ctx, states)
}

func isDurableCMDProjectionMessage(msg channel.Message) bool {
	if msg.MessageSeq == 0 || msg.Framer.NoPersist {
		return false
	}
	return runtimechannelid.IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce
}

func activeAtFromMessage(msg channel.Message, now func() time.Time) int64 {
	if msg.Timestamp != 0 {
		return time.Unix(int64(msg.Timestamp), 0).UnixNano()
	}
	if now == nil {
		now = time.Now
	}
	return now().UnixNano()
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
