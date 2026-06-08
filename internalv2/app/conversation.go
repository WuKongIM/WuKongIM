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
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
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

type conversationPatchProjector interface {
	ProjectActivePatches(context.Context, messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error)
}

type conversationPatchAuthority interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

type conversationAuthorityFlusher interface {
	Flush(context.Context) error
}

type conversationAuthorityCommittedOptions struct {
	// Projector derives UID-owned active patches from committed message events.
	Projector conversationPatchProjector
	// Authority routes active patches to the current UID authority.
	Authority conversationPatchAuthority
	// Flusher persists any local authority cache rows during explicit flush/stop.
	Flusher conversationAuthorityFlusher
	// LocalAuthority tracks route-authority state for this node.
	LocalAuthority *conversationAuthority
	// LocalNodeID identifies this node when applying route-authority events.
	LocalNodeID uint64
	// Initial returns the currently known route authorities when the sink starts.
	Initial func() []clusterv2.RouteAuthority
	// Watch creates the route-authority event stream when the sink starts.
	Watch func() <-chan clusterv2.RouteAuthorityEvent
	// AdmissionTimeout bounds foreground cache admission after a durable commit.
	AdmissionTimeout time.Duration
	// HandoffTimeout bounds local authority drain during route-authority changes.
	HandoffTimeout time.Duration
	// RPCTimeout bounds one authority admission call.
	RPCTimeout time.Duration
	// RPCBatchRows limits active patches sent in one admission call.
	RPCBatchRows int
	// RPCConcurrency limits concurrent admission calls for one committed event.
	RPCConcurrency int
	// Observer receives low-cardinality compatibility observations.
	Observer conversationProjectorObserver
	// AuthorityObserver receives low-cardinality authority admission observations.
	AuthorityObserver conversationAuthorityObserver
}

type conversationAuthorityPendingKey struct {
	// uid owns the pending active patch.
	uid string
	// channelID identifies the pending conversation row.
	channelID string
	// channelType identifies the pending conversation namespace.
	channelType int64
}

type conversationAuthorityCommittedSink struct {
	projector         conversationPatchProjector
	authority         conversationPatchAuthority
	flusher           conversationAuthorityFlusher
	localAuthority    *conversationAuthority
	localNodeID       uint64
	initial           func() []clusterv2.RouteAuthority
	watch             func() <-chan clusterv2.RouteAuthorityEvent
	admissionTimeout  time.Duration
	handoffTimeout    time.Duration
	rpcTimeout        time.Duration
	rpcBatchRows      int
	rpcConcurrency    int
	observer          conversationProjectorObserver
	authorityObserver conversationAuthorityObserver

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	latest map[uint16]conversationusecase.RouteTarget
	// pending keeps patches that were derived after a durable append but not admitted yet.
	pending map[conversationAuthorityPendingKey]conversationusecase.ActivePatch
}

func newConversationAuthorityCommittedSink(opts conversationAuthorityCommittedOptions) *conversationAuthorityCommittedSink {
	if opts.AdmissionTimeout <= 0 {
		opts.AdmissionTimeout = 500 * time.Millisecond
	}
	if opts.HandoffTimeout <= 0 {
		opts.HandoffTimeout = 3 * time.Second
	}
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = 500 * time.Millisecond
	}
	if opts.RPCBatchRows <= 0 {
		opts.RPCBatchRows = 512
	}
	if opts.RPCConcurrency <= 0 {
		opts.RPCConcurrency = 16
	}
	return &conversationAuthorityCommittedSink{
		projector:         opts.Projector,
		authority:         opts.Authority,
		flusher:           opts.Flusher,
		localAuthority:    opts.LocalAuthority,
		localNodeID:       opts.LocalNodeID,
		initial:           opts.Initial,
		watch:             opts.Watch,
		admissionTimeout:  opts.AdmissionTimeout,
		handoffTimeout:    opts.HandoffTimeout,
		rpcTimeout:        opts.RPCTimeout,
		rpcBatchRows:      opts.RPCBatchRows,
		rpcConcurrency:    opts.RPCConcurrency,
		observer:          opts.Observer,
		authorityObserver: opts.AuthorityObserver,
		latest:            make(map[uint16]conversationusecase.RouteTarget),
		pending:           make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch),
	}
}

func (s *conversationAuthorityCommittedSink) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	var events <-chan clusterv2.RouteAuthorityEvent
	if s.watch != nil {
		events = s.watch()
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return nil
	}
	s.cancel = cancel
	if s.latest == nil {
		s.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	if events != nil {
		s.wg.Add(1)
		go s.watchRouteAuthorities(runCtx, events)
	}
	s.mu.Unlock()
	s.applyRouteAuthorities(runCtx, s.initialAuthorities())
	return nil
}

func (s *conversationAuthorityCommittedSink) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	return s.Flush(ctx)
}

func (s *conversationAuthorityCommittedSink) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if s == nil || s.projector == nil || s.authority == nil {
		s.observeSubmit(conversationProjectorResultIgnored)
		s.observeAuthorityAdmit(conversationAuthorityResultIgnored)
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	admissionCtx, cancel := context.WithTimeout(ctx, s.admissionTimeout)
	defer cancel()
	patches, err := s.projector.ProjectActivePatches(admissionCtx, event)
	if err != nil {
		s.observeSubmit(conversationProjectorResultError)
		s.observeAuthorityAdmit(conversationAuthorityResultFromError(err, conversationAuthorityResultError))
		return err
	}
	if len(patches) == 0 {
		s.observeSubmit(conversationProjectorResultIgnored)
		s.observeAuthorityAdmit(conversationAuthorityResultIgnored)
		return nil
	}
	attempt := s.pendingSnapshotWith(patches)
	if err := s.admitBatches(admissionCtx, attempt); err != nil {
		s.rememberPending(attempt)
		s.observeSubmit(conversationProjectorResultError)
		s.observeAuthorityAdmit(conversationAuthorityResultFromError(err, conversationAuthorityResultError))
		return err
	}
	s.clearPending(attempt)
	s.observeSubmit(conversationProjectorResultAccepted)
	s.observeAuthorityAdmit(conversationAuthorityResultOK)
	return nil
}

func (s *conversationAuthorityCommittedSink) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := time.Now()
	err := s.retryPending(ctx)
	if s.flusher != nil {
		if flushErr := s.flusher.Flush(ctx); err == nil {
			err = flushErr
		}
	}
	result := conversationProjectorResultOK
	if err != nil {
		result = conversationProjectorResultError
	}
	s.observeFlush(conversationProjectorFlushEvent{Result: result, Duration: time.Since(startedAt)})
	return err
}

func (s *conversationAuthorityCommittedSink) retryPending(ctx context.Context) error {
	if s == nil {
		return nil
	}
	attempt := s.pendingSnapshotWith(nil)
	if len(attempt) == 0 {
		return nil
	}
	if err := s.admitBatches(ctx, attempt); err != nil {
		s.rememberPending(attempt)
		return err
	}
	s.clearPending(attempt)
	return nil
}

func (s *conversationAuthorityCommittedSink) admitBatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	batches := conversationActivePatchBatches(patches, s.rpcBatchRows)
	if len(batches) == 0 {
		return nil
	}
	if s.rpcConcurrency <= 1 || len(batches) == 1 {
		for _, batch := range batches {
			if err := s.admitBatch(ctx, batch); err != nil {
				return err
			}
		}
		return nil
	}
	workers := s.rpcConcurrency
	if workers > len(batches) {
		workers = len(batches)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan []conversationusecase.ActivePatch)
	var wg sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	setErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				if err := s.admitBatch(workCtx, batch); err != nil {
					setErr(err)
				}
			}
		}()
	}
send:
	for _, batch := range batches {
		select {
		case <-workCtx.Done():
			break send
		case jobs <- batch:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *conversationAuthorityCommittedSink) admitBatch(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if s.rpcTimeout <= 0 {
		return s.authority.AdmitPatches(ctx, patches)
	}
	rpcCtx, cancel := context.WithTimeout(ctx, s.rpcTimeout)
	defer cancel()
	return s.authority.AdmitPatches(rpcCtx, patches)
}

func (s *conversationAuthorityCommittedSink) pendingSnapshotWith(patches []conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(s.pending)+len(patches))
	for key, patch := range s.pending {
		merged[key] = patch
	}
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		merged[key] = mergeConversationActivePatch(merged[key], patch)
	}
	return sortedPendingConversationPatches(merged)
}

func (s *conversationAuthorityCommittedSink) rememberPending(patches []conversationusecase.ActivePatch) {
	if s == nil || len(patches) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch)
	}
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		s.pending[key] = mergeConversationActivePatch(s.pending[key], patch)
	}
}

func (s *conversationAuthorityCommittedSink) clearPending(patches []conversationusecase.ActivePatch) {
	if s == nil || len(patches) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, patch := range patches {
		key := pendingConversationPatchKey(patch)
		current, ok := s.pending[key]
		if ok && conversationActivePatchDominates(patch, current) {
			delete(s.pending, key)
		}
	}
}

func pendingConversationPatchKey(patch conversationusecase.ActivePatch) conversationAuthorityPendingKey {
	return conversationAuthorityPendingKey{uid: patch.UID, channelID: patch.ChannelID, channelType: patch.ChannelType}
}

func sortedPendingConversationPatches(patches map[conversationAuthorityPendingKey]conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	keys := make([]conversationAuthorityPendingKey, 0, len(patches))
	for key := range patches {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].uid != keys[j].uid {
			return keys[i].uid < keys[j].uid
		}
		if keys[i].channelID != keys[j].channelID {
			return keys[i].channelID < keys[j].channelID
		}
		return keys[i].channelType < keys[j].channelType
	})
	out := make([]conversationusecase.ActivePatch, 0, len(keys))
	for _, key := range keys {
		out = append(out, patches[key])
	}
	return out
}

func conversationActivePatchDominates(admitted, current conversationusecase.ActivePatch) bool {
	if current.UID == "" {
		return true
	}
	return mergeConversationActivePatch(current, admitted) == admitted
}

func conversationActivePatchBatches(patches []conversationusecase.ActivePatch, batchRows int) [][]conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	if batchRows <= 0 || batchRows >= len(patches) {
		return [][]conversationusecase.ActivePatch{append([]conversationusecase.ActivePatch(nil), patches...)}
	}
	batches := make([][]conversationusecase.ActivePatch, 0, (len(patches)+batchRows-1)/batchRows)
	for start := 0; start < len(patches); start += batchRows {
		end := start + batchRows
		if end > len(patches) {
			end = len(patches)
		}
		batches = append(batches, append([]conversationusecase.ActivePatch(nil), patches[start:end]...))
	}
	return batches
}

func (s *conversationAuthorityCommittedSink) initialAuthorities() []clusterv2.RouteAuthority {
	if s == nil || s.initial == nil {
		return nil
	}
	return s.initial()
}

func (s *conversationAuthorityCommittedSink) watchRouteAuthorities(ctx context.Context, events <-chan clusterv2.RouteAuthorityEvent) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			for _, authority := range event.Authorities {
				s.handleRouteAuthority(ctx, authority)
			}
		}
	}
}

func (s *conversationAuthorityCommittedSink) applyRouteAuthorities(ctx context.Context, authorities []clusterv2.RouteAuthority) {
	if s == nil || s.localAuthority == nil {
		return
	}
	for _, authority := range authorities {
		s.handleRouteAuthority(ctx, authority)
	}
}

func (s *conversationAuthorityCommittedSink) handleRouteAuthority(ctx context.Context, authority clusterv2.RouteAuthority) {
	if s == nil || s.localAuthority == nil {
		return
	}
	target := conversationRouteTarget(authority)
	previous, hadPrevious, accepted := s.acceptRouteAuthorityTarget(target)
	if !accepted {
		return
	}
	switch {
	case target.LeaderNodeID == s.localNodeID:
		s.localAuthority.markActive(target)
	case target.LeaderNodeID == 0:
		if hadPrevious && s.localAuthorityCapable(previous) {
			s.drainAuthorityTarget(ctx, previous)
		}
		s.localAuthority.markWarming(target)
	default:
		if hadPrevious && s.localAuthorityCapable(previous) {
			s.drainAuthorityTarget(ctx, previous)
		}
	}
}

func (s *conversationAuthorityCommittedSink) acceptRouteAuthorityTarget(target conversationusecase.RouteTarget) (conversationusecase.RouteTarget, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		s.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	current, ok := s.latest[target.HashSlot]
	if ok && !conversationAuthorityRouteTargetNewer(target, current) {
		return current, true, false
	}
	s.latest[target.HashSlot] = target
	return current, ok, true
}

func conversationAuthorityRouteTargetNewer(next, current conversationusecase.RouteTarget) bool {
	if next.RouteRevision != current.RouteRevision {
		return next.RouteRevision > current.RouteRevision
	}
	return next.AuthorityEpoch > current.AuthorityEpoch
}

func (s *conversationAuthorityCommittedSink) localAuthorityCapable(target conversationusecase.RouteTarget) bool {
	return target.LeaderNodeID == 0 || target.LeaderNodeID == s.localNodeID
}

func (s *conversationAuthorityCommittedSink) drainAuthorityTarget(ctx context.Context, target conversationusecase.RouteTarget) {
	if s == nil || s.localAuthority == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	drainCtx := ctx
	var cancel context.CancelFunc
	if s.handoffTimeout > 0 {
		drainCtx, cancel = context.WithTimeout(ctx, s.handoffTimeout)
		defer cancel()
	}
	startedAt := time.Now()
	_, err := s.localAuthority.DrainAuthority(drainCtx, target)
	result := conversationProjectorResultOK
	if err != nil {
		result = conversationProjectorResultError
	}
	s.observeFlush(conversationProjectorFlushEvent{Result: result, Duration: time.Since(startedAt)})
}

func conversationRouteTarget(authority clusterv2.RouteAuthority) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}
}

func (s *conversationAuthorityCommittedSink) observeSubmit(result string) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.ObserveConversationProjectorSubmit(conversationProjectorSubmitEvent{Result: result})
}

func (s *conversationAuthorityCommittedSink) observeFlush(event conversationProjectorFlushEvent) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.ObserveConversationProjectorFlush(event)
}

func (s *conversationAuthorityCommittedSink) observeAuthorityAdmit(result string) {
	if s == nil || s.authorityObserver == nil {
		return
	}
	s.authorityObserver.ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent{Result: result})
}

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
