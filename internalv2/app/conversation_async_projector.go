package app

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
)

const (
	conversationProjectionResultAccepted      = "accepted"
	conversationProjectionResultCoalesced     = "coalesced"
	conversationProjectionResultDropped       = "dropped"
	conversationProjectionResultIgnored       = "ignored"
	conversationProjectionResultOK            = "ok"
	conversationProjectionResultError         = "error"
	conversationProjectionResultRouteNotReady = "route_not_ready"
	conversationProjectionResultStaleRoute    = "stale_route"
	conversationProjectionResultNotLeader     = "not_leader"
	conversationProjectionResultTimeout       = "timeout"

	conversationProjectionRetryDropCapacity = "capacity"
	conversationProjectionRetryDropAge      = "age"
	conversationProjectionRetryDropInvalid  = "invalid"
)

var _ message.CommittedSink = (*conversationAsyncProjector)(nil)
var _ message.CommittedPayloadPolicy = (*conversationAsyncProjector)(nil)

type conversationAsyncProjectorOptions struct {
	// Projector derives UID-owned active patches from committed metadata events.
	Projector conversationPatchProjector
	// Authority routes active patches to their current UID authority.
	Authority conversationPatchAuthority
	// Flusher persists local authority cache rows when the projector stops.
	Flusher conversationAuthorityFlusher
	// FlushInterval controls background projection cadence.
	FlushInterval time.Duration
	// ShardCount bounds foreground lock contention while coalescing events.
	ShardCount int
	// MaxDirtyEvents bounds unprojected event keys retained in memory.
	MaxDirtyEvents int
	// MaxRetryPatches bounds retryable active patches retained in memory.
	MaxRetryPatches int
	// RetryMaxAge bounds retry retention for active patches.
	RetryMaxAge time.Duration
	// AdmitBatchRows limits active patches in one background admission batch.
	AdmitBatchRows int
	// AdmitConcurrency limits concurrent background admission batches.
	AdmitConcurrency int
	// AdmitTimeout bounds one background admission call.
	AdmitTimeout time.Duration
	// Observer receives low-cardinality projection observations.
	Observer conversationProjectionObserver
}

type conversationProjectionObserver interface {
	SetConversationProjectionDirty(conversationProjectionDirtyEvent)
	ObserveConversationProjectionSubmit(conversationProjectionSubmitEvent)
	ObserveConversationProjectionFlush(conversationProjectionFlushEvent)
	ObserveConversationProjectionMemberClassify(conversationProjectionMemberClassifyEvent)
	ObserveConversationProjectionAuthorityAdmit(conversationProjectionAuthorityAdmitEvent)
	SetConversationProjectionRetry(conversationProjectionRetryEvent)
	ObserveConversationProjectionRetryDrop(conversationProjectionRetryDropEvent)
}

type conversationProjectionDirtyEvent struct {
	DirtyKeys      int
	MaxDirtyEvents int
}

type conversationProjectionSubmitEvent struct {
	Result string
}

type conversationProjectionFlushEvent struct {
	Result           string
	Duration         time.Duration
	DrainedEvents    int
	ProjectedPatches int
	DenseEvents      int
	SparseEvents     int
	RetryPatches     int
}

type conversationProjectionMemberClassifyEvent struct {
	Result   string
	CacheHit bool
}

type conversationProjectionAuthorityAdmitEvent struct {
	Result        string
	TargetGroups  int
	LocalBatches  int
	RemoteBatches int
}

type conversationProjectionRetryEvent struct {
	Patches int
}

type conversationProjectionRetryDropEvent struct {
	Reason string
}

type conversationAsyncProjector struct {
	projector        conversationPatchProjector
	authority        conversationPatchAuthority
	flusher          conversationAuthorityFlusher
	flushInterval    time.Duration
	maxDirtyEvents   int
	maxRetryPatches  int
	retryMaxAge      time.Duration
	admitBatchRows   int
	admitConcurrency int
	admitTimeout     time.Duration
	observer         conversationProjectionObserver

	shards      []conversationAsyncProjectorShard
	dirtyEvents atomic.Int64

	retryMu sync.Mutex
	retry   map[conversationAuthorityPendingKey]conversationProjectionRetryPatch

	runMu     sync.Mutex
	started   bool
	cancelRun context.CancelFunc
	doneCh    chan struct{}
	flushMu   sync.Mutex
}

type conversationAsyncProjectorShard struct {
	mu     sync.Mutex
	events map[conversationAsyncProjectorKey]messageevents.MessageCommitted
}

type conversationAsyncProjectorKey struct {
	channelID   string
	channelType uint8
	fromUID     string
}

type conversationProjectionRetryPatch struct {
	patch     conversationusecase.ActivePatch
	createdAt time.Time
}

func newConversationAsyncProjector(opts conversationAsyncProjectorOptions) *conversationAsyncProjector {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}
	if opts.ShardCount <= 0 {
		opts.ShardCount = 64
	}
	if opts.MaxDirtyEvents <= 0 {
		opts.MaxDirtyEvents = 100000
	}
	if opts.MaxRetryPatches <= 0 {
		opts.MaxRetryPatches = 100000
	}
	if opts.RetryMaxAge <= 0 {
		opts.RetryMaxAge = 30 * time.Second
	}
	if opts.AdmitBatchRows <= 0 {
		opts.AdmitBatchRows = 512
	}
	if opts.AdmitConcurrency <= 0 {
		opts.AdmitConcurrency = 16
	}
	if opts.AdmitTimeout <= 0 {
		opts.AdmitTimeout = 500 * time.Millisecond
	}
	p := &conversationAsyncProjector{
		projector:        opts.Projector,
		authority:        opts.Authority,
		flusher:          opts.Flusher,
		flushInterval:    opts.FlushInterval,
		maxDirtyEvents:   opts.MaxDirtyEvents,
		maxRetryPatches:  opts.MaxRetryPatches,
		retryMaxAge:      opts.RetryMaxAge,
		admitBatchRows:   opts.AdmitBatchRows,
		admitConcurrency: opts.AdmitConcurrency,
		admitTimeout:     opts.AdmitTimeout,
		observer:         opts.Observer,
		shards:           make([]conversationAsyncProjectorShard, opts.ShardCount),
		retry:            make(map[conversationAuthorityPendingKey]conversationProjectionRetryPatch),
	}
	for i := range p.shards {
		p.shards[i].events = make(map[conversationAsyncProjectorKey]messageevents.MessageCommitted)
	}
	p.observeDirty()
	p.observeRetry()
	return p
}

func (p *conversationAsyncProjector) Start(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if p.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancelRun = cancel
	p.doneCh = make(chan struct{})
	p.started = true
	go p.run(runCtx, p.doneCh)
	return nil
}

func (p *conversationAsyncProjector) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.runMu.Lock()
	cancel := p.cancelRun
	doneCh := p.doneCh
	started := p.started
	p.cancelRun = nil
	p.doneCh = nil
	p.started = false
	p.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started && doneCh != nil {
		select {
		case <-doneCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := p.Flush(ctx)
	if p.flusher != nil {
		err = errors.Join(err, p.flusher.Flush(ctx))
	}
	return err
}

func (p *conversationAsyncProjector) run(ctx context.Context, doneCh chan<- struct{}) {
	ticker := time.NewTicker(p.flushInterval)
	defer func() {
		ticker.Stop()
		close(doneCh)
	}()
	for {
		select {
		case <-ticker.C:
			_ = p.Flush(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (p *conversationAsyncProjector) RequiresCommittedPayload() bool {
	return false
}

func (p *conversationAsyncProjector) SubmitMetadata(ctx context.Context, event messageevents.MessageCommitted) error {
	return p.Submit(ctx, event)
}

func (p *conversationAsyncProjector) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	if p == nil || len(p.shards) == 0 || event.ChannelID == "" || event.ChannelType == 0 {
		p.observeSubmit(conversationProjectionResultIgnored)
		return nil
	}
	event.Payload = nil
	event.MessageScopedUIDs = nil
	key := conversationAsyncProjectorKey{channelID: event.ChannelID, channelType: event.ChannelType, fromUID: event.FromUID}
	shard := &p.shards[p.shardIndex(key)]
	shard.mu.Lock()
	existing, exists := shard.events[key]
	if !exists && !p.tryReserveDirtyEvent() {
		shard.mu.Unlock()
		p.observeSubmit(conversationProjectionResultDropped)
		return nil
	}
	if !exists || committedEventAfter(event, existing) {
		shard.events[key] = event
	}
	shard.mu.Unlock()
	if exists {
		p.observeSubmit(conversationProjectionResultCoalesced)
		return nil
	}
	p.observeDirty()
	p.observeSubmit(conversationProjectionResultAccepted)
	return nil
}

func (p *conversationAsyncProjector) Flush(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.flushMu.Lock()
	defer p.flushMu.Unlock()
	startedAt := time.Now()
	events := p.drainEvents()
	retryPatches := p.drainRetry(time.Now())
	patches, dense, sparse, projectErr := p.projectEvents(ctx, events)
	patches = append(patches, retryPatches...)
	patches = coalesceConversationPatches(patches)
	if len(patches) == 0 {
		p.observeFlush(conversationProjectionFlushEvent{
			Result:        projectionFlushResult(projectErr),
			Duration:      time.Since(startedAt),
			DrainedEvents: len(events),
			DenseEvents:   dense,
			SparseEvents:  sparse,
			RetryPatches:  p.retryCount(),
		})
		return projectErr
	}
	admitErr := p.admitPatches(ctx, patches)
	if admitErr != nil {
		p.rememberRetry(patches, time.Now())
	}
	err := errors.Join(projectErr, admitErr)
	p.observeFlush(conversationProjectionFlushEvent{
		Result:           projectionFlushResult(err),
		Duration:         time.Since(startedAt),
		DrainedEvents:    len(events),
		ProjectedPatches: len(patches),
		DenseEvents:      dense,
		SparseEvents:     sparse,
		RetryPatches:     p.retryCount(),
	})
	return err
}

func (p *conversationAsyncProjector) drainEvents() []messageevents.MessageCommitted {
	if p == nil {
		return nil
	}
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
	sort.Slice(events, func(i, j int) bool {
		if events[i].ChannelID != events[j].ChannelID {
			return events[i].ChannelID < events[j].ChannelID
		}
		if events[i].ChannelType != events[j].ChannelType {
			return events[i].ChannelType < events[j].ChannelType
		}
		return events[i].FromUID < events[j].FromUID
	})
	p.observeDirty()
	return events
}

func (p *conversationAsyncProjector) projectEvents(ctx context.Context, events []messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, int, int, error) {
	if p == nil || p.projector == nil {
		return nil, 0, 0, nil
	}
	var patches []conversationusecase.ActivePatch
	var firstErr error
	dense := 0
	sparse := 0
	for _, event := range events {
		projected, err := p.projector.ProjectActivePatches(ctx, event)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			p.observeMemberClassify(conversationProjectionResultFromError(err), false)
			continue
		}
		if len(projected) == 0 {
			p.observeMemberClassify(conversationProjectionResultIgnored, false)
			continue
		}
		if conversationPatchesSparse(projected) {
			sparse++
		} else {
			dense++
		}
		p.observeMemberClassify(conversationProjectionResultOK, false)
		patches = append(patches, projected...)
	}
	return coalesceConversationPatches(patches), dense, sparse, firstErr
}

func (p *conversationAsyncProjector) admitPatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if p == nil || p.authority == nil || len(patches) == 0 {
		return nil
	}
	batches := conversationActivePatchBatches(patches, p.admitBatchRows)
	if len(batches) == 0 {
		return nil
	}
	if p.admitConcurrency <= 1 || len(batches) == 1 {
		for _, batch := range batches {
			if err := p.admitBatch(ctx, batch); err != nil {
				p.observeAuthorityAdmit(conversationProjectionResultFromError(err), len(batches), 0, len(batches))
				return err
			}
		}
		p.observeAuthorityAdmit(conversationProjectionResultOK, len(batches), 0, len(batches))
		return nil
	}
	workers := p.admitConcurrency
	if workers > len(batches) {
		workers = len(batches)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan []conversationusecase.ActivePatch)
	var wg sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				if err := p.admitBatch(workCtx, batch); err != nil {
					firstErrOnce.Do(func() {
						firstErr = err
						cancel()
					})
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
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	p.observeAuthorityAdmit(conversationProjectionResultFromError(firstErr), len(batches), 0, len(batches))
	return firstErr
}

func (p *conversationAsyncProjector) admitBatch(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if p.admitTimeout <= 0 {
		return p.authority.AdmitPatches(ctx, patches)
	}
	admitCtx, cancel := context.WithTimeout(ctx, p.admitTimeout)
	defer cancel()
	return p.authority.AdmitPatches(admitCtx, patches)
}

func (p *conversationAsyncProjector) rememberRetry(patches []conversationusecase.ActivePatch, now time.Time) {
	if p == nil || len(patches) == 0 {
		return
	}
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			p.observeRetryDrop(conversationProjectionRetryDropInvalid)
			continue
		}
		key := pendingConversationPatchKey(patch)
		if len(p.retry) >= p.maxRetryPatches {
			p.dropOldestRetryLocked()
		}
		existing := p.retry[key]
		if existing.createdAt.IsZero() {
			existing.createdAt = now
		}
		existing.patch = mergeConversationActivePatch(existing.patch, patch)
		p.retry[key] = existing
	}
	p.observeRetryLocked()
}

func (p *conversationAsyncProjector) drainRetry(now time.Time) []conversationusecase.ActivePatch {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	if len(p.retry) == 0 {
		return nil
	}
	patches := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(p.retry))
	for key, retry := range p.retry {
		if p.retryMaxAge > 0 && now.Sub(retry.createdAt) > p.retryMaxAge {
			delete(p.retry, key)
			p.observeRetryDrop(conversationProjectionRetryDropAge)
			continue
		}
		patches[key] = retry.patch
		delete(p.retry, key)
	}
	p.observeRetryLocked()
	return sortedPendingConversationPatches(patches)
}

func (p *conversationAsyncProjector) dropOldestRetryLocked() {
	var oldestKey conversationAuthorityPendingKey
	var oldest time.Time
	for key, retry := range p.retry {
		if oldest.IsZero() || retry.createdAt.Before(oldest) {
			oldestKey = key
			oldest = retry.createdAt
		}
	}
	if !oldest.IsZero() {
		delete(p.retry, oldestKey)
		p.observeRetryDrop(conversationProjectionRetryDropCapacity)
	}
}

func (p *conversationAsyncProjector) retryCount() int {
	if p == nil {
		return 0
	}
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	return len(p.retry)
}

func (p *conversationAsyncProjector) tryReserveDirtyEvent() bool {
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

func (p *conversationAsyncProjector) shardIndex(key conversationAsyncProjectorKey) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key.channelID))
	_, _ = h.Write([]byte{key.channelType})
	_, _ = h.Write([]byte(key.fromUID))
	return int(h.Sum32() % uint32(len(p.shards)))
}

func committedEventAfter(next, existing messageevents.MessageCommitted) bool {
	if next.MessageSeq != existing.MessageSeq {
		return next.MessageSeq > existing.MessageSeq
	}
	return next.ServerTimestampMS >= existing.ServerTimestampMS
}

func coalesceConversationPatches(patches []conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	merged := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(patches))
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		merged[key] = mergeConversationActivePatch(merged[key], patch)
	}
	return sortedPendingConversationPatches(merged)
}

func conversationPatchesSparse(patches []conversationusecase.ActivePatch) bool {
	for _, patch := range patches {
		if patch.SparseActive {
			return true
		}
	}
	return false
}

func projectionFlushResult(err error) string {
	if err == nil {
		return conversationProjectionResultOK
	}
	return conversationProjectionResultFromError(err)
}

func conversationProjectionResultFromError(err error) string {
	switch {
	case err == nil:
		return conversationProjectionResultOK
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return conversationProjectionResultTimeout
	case errors.Is(err, conversationusecase.ErrRouteNotReady):
		return conversationProjectionResultRouteNotReady
	case errors.Is(err, conversationusecase.ErrStaleRoute):
		return conversationProjectionResultStaleRoute
	case errors.Is(err, conversationusecase.ErrNotLeader):
		return conversationProjectionResultNotLeader
	case errors.Is(err, conversationusecase.ErrCachePressure):
		return conversationAuthorityResultCachePressure
	default:
		return conversationProjectionResultError
	}
}

func (p *conversationAsyncProjector) observeDirty() {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.SetConversationProjectionDirty(conversationProjectionDirtyEvent{DirtyKeys: int(p.dirtyEvents.Load()), MaxDirtyEvents: p.maxDirtyEvents})
}

func (p *conversationAsyncProjector) observeSubmit(result string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionSubmit(conversationProjectionSubmitEvent{Result: result})
}

func (p *conversationAsyncProjector) observeFlush(event conversationProjectionFlushEvent) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionFlush(event)
}

func (p *conversationAsyncProjector) observeMemberClassify(result string, cacheHit bool) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionMemberClassify(conversationProjectionMemberClassifyEvent{Result: result, CacheHit: cacheHit})
}

func (p *conversationAsyncProjector) observeAuthorityAdmit(result string, targetGroups, localBatches, remoteBatches int) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionAuthorityAdmit(conversationProjectionAuthorityAdmitEvent{
		Result:        result,
		TargetGroups:  targetGroups,
		LocalBatches:  localBatches,
		RemoteBatches: remoteBatches,
	})
}

func (p *conversationAsyncProjector) observeRetry() {
	if p == nil || p.observer == nil {
		return
	}
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	p.observeRetryLocked()
}

func (p *conversationAsyncProjector) observeRetryLocked() {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.SetConversationProjectionRetry(conversationProjectionRetryEvent{Patches: len(p.retry)})
}

func (p *conversationAsyncProjector) observeRetryDrop(reason string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveConversationProjectionRetryDrop(conversationProjectionRetryDropEvent{Reason: reason})
}
