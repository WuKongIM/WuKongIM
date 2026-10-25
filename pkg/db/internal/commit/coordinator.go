package commit

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

const (
	// A 500-microsecond collection window is the measured local optimum for the
	// durable single-shard coordinator; 1 millisecond and wider windows add more
	// request wait than batching benefit.
	defaultFlushWindow = 500 * time.Microsecond
	defaultQueueSize   = 1024
	maxConsecutiveHigh = 8
)

// ErrClosed reports submissions after the coordinator stops accepting work.
var ErrClosed = errors.New("commit: closed")

// Priority describes the relative importance of a commit lane.
type Priority uint8

const (
	// PriorityNormal is the default lane priority.
	PriorityNormal Priority = iota
	// PriorityHigh is reserved for hot-path append lanes.
	PriorityHigh
)

// Lane identifies a logical commit stream.
type Lane struct {
	// Name is a low-cardinality lane name for metrics and debugging.
	Name string
	// Priority controls bounded owner-side selection when multiple requests are queued.
	Priority Priority
}

// BatchEvent describes one physical commit attempt produced by the coordinator.
type BatchEvent struct {
	// Requests is the number of logical requests grouped into the physical commit.
	Requests int
	// Records is the total logical record count in the grouped requests.
	Records int
	// Bytes is the approximate payload byte count in the grouped requests.
	Bytes int
	// CollectDuration is the time spent collecting adjacent requests.
	CollectDuration time.Duration
	// BuildDuration is the time spent staging all request mutations into the batch.
	BuildDuration time.Duration
	// CommitDuration is the time spent in the physical engine commit.
	CommitDuration time.Duration
	// PublishDuration is the time spent publishing committed state to callers.
	PublishDuration time.Duration
	// TotalDuration is the sum of collect, build, commit, and publish durations.
	TotalDuration time.Duration
	// Native is available only when this physical batch completed native commit work.
	// Its nested durations do not extend TotalDuration or form RPC spans.
	Native engine.NativeCommitStats
	// Err is set when build, commit, publish, or close failed the batch.
	Err error
}

// RequestEvent describes one logical commit request as observed by the caller.
type RequestEvent struct {
	// Lane identifies the logical commit lane.
	Lane Lane
	// Records is the logical record count carried by the request.
	Records int
	// Bytes is the approximate payload byte count carried by the request.
	Bytes int
	// Duration is the caller-visible time spent inside Submit.
	Duration time.Duration
	// Err is the Submit result observed by the caller.
	Err error
}

// Observer receives low-cardinality coordinator runtime measurements.
type Observer interface {
	// SetQueueDepth reports the current logical commit queue depth.
	SetQueueDepth(depth int)
	// ObserveBatch reports one grouped physical commit attempt.
	ObserveBatch(event BatchEvent)
}

// RequestObserver receives caller-visible logical request measurements.
type RequestObserver interface {
	// ObserveRequest reports one Submit call without changing commit behavior.
	ObserveRequest(event RequestEvent)
}

// Config controls group commit behavior.
type Config struct {
	// FlushWindow bounds waiting for additional arrivals. After it expires, one
	// bounded snapshot of already queued requests may join the batch without waiting.
	FlushWindow time.Duration
	// QueueSize bounds waiting requests.
	QueueSize int
	// Shards routes logical requests across independent coordinators by partition. One keeps serial behavior.
	Shards int
	// MaxRequests caps requests per physical commit when positive.
	MaxRequests int
	// MaxRecords caps logical records per physical commit when positive.
	MaxRecords int
	// MaxBytes caps approximate payload bytes per physical commit when positive.
	MaxBytes int
	// Observer receives grouped commit batch and queue-depth measurements.
	Observer Observer
}

// Request is one logical durable mutation.
type Request struct {
	// Lane identifies the logical commit lane.
	Lane Lane
	// Partition identifies the logical partition affected by the request.
	Partition string
	// Records is the logical record count used for batch limits.
	Records int
	// Bytes is the approximate payload byte count used for batch limits.
	Bytes int
	// Build stages writes into the physical batch.
	Build func(batch *engine.Batch) error
	// RebuildOnGroupAbort allows one isolated rebuild when another request
	// rejects the shared batch before physical commit. Build must reset all
	// staged overlays and have no external side effects; resources remain owned
	// until the single terminal Finalize. Physical commit errors are never retried.
	RebuildOnGroupAbort bool
	// Publish runs after the physical commit succeeds.
	Publish func() error
	// Finalize releases request-scoped resources after the request reaches one terminal state.
	Finalize func()
}

// Outcome classifies whether one atomic commit request can have changed disk.
type Outcome uint8

const (
	OutcomeUnspecified Outcome = iota
	// OutcomeDefinitelyNotCommitted proves the request never reached a physical commit.
	OutcomeDefinitelyNotCommitted
	// OutcomeCommitted proves the physical commit completed successfully.
	OutcomeCommitted
	// OutcomeUnknown means commit admission occurred but its durable result is ambiguous.
	OutcomeUnknown
)

// SubmitResult is the exact terminal evidence returned by SubmitWithOutcome.
type SubmitResult struct {
	Outcome Outcome
	Err     error
}

// Coordinator batches logical requests into fewer physical commits.
type Coordinator struct {
	db *engine.DB

	shards []coordinatorShard

	mu              sync.Mutex
	cfg             Config
	commitFunc      func(batch *engine.Batch) error
	pendingHigh     []pendingRequest
	pendingNormal   []pendingRequest
	consecutiveHigh int
	// queueObservationMu linearizes absolute queue-depth publications without
	// holding the physical queue state lock across the external observer.
	queueObservationMu sync.Mutex

	requests chan pendingRequest
	// queueSlots bounds requests across both the intake channel and owner-local
	// priority queues. A slot is released only when a request enters a physical
	// batch or reaches a terminal state without doing so.
	queueSlots chan struct{}
	stopCh     chan struct{}
	doneCh     chan struct{}

	closeOnce sync.Once
	acceptMu  sync.RWMutex
	closed    bool
}

type pendingRequest struct {
	Request
	done             chan SubmitResult
	finalizeOnce     *sync.Once
	queueSlotOnce    *sync.Once
	releaseQueueSlot func()
	// trace is optional fixed observation state shared by every copy of this receipt.
	trace *requestTrace
}

type coordinatorShard struct {
	coordinator *Coordinator
}

// NewCoordinator starts a commit coordinator for db.
func NewCoordinator(db *engine.DB, cfg Config) *Coordinator {
	cfg = effectiveConfig(cfg)
	if cfg.Shards > 1 {
		c := &Coordinator{db: db, cfg: cfg}
		observer := newShardedObserver(cfg.Observer, cfg.Shards)
		c.shards = make([]coordinatorShard, cfg.Shards)
		for i := range c.shards {
			shardCfg := cfg
			shardCfg.Shards = 1
			if observer != nil {
				shardCfg.Observer = observer.shard(i)
			}
			c.shards[i].coordinator = NewCoordinator(db, shardCfg)
		}
		return c
	}
	c := &Coordinator{
		db:         db,
		cfg:        cfg,
		requests:   make(chan pendingRequest, cfg.QueueSize),
		queueSlots: make(chan struct{}, cfg.QueueSize),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	c.commitFunc = func(batch *engine.Batch) error { return batch.Commit(true) }
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskDatabaseCommitCoordinator, c.run)
	return c
}

// SetCommitFunc overrides the physical commit function. It is intended for tests.
func (c *Coordinator) SetCommitFunc(fn func(batch *engine.Batch) error) {
	if c == nil || fn == nil {
		return
	}
	if c.isSharded() {
		for _, shard := range c.shards {
			shard.coordinator.SetCommitFunc(fn)
		}
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commitFunc = fn
}

// Submit queues one logical request and waits for its result.
func (c *Coordinator) Submit(ctx context.Context, req Request) (err error) {
	return c.SubmitWithOutcome(ctx, req).Err
}

// SubmitWithOutcome queues one logical mutation and preserves whether an error was
// known before physical commit admission or became ambiguous afterward.
func (c *Coordinator) SubmitWithOutcome(ctx context.Context, req Request) (result SubmitResult) {
	pending := newPendingRequest(req)
	pending.trace = newRequestTrace(ctx)
	if c == nil || c.db == nil || req.Build == nil {
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: dberrors.ErrInvalidArgument}
		pending.complete(result)
		return result
	}
	if c.isSharded() {
		c.acceptMu.RLock()
		if c.closed {
			c.acceptMu.RUnlock()
			result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed}
			pending.complete(result)
			return result
		}
		shard := c.shardFor(req.Partition)
		c.acceptMu.RUnlock()
		if shard == nil {
			result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed}
			pending.complete(result)
			return result
		}
		return shard.submitResult(ctx, pending)
	}
	return c.submitResult(ctx, pending)
}

func (c *Coordinator) submitResult(ctx context.Context, pending pendingRequest) (result SubmitResult) {
	req := pending.Request
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := time.Now()
	defer func() {
		c.observeRequest(RequestEvent{
			Lane:     req.Lane,
			Records:  req.Records,
			Bytes:    req.Bytes,
			Duration: time.Since(startedAt),
			Err:      result.Err,
		})
	}()
	c.acceptMu.RLock()
	if c.closed {
		c.acceptMu.RUnlock()
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed}
		pending.complete(result)
		return result
	}
	select {
	case c.queueSlots <- struct{}{}:
		pending.releaseQueueSlot = func() { <-c.queueSlots }
	case <-ctx.Done():
		c.acceptMu.RUnlock()
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ctx.Err()}
		pending.complete(result)
		return result
	case <-c.stopCh:
		c.acceptMu.RUnlock()
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed}
		pending.complete(result)
		return result
	}
	// Admission measures queue-credit acquisition; queue time includes reserved
	// credit before intake handoff and all owner-local priority-buffer residence.
	pending.trace.advance(rpctrace.StageCommitQueue, rpctrace.ResultOK)
	select {
	case c.requests <- pending:
		c.acceptMu.RUnlock()
		c.observeQueueDepth()
	case <-ctx.Done():
		c.acceptMu.RUnlock()
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ctx.Err()}
		pending.complete(result)
		return result
	case <-c.stopCh:
		c.acceptMu.RUnlock()
		result = SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed}
		pending.complete(result)
		return result
	}

	select {
	case result = <-pending.done:
		return result
	case <-ctx.Done():
		return SubmitResult{Outcome: OutcomeUnknown, Err: ctx.Err()}
	case <-c.doneCh:
		select {
		case result = <-pending.done:
			return result
		default:
			pending.complete(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed})
			return <-pending.done
		}
	}
}

func newPendingRequest(req Request) pendingRequest {
	return pendingRequest{
		Request:       req,
		done:          make(chan SubmitResult, 1),
		finalizeOnce:  &sync.Once{},
		queueSlotOnce: &sync.Once{},
	}
}

func (r pendingRequest) complete(result SubmitResult) {
	r.finalizeOnce.Do(func() {
		r.trace.finish(commitTraceResult(result.Err))
		r.releaseQueued()
		if r.Finalize != nil {
			r.Finalize()
		}
		r.done <- result
	})
}

func (r pendingRequest) releaseQueued() {
	if r.queueSlotOnce == nil {
		return
	}
	r.queueSlotOnce.Do(func() {
		if r.releaseQueueSlot != nil {
			r.releaseQueueSlot()
		}
	})
}

// Close stops the coordinator and fails work that was not committed.
func (c *Coordinator) Close() {
	if c == nil {
		return
	}
	if c.isSharded() {
		c.closeOnce.Do(func() {
			c.acceptMu.Lock()
			c.closed = true
			c.acceptMu.Unlock()
			for _, shard := range c.shards {
				shard.coordinator.Close()
			}
		})
		return
	}
	c.closeOnce.Do(func() {
		c.acceptMu.Lock()
		c.closed = true
		c.acceptMu.Unlock()
		close(c.stopCh)
		<-c.doneCh
	})
}

func (c *Coordinator) run() {
	defer close(c.doneCh)
	for {
		req, ok := c.nextRequest()
		if !ok {
			return
		}
		collectStarted := time.Now()
		batch := c.collect(req)
		// collect may drain additional requests directly from the channel. Publish
		// the remaining physical queue after that drain so the last grouped batch
		// cannot leave its pre-collection depth as the terminal gauge.
		c.observeQueueDepth()
		c.commit(batch, time.Since(collectStarted))
	}
}

func (c *Coordinator) nextRequest() (pendingRequest, bool) {
	select {
	case <-c.stopCh:
		c.failQueued()
		return pendingRequest{}, false
	default:
	}
	c.drainIncoming()
	if req, ok := c.popPending(true); ok {
		req.enterBatch()
		c.observeQueueDepth()
		return req, true
	}

	select {
	case <-c.stopCh:
		c.failQueued()
		return pendingRequest{}, false
	case req := <-c.requests:
		c.enqueuePending(req)
		c.drainIncoming()
		req, ok := c.popPending(true)
		if !ok {
			return pendingRequest{}, false
		}
		req.enterBatch()
		c.observeQueueDepth()
		return req, true
	}
}

func (c *Coordinator) collect(first pendingRequest) requestBatch {
	batch := requestBatch{requests: []pendingRequest{first}}
	if c.limitReached(batch) {
		return batch
	}

	if c.cfg.FlushWindow <= 0 {
		for {
			if c.stopped() {
				batch.closed = true
				return batch
			}
			c.drainIncoming()
			req, ok := c.popPending(false)
			if !ok {
				return batch
			}
			if c.wouldExceed(batch, req) {
				c.requeueFront(req)
				return batch
			}
			req.enterBatch()
			batch.requests = append(batch.requests, req)
			if c.limitReached(batch) {
				return batch
			}
		}
	}

	timer := time.NewTimer(c.cfg.FlushWindow)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return c.collectQueuedSnapshot(batch)
		case <-c.stopCh:
			batch.closed = true
			return batch
		default:
		}
		c.drainIncoming()
		if req, ok := c.popPending(false); ok {
			if c.wouldExceed(batch, req) {
				c.requeueFront(req)
				return batch
			}
			req.enterBatch()
			batch.requests = append(batch.requests, req)
			if c.limitReached(batch) {
				return batch
			}
			continue
		}
		select {
		case req := <-c.requests:
			c.enqueuePending(req)
		case <-timer.C:
			return c.collectQueuedSnapshot(batch)
		case <-c.stopCh:
			batch.closed = true
			return batch
		}
	}
}

// collectQueuedSnapshot admits only the queue observed after the wait window.
// Releasing credits may admit new submissions; those stay in intake for the
// next cohort, so arrivals cannot prolong this drain even without size limits.
func (c *Coordinator) collectQueuedSnapshot(batch requestBatch) requestBatch {
	if c.stopped() {
		batch.closed = true
		return batch
	}
	incoming := len(c.requests)
	for i := 0; i < incoming; i++ {
		c.enqueuePending(<-c.requests)
	}
	c.mu.Lock()
	remaining := len(c.pendingHigh) + len(c.pendingNormal)
	c.mu.Unlock()
	for i := 0; i < remaining; i++ {
		if c.stopped() {
			batch.closed = true
			return batch
		}
		req, ok := c.popPending(false)
		if !ok {
			return batch
		}
		if c.wouldExceed(batch, req) {
			c.requeueFront(req)
			return batch
		}
		req.enterBatch()
		batch.requests = append(batch.requests, req)
		if c.limitReached(batch) {
			return batch
		}
	}
	return batch
}

func (c *Coordinator) commit(reqs requestBatch, collectDuration time.Duration) {
	requests, records, bytes := reqs.stats()
	if reqs.closed {
		c.observeBatch(BatchEvent{
			Requests:        requests,
			Records:         records,
			Bytes:           bytes,
			CollectDuration: collectDuration,
			TotalDuration:   collectDuration,
			Err:             ErrClosed,
		})
		reqs.completeAll(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed})
		return
	}
	reqs.traceStage(rpctrace.StageCommitBuild, rpctrace.ResultOK)
	batch := c.db.NewBatch()
	defer batch.Close()
	buildStarted := time.Now()
	for index, req := range reqs.requests {
		if err := req.Build(batch); err != nil {
			buildDuration := time.Since(buildStarted)
			reqs.finishTrace(commitTraceResult(err))
			c.observeBatch(BatchEvent{
				Requests:        requests,
				Records:         records,
				Bytes:           bytes,
				CollectDuration: collectDuration,
				BuildDuration:   buildDuration,
				TotalDuration:   collectDuration + buildDuration,
				Err:             err,
			})
			// Discard every staged write before rebuilding any independent
			// request. A logical rejection must never be attributed to a
			// different request that opted into isolated reconstruction.
			_ = batch.Close()
			for position, candidate := range reqs.requests {
				if position != index && candidate.RebuildOnGroupAbort && len(reqs.requests) > 1 {
					c.commit(requestBatch{requests: []pendingRequest{candidate}, closed: c.stopped()}, 0)
				} else {
					candidate.complete(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: err})
				}
			}
			return
		}
	}
	buildDuration := time.Since(buildStarted)
	c.mu.Lock()
	commitFunc := c.commitFunc
	c.mu.Unlock()
	commitStarted := time.Now()
	reqs.traceStage(rpctrace.StageCommitDurable, rpctrace.ResultOK)
	if err := commitFunc(batch); err != nil {
		commitDuration := time.Since(commitStarted)
		reqs.finishTrace(commitTraceResult(err))
		c.observeBatch(BatchEvent{
			Requests:        requests,
			Records:         records,
			Bytes:           bytes,
			CollectDuration: collectDuration,
			BuildDuration:   buildDuration,
			CommitDuration:  commitDuration,
			Native:          batch.CommitStats(),
			TotalDuration:   collectDuration + buildDuration + commitDuration,
			Err:             err,
		})
		reqs.completeAll(SubmitResult{Outcome: OutcomeUnknown, Err: err})
		return
	}
	commitDuration := time.Since(commitStarted)
	native := batch.CommitStats()
	reqs.traceStage(rpctrace.StageCommitPublish, rpctrace.ResultOK)
	publishStarted := time.Now()
	var publishErr error
	publishResults := make([]error, len(reqs.requests))
	for i, req := range reqs.requests {
		if req.Publish != nil {
			if err := req.Publish(); err != nil {
				if publishErr == nil {
					publishErr = err
				}
				publishResults[i] = err
			}
		}
	}
	publishDuration := time.Since(publishStarted)
	// Publication spans cover every group callback, excluding the later Finalize
	// resource release and observer callbacks; their results stay request-aligned.
	for i, req := range reqs.requests {
		req.trace.finish(commitTraceResult(publishResults[i]))
	}
	c.observeBatch(BatchEvent{
		Requests:        requests,
		Records:         records,
		Bytes:           bytes,
		CollectDuration: collectDuration,
		BuildDuration:   buildDuration,
		CommitDuration:  commitDuration,
		PublishDuration: publishDuration,
		Native:          native,
		TotalDuration:   collectDuration + buildDuration + commitDuration + publishDuration,
		Err:             publishErr,
	})
	for i, req := range reqs.requests {
		req.complete(SubmitResult{Outcome: OutcomeCommitted, Err: publishResults[i]})
	}
}

func (c *Coordinator) enqueuePending(req pendingRequest) {
	c.mu.Lock()
	if req.Lane.Priority == PriorityHigh {
		c.pendingHigh = append(c.pendingHigh, req)
	} else {
		c.pendingNormal = append(c.pendingNormal, req)
	}
	c.mu.Unlock()
}

func (c *Coordinator) requeueFront(req pendingRequest) {
	c.mu.Lock()
	if req.Lane.Priority == PriorityHigh {
		c.pendingHigh = append(c.pendingHigh, pendingRequest{})
		copy(c.pendingHigh[1:], c.pendingHigh[:len(c.pendingHigh)-1])
		c.pendingHigh[0] = req
	} else {
		c.pendingNormal = append(c.pendingNormal, pendingRequest{})
		copy(c.pendingNormal[1:], c.pendingNormal[:len(c.pendingNormal)-1])
		c.pendingNormal[0] = req
	}
	c.mu.Unlock()
}

func (c *Coordinator) drainIncoming() {
	for {
		select {
		case req := <-c.requests:
			c.enqueuePending(req)
		default:
			return
		}
	}
}

func (c *Coordinator) popPending(fair bool) (pendingRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	preferHigh := len(c.pendingHigh) > 0
	if fair && len(c.pendingNormal) > 0 && c.consecutiveHigh >= maxConsecutiveHigh {
		preferHigh = false
	}
	if preferHigh {
		req := c.pendingHigh[0]
		copy(c.pendingHigh, c.pendingHigh[1:])
		c.pendingHigh = c.pendingHigh[:len(c.pendingHigh)-1]
		if fair {
			c.consecutiveHigh++
		}
		return req, true
	}
	if len(c.pendingNormal) > 0 {
		req := c.pendingNormal[0]
		copy(c.pendingNormal, c.pendingNormal[1:])
		c.pendingNormal = c.pendingNormal[:len(c.pendingNormal)-1]
		if fair {
			c.consecutiveHigh = 0
		}
		return req, true
	}
	return pendingRequest{}, false
}

func (c *Coordinator) stopped() bool {
	select {
	case <-c.stopCh:
		return true
	default:
		return false
	}
}

func (c *Coordinator) failQueued() {
	c.mu.Lock()
	pending := append([]pendingRequest(nil), c.pendingHigh...)
	pending = append(pending, c.pendingNormal...)
	c.pendingHigh = nil
	c.pendingNormal = nil
	c.mu.Unlock()
	for _, req := range pending {
		req.complete(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed})
	}
	for {
		select {
		case req := <-c.requests:
			req.complete(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed})
		default:
			c.observeQueueDepth()
			return
		}
	}
}

func (c *Coordinator) observeQueueDepth() {
	if c == nil || c.cfg.Observer == nil {
		return
	}
	c.queueObservationMu.Lock()
	defer c.queueObservationMu.Unlock()
	c.cfg.Observer.SetQueueDepth(len(c.queueSlots))
}

func (c *Coordinator) observeBatch(event BatchEvent) {
	if c == nil || c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.ObserveBatch(event)
}

func (c *Coordinator) observeRequest(event RequestEvent) {
	if c == nil || c.cfg.Observer == nil {
		return
	}
	observer, ok := c.cfg.Observer.(RequestObserver)
	if !ok {
		return
	}
	observer.ObserveRequest(event)
}

func (c *Coordinator) wouldExceed(batch requestBatch, req pendingRequest) bool {
	requests, records, bytes := batch.stats()
	return (c.cfg.MaxRequests > 0 && requests+1 > c.cfg.MaxRequests) ||
		(c.cfg.MaxRecords > 0 && records+req.Records > c.cfg.MaxRecords) ||
		(c.cfg.MaxBytes > 0 && bytes+req.Bytes > c.cfg.MaxBytes)
}

func (c *Coordinator) limitReached(batch requestBatch) bool {
	requests, records, bytes := batch.stats()
	return (c.cfg.MaxRequests > 0 && requests >= c.cfg.MaxRequests) ||
		(c.cfg.MaxRecords > 0 && records >= c.cfg.MaxRecords) ||
		(c.cfg.MaxBytes > 0 && bytes >= c.cfg.MaxBytes)
}

func effectiveConfig(cfg Config) Config {
	if cfg.FlushWindow == 0 {
		cfg.FlushWindow = defaultFlushWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.Shards <= 0 {
		cfg.Shards = 1
	}
	return cfg
}

func (c *Coordinator) isSharded() bool {
	return c != nil && len(c.shards) > 0
}

func (c *Coordinator) shardFor(partition string) *Coordinator {
	if !c.isSharded() {
		return c
	}
	index := partitionShard(partition, len(c.shards))
	return c.shards[index].coordinator
}

func partitionShard(partition string, shards int) int {
	if shards <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(partition))
	return int(h.Sum32() % uint32(shards))
}

type shardedObserver struct {
	observer      Observer
	mu            sync.Mutex
	depths        []int
	publicationMu sync.Mutex
}

type shardObserver struct {
	parent *shardedObserver
	index  int
}

func newShardedObserver(observer Observer, shards int) *shardedObserver {
	if observer == nil || shards <= 1 {
		return nil
	}
	return &shardedObserver{observer: observer, depths: make([]int, shards)}
}

func (o *shardedObserver) shard(index int) shardObserver {
	return shardObserver{parent: o, index: index}
}

func (o shardObserver) SetQueueDepth(depth int) {
	if o.parent == nil || o.parent.observer == nil {
		return
	}
	o.parent.mu.Lock()
	if o.index >= 0 && o.index < len(o.parent.depths) {
		o.parent.depths[o.index] = depth
	}
	o.parent.mu.Unlock()

	// Serialize absolute aggregate publications, then refresh the total after
	// acquiring the publication lock. A delayed shard callback must publish the
	// newest aggregate rather than overwrite a later zero with a stale total.
	o.parent.publicationMu.Lock()
	defer o.parent.publicationMu.Unlock()
	o.parent.mu.Lock()
	total := 0
	for _, value := range o.parent.depths {
		total += value
	}
	o.parent.mu.Unlock()
	o.parent.observer.SetQueueDepth(total)
}

func (o shardObserver) ObserveBatch(event BatchEvent) {
	if o.parent == nil || o.parent.observer == nil {
		return
	}
	o.parent.observer.ObserveBatch(event)
}

func (o shardObserver) ObserveRequest(event RequestEvent) {
	if o.parent == nil || o.parent.observer == nil {
		return
	}
	observer, ok := o.parent.observer.(RequestObserver)
	if !ok {
		return
	}
	observer.ObserveRequest(event)
}
