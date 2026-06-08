package app

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultConversationProjectorFlushInterval  = 100 * time.Millisecond
	defaultConversationProjectorShardCount     = 64
	defaultConversationProjectorMaxDirtyEvents = 100000
)

const (
	conversationProjectorResultAccepted  = "accepted"
	conversationProjectorResultCoalesced = "coalesced"
	conversationProjectorResultDropped   = "dropped"
	conversationProjectorResultIgnored   = "ignored"
	conversationProjectorResultOK        = "ok"
	conversationProjectorResultError     = "error"
)

type conversationProjectorOptions struct {
	// store persists coalesced UID-owned conversation rows outside the foreground send path.
	store conversationusecase.ConversationBatchStore
	// members classifies non-person channels for dense or sparse projection.
	members conversationusecase.MemberSource
	// observer receives low-cardinality projector pressure and flush observations.
	observer conversationProjectorObserver
	// smallGroupFanoutLimit bounds dense fanout for ordinary channels.
	smallGroupFanoutLimit int
	// maxDirtyEvents bounds unflushed committed-message keys retained in memory.
	maxDirtyEvents int
	// flushInterval controls the background flush cadence.
	flushInterval time.Duration
	// shardCount bounds lock contention when many channels are updated concurrently.
	shardCount int
}

type conversationProjectorObserver interface {
	SetConversationProjectorDirty(conversationProjectorDirtyEvent)
	ObserveConversationProjectorSubmit(conversationProjectorSubmitEvent)
	ObserveConversationProjectorFlush(conversationProjectorFlushEvent)
	ObserveConversationProjectorMemberClassify(conversationProjectorMemberClassifyEvent)
	ObserveConversationProjectorWrite(conversationProjectorWriteEvent)
}

// conversationProjectorDirtyEvent reports current dirty-key pressure.
type conversationProjectorDirtyEvent struct {
	DirtyKeys      int
	MaxDirtyEvents int
}

// conversationProjectorSubmitEvent reports foreground committed-message admission.
type conversationProjectorSubmitEvent struct {
	Result         string
	DirtyKeys      int
	MaxDirtyEvents int
}

// conversationProjectorFlushEvent reports one background projection flush.
type conversationProjectorFlushEvent struct {
	Result         string
	Duration       time.Duration
	DrainedEvents  int
	ProjectedRows  int
	DenseEvents    int
	SparseEvents   int
	RequeuedEvents int
}

// conversationProjectorMemberClassifyEvent reports one group member-classification lookup.
type conversationProjectorMemberClassifyEvent struct {
	Result   string
	CacheHit bool
}

// conversationProjectorWriteEvent reports one durable projector write attempt.
type conversationProjectorWriteEvent struct {
	Phase    string
	Result   string
	Duration time.Duration
	Rows     int
}

// conversationProjector coalesces committed messages before durable conversation-row flush.
type conversationProjector struct {
	store                 conversationusecase.ConversationBatchStore
	members               conversationusecase.MemberSource
	observer              conversationProjectorObserver
	smallGroupFanoutLimit int
	maxDirtyEvents        int
	flushInterval         time.Duration
	shards                []conversationProjectorShard
	dirtyEvents           atomic.Int64

	flushMu   sync.Mutex
	runMu     sync.Mutex
	started   bool
	stopCh    chan struct{}
	doneCh    chan struct{}
	runCancel func()
}

type conversationProjectorShard struct {
	mu     sync.Mutex
	events map[conversationProjectorKey]messageevents.MessageCommitted
}

type conversationProjectorKey struct {
	channelID   string
	channelType uint8
	fromUID     string
}

func newConversationProjector(opts conversationProjectorOptions) *conversationProjector {
	if opts.flushInterval <= 0 {
		opts.flushInterval = defaultConversationProjectorFlushInterval
	}
	if opts.shardCount <= 0 {
		opts.shardCount = defaultConversationProjectorShardCount
	}
	if opts.maxDirtyEvents <= 0 {
		opts.maxDirtyEvents = defaultConversationProjectorMaxDirtyEvents
	}
	projector := &conversationProjector{
		store:                 opts.store,
		members:               opts.members,
		observer:              opts.observer,
		smallGroupFanoutLimit: opts.smallGroupFanoutLimit,
		maxDirtyEvents:        opts.maxDirtyEvents,
		flushInterval:         opts.flushInterval,
		shards:                make([]conversationProjectorShard, opts.shardCount),
	}
	for i := range projector.shards {
		projector.shards[i].events = make(map[conversationProjectorKey]messageevents.MessageCommitted)
	}
	projector.observeDirty()
	return projector
}

func (p *conversationProjector) Start(context.Context) error {
	if p == nil || p.store == nil {
		return nil
	}
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if p.started {
		return nil
	}
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	runCtx, cancel := context.WithCancel(context.Background())
	p.runCancel = cancel
	p.started = true
	go p.run(runCtx, p.stopCh, p.doneCh)
	return nil
}

func (p *conversationProjector) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.runMu.Lock()
	if !p.started {
		p.runMu.Unlock()
		return p.Flush(ctx)
	}
	stopCh := p.stopCh
	doneCh := p.doneCh
	cancel := p.runCancel
	p.started = false
	p.runCancel = nil
	if cancel != nil {
		cancel()
	}
	close(stopCh)
	p.runMu.Unlock()

	select {
	case <-doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.Flush(ctx)
}

func (p *conversationProjector) run(ctx context.Context, stopCh <-chan struct{}, doneCh chan<- struct{}) {
	ticker := time.NewTicker(p.flushInterval)
	defer func() {
		ticker.Stop()
		close(doneCh)
	}()
	for {
		select {
		case <-ticker.C:
			_ = p.Flush(ctx)
		case <-stopCh:
			return
		}
	}
}

func (p *conversationProjector) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	if p == nil {
		return nil
	}
	if p.store == nil || event.ChannelID == "" || event.ChannelType == 0 {
		p.observeSubmit(conversationProjectorResultIgnored)
		return nil
	}
	event.Payload = nil
	event.MessageScopedUIDs = nil
	p.observeSubmit(p.merge(event))
	return nil
}

func (p *conversationProjector) Flush(ctx context.Context) error {
	if p == nil || p.store == nil {
		return nil
	}
	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	startedAt := time.Now()
	events := p.drain()
	if len(events) == 0 {
		return nil
	}
	members := newConversationProjectorMemberCache(p.members, p.observer)
	var attempts []conversationProjectionAttempt
	var failedEvents []messageevents.MessageCommitted
	var firstErr error
	denseEvents := 0
	sparseEvents := 0
	for _, event := range events {
		collector := newConversationStateCollector()
		projector := conversationusecase.NewProjector(conversationusecase.ProjectorOptions{
			Store:                 collector,
			Members:               members,
			SmallGroupFanoutLimit: p.smallGroupFanoutLimit,
		})
		if err := projector.HandleCommitted(ctx, event); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failedEvents = append(failedEvents, event)
			continue
		}
		if states := collector.states(); len(states) > 0 {
			switch conversationProjectionMode(states) {
			case "sparse":
				sparseEvents++
			default:
				denseEvents++
			}
			attempts = append(attempts, conversationProjectionAttempt{
				event:  event,
				states: states,
			})
		}
	}
	states := collectProjectionAttemptStates(attempts)
	if len(states) == 0 {
		p.mergeEvents(failedEvents)
		p.observeFlush(conversationProjectorFlushEvent{
			Result:         conversationProjectorFlushResult(firstErr),
			Duration:       time.Since(startedAt),
			DrainedEvents:  len(events),
			DenseEvents:    denseEvents,
			SparseEvents:   sparseEvents,
			RequeuedEvents: len(failedEvents),
		})
		return firstErr
	}
	writeStartedAt := time.Now()
	if err := p.store.UpsertUserConversationStatesBatch(ctx, states); err != nil {
		p.observeWrite(conversationProjectorWriteEvent{
			Phase:    "batch",
			Result:   conversationProjectorResultError,
			Duration: time.Since(writeStartedAt),
			Rows:     len(states),
		})
		retryErr, retryFailed := p.flushProjectionAttemptsIndividually(ctx, attempts)
		if retryErr != nil && firstErr == nil {
			firstErr = retryErr
		}
		failedEvents = append(failedEvents, retryFailed...)
	} else {
		p.observeWrite(conversationProjectorWriteEvent{
			Phase:    "batch",
			Result:   conversationProjectorResultOK,
			Duration: time.Since(writeStartedAt),
			Rows:     len(states),
		})
	}
	p.mergeEvents(failedEvents)
	p.observeFlush(conversationProjectorFlushEvent{
		Result:         conversationProjectorFlushResult(firstErr),
		Duration:       time.Since(startedAt),
		DrainedEvents:  len(events),
		ProjectedRows:  len(states),
		DenseEvents:    denseEvents,
		SparseEvents:   sparseEvents,
		RequeuedEvents: len(failedEvents),
	})
	return firstErr
}

type conversationProjectionAttempt struct {
	event  messageevents.MessageCommitted
	states []metadb.UserConversationState
}

func collectProjectionAttemptStates(attempts []conversationProjectionAttempt) []metadb.UserConversationState {
	collector := newConversationStateCollector()
	for _, attempt := range attempts {
		_ = collector.UpsertUserConversationStatesBatch(context.Background(), attempt.states)
	}
	return collector.states()
}

func (p *conversationProjector) flushProjectionAttemptsIndividually(ctx context.Context, attempts []conversationProjectionAttempt) (error, []messageevents.MessageCommitted) {
	var firstErr error
	var failed []messageevents.MessageCommitted
	for _, attempt := range attempts {
		if len(attempt.states) == 0 {
			continue
		}
		startedAt := time.Now()
		if err := p.store.UpsertUserConversationStatesBatch(ctx, attempt.states); err != nil {
			p.observeWrite(conversationProjectorWriteEvent{
				Phase:    "fallback",
				Result:   conversationProjectorResultError,
				Duration: time.Since(startedAt),
				Rows:     len(attempt.states),
			})
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, attempt.event)
			continue
		}
		p.observeWrite(conversationProjectorWriteEvent{
			Phase:    "fallback",
			Result:   conversationProjectorResultOK,
			Duration: time.Since(startedAt),
			Rows:     len(attempt.states),
		})
	}
	return firstErr, failed
}

type conversationProjectorMemberCache struct {
	next     conversationusecase.MemberSource
	observer conversationProjectorObserver
	mu       sync.Mutex
	rows     map[conversationProjectorMemberKey]conversationProjectorMemberResult
}

type conversationProjectorMemberKey struct {
	channelID   string
	channelType int64
	limit       int
}

type conversationProjectorMemberResult struct {
	class conversationusecase.MemberClass
	err   error
}

func newConversationProjectorMemberCache(next conversationusecase.MemberSource, observer conversationProjectorObserver) *conversationProjectorMemberCache {
	return &conversationProjectorMemberCache{
		next:     next,
		observer: observer,
		rows:     map[conversationProjectorMemberKey]conversationProjectorMemberResult{},
	}
}

func (c *conversationProjectorMemberCache) ClassifyMembers(ctx context.Context, channelID string, channelType int64, limit int) (conversationusecase.MemberClass, error) {
	if c == nil || c.next == nil {
		return conversationusecase.MemberClass{}, conversationusecase.ErrProjectorConfig
	}
	key := conversationProjectorMemberKey{channelID: channelID, channelType: channelType, limit: limit}
	c.mu.Lock()
	if result, ok := c.rows[key]; ok {
		c.mu.Unlock()
		c.observeMemberClassify(result.err, true)
		return result.class, result.err
	}
	c.mu.Unlock()

	class, err := c.next.ClassifyMembers(ctx, channelID, channelType, limit)
	result := conversationProjectorMemberResult{class: cloneMemberClass(class), err: err}
	c.mu.Lock()
	c.rows[key] = result
	c.mu.Unlock()
	c.observeMemberClassify(err, false)
	return result.class, result.err
}

func (c *conversationProjectorMemberCache) observeMemberClassify(err error, cacheHit bool) {
	if c == nil || c.observer == nil {
		return
	}
	result := conversationProjectorResultOK
	if err != nil {
		result = conversationProjectorResultError
	}
	c.observer.ObserveConversationProjectorMemberClassify(conversationProjectorMemberClassifyEvent{Result: result, CacheHit: cacheHit})
}

func cloneMemberClass(class conversationusecase.MemberClass) conversationusecase.MemberClass {
	class.Members = append([]conversationusecase.Member(nil), class.Members...)
	return class
}

func (p *conversationProjector) drain() []messageevents.MessageCommitted {
	var events []messageevents.MessageCommitted
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		for key, event := range shard.events {
			events = append(events, event)
			delete(shard.events, key)
			p.dirtyEvents.Add(-1)
		}
		shard.mu.Unlock()
	}
	p.observeDirty()
	sort.Slice(events, func(i, j int) bool {
		if events[i].ChannelID != events[j].ChannelID {
			return events[i].ChannelID < events[j].ChannelID
		}
		if events[i].ChannelType != events[j].ChannelType {
			return events[i].ChannelType < events[j].ChannelType
		}
		return events[i].FromUID < events[j].FromUID
	})
	return events
}

func (p *conversationProjector) mergeEvents(events []messageevents.MessageCommitted) {
	for _, event := range events {
		p.merge(event)
	}
}

func (p *conversationProjector) merge(event messageevents.MessageCommitted) string {
	if p == nil || len(p.shards) == 0 {
		return conversationProjectorResultIgnored
	}
	key := conversationProjectorKey{channelID: event.ChannelID, channelType: event.ChannelType, fromUID: event.FromUID}
	shard := &p.shards[p.shardIndex(key)]
	shard.mu.Lock()
	existing, ok := shard.events[key]
	if !ok && !p.tryReserveDirtyEvent() {
		shard.mu.Unlock()
		return conversationProjectorResultDropped
	}
	if !ok || committedEventAfter(event, existing) {
		shard.events[key] = event
	}
	shard.mu.Unlock()
	if !ok {
		p.observeDirty()
		return conversationProjectorResultAccepted
	}
	return conversationProjectorResultCoalesced
}

func (p *conversationProjector) tryReserveDirtyEvent() bool {
	limit := int64(p.maxDirtyEvents)
	for {
		current := p.dirtyEvents.Load()
		if current >= limit {
			return false
		}
		if p.dirtyEvents.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func conversationProjectionMode(states []metadb.UserConversationState) string {
	for _, state := range states {
		if state.SparseActive {
			return "sparse"
		}
	}
	return "dense"
}

func conversationProjectorFlushResult(err error) string {
	if err != nil {
		return conversationProjectorResultError
	}
	return conversationProjectorResultOK
}

func (p *conversationProjector) observeDirty() {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.SetConversationProjectorDirty(conversationProjectorDirtyEvent{
		DirtyKeys:      int(p.dirtyEvents.Load()),
		MaxDirtyEvents: p.maxDirtyEvents,
	})
}

func (p *conversationProjector) observeSubmit(result string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectorSubmit(conversationProjectorSubmitEvent{
		Result:         result,
		DirtyKeys:      int(p.dirtyEvents.Load()),
		MaxDirtyEvents: p.maxDirtyEvents,
	})
}

func (p *conversationProjector) observeFlush(event conversationProjectorFlushEvent) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectorFlush(event)
}

func (p *conversationProjector) observeWrite(event conversationProjectorWriteEvent) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectorWrite(event)
}

func committedEventAfter(next, existing messageevents.MessageCommitted) bool {
	if next.MessageSeq != existing.MessageSeq {
		return next.MessageSeq > existing.MessageSeq
	}
	return next.ServerTimestampMS >= existing.ServerTimestampMS
}

func (p *conversationProjector) shardIndex(key conversationProjectorKey) uint32 {
	hash := uint32(2166136261)
	for i := 0; i < len(key.channelID); i++ {
		hash ^= uint32(key.channelID[i])
		hash *= 16777619
	}
	hash ^= uint32(key.channelType)
	hash *= 16777619
	for i := 0; i < len(key.fromUID); i++ {
		hash ^= uint32(key.fromUID[i])
		hash *= 16777619
	}
	return hash % uint32(len(p.shards))
}

type conversationStateCollector struct {
	rows map[conversationStateCollectorKey]metadb.UserConversationState
}

type conversationStateCollectorKey struct {
	uid         string
	channelID   string
	channelType int64
}

func newConversationStateCollector() *conversationStateCollector {
	return &conversationStateCollector{rows: map[conversationStateCollectorKey]metadb.UserConversationState{}}
}

func (c *conversationStateCollector) UpsertUserConversationStatesBatch(_ context.Context, states []metadb.UserConversationState) error {
	if c == nil {
		return nil
	}
	for _, state := range states {
		key := conversationStateCollectorKey{uid: state.UID, channelID: state.ChannelID, channelType: state.ChannelType}
		if existing, ok := c.rows[key]; ok {
			state = mergeConversationProjectionState(existing, state)
		}
		c.rows[key] = state
	}
	return nil
}

func (c *conversationStateCollector) states() []metadb.UserConversationState {
	if c == nil || len(c.rows) == 0 {
		return nil
	}
	out := make([]metadb.UserConversationState, 0, len(c.rows))
	for _, row := range c.rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UID != out[j].UID {
			return out[i].UID < out[j].UID
		}
		if out[i].ChannelID != out[j].ChannelID {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].ChannelType < out[j].ChannelType
	})
	return out
}

func mergeConversationProjectionState(existing, next metadb.UserConversationState) metadb.UserConversationState {
	next.UID = existing.UID
	next.ChannelID = existing.ChannelID
	next.ChannelType = existing.ChannelType
	if next.ReadSeq < existing.ReadSeq {
		next.ReadSeq = existing.ReadSeq
	}
	if next.DeletedToSeq < existing.DeletedToSeq {
		next.DeletedToSeq = existing.DeletedToSeq
	}
	if next.ActiveAt < existing.ActiveAt {
		next.ActiveAt = existing.ActiveAt
	}
	if next.UpdatedAt < existing.UpdatedAt {
		next.UpdatedAt = existing.UpdatedAt
		next.SparseActive = existing.SparseActive
	}
	return next
}

type committedSinkGroup []message.CommittedSink

func combineCommittedSinks(sinks ...message.CommittedSink) message.CommittedSink {
	group := committedSinkGroup{}
	for _, sink := range sinks {
		if sink != nil {
			group = append(group, sink)
		}
	}
	if len(group) == 0 {
		return nil
	}
	return group
}

func (g committedSinkGroup) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	var firstErr error
	for _, sink := range g {
		if sink == nil {
			continue
		}
		if err := sink.Submit(ctx, event.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
