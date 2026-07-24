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
)

const (
	defaultFlushWindow = 200 * time.Microsecond
	defaultQueueSize   = 1024
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
	// Priority is reserved for future scheduling policy.
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
	// FlushWindow is the maximum time spent collecting adjacent requests.
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
	// Publish runs after the physical commit succeeds.
	Publish func() error
	// Finalize releases request-scoped resources after the request reaches one terminal state.
	Finalize func()
}

// Coordinator batches logical requests into fewer physical commits.
type Coordinator struct {
	db *engine.DB

	shards []coordinatorShard

	mu         sync.Mutex
	cfg        Config
	commitFunc func(batch *engine.Batch) error
	deferred   []pendingRequest

	requests chan pendingRequest
	stopCh   chan struct{}
	doneCh   chan struct{}

	closeOnce sync.Once
	acceptMu  sync.RWMutex
	closed    bool
}

type pendingRequest struct {
	Request
	done         chan error
	finalizeOnce *sync.Once
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
		db:       db,
		cfg:      cfg,
		requests: make(chan pendingRequest, cfg.QueueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
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
	pending := newPendingRequest(req)
	if c == nil || c.db == nil || req.Build == nil {
		pending.complete(dberrors.ErrInvalidArgument)
		return dberrors.ErrInvalidArgument
	}
	if c.isSharded() {
		c.acceptMu.RLock()
		if c.closed {
			c.acceptMu.RUnlock()
			pending.complete(ErrClosed)
			return ErrClosed
		}
		shard := c.shardFor(req.Partition)
		c.acceptMu.RUnlock()
		if shard == nil {
			pending.complete(ErrClosed)
			return ErrClosed
		}
		return shard.submit(ctx, pending)
	}
	return c.submit(ctx, pending)
}

func (c *Coordinator) submit(ctx context.Context, pending pendingRequest) (err error) {
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
			Err:      err,
		})
	}()
	c.acceptMu.RLock()
	if c.closed {
		c.acceptMu.RUnlock()
		err = ErrClosed
		pending.complete(err)
		return err
	}
	select {
	case c.requests <- pending:
		c.acceptMu.RUnlock()
		c.observeQueueDepth()
	case <-ctx.Done():
		c.acceptMu.RUnlock()
		err = ctx.Err()
		pending.complete(err)
		return err
	case <-c.stopCh:
		c.acceptMu.RUnlock()
		err = ErrClosed
		pending.complete(err)
		return err
	}

	select {
	case err = <-pending.done:
		return err
	case <-ctx.Done():
		err = ctx.Err()
		return err
	case <-c.doneCh:
		select {
		case err = <-pending.done:
			return err
		default:
			pending.complete(ErrClosed)
			err = <-pending.done
			return err
		}
	}
}

func newPendingRequest(req Request) pendingRequest {
	return pendingRequest{
		Request:      req,
		done:         make(chan error, 1),
		finalizeOnce: &sync.Once{},
	}
}

func (r pendingRequest) complete(err error) {
	r.finalizeOnce.Do(func() {
		if r.Finalize != nil {
			r.Finalize()
		}
		r.done <- err
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
		c.commit(batch, time.Since(collectStarted))
	}
}

func (c *Coordinator) nextRequest() (pendingRequest, bool) {
	c.mu.Lock()
	if len(c.deferred) > 0 {
		req := c.deferred[0]
		copy(c.deferred, c.deferred[1:])
		c.deferred = c.deferred[:len(c.deferred)-1]
		c.mu.Unlock()
		c.observeQueueDepth()
		return req, true
	}
	c.mu.Unlock()

	select {
	case <-c.stopCh:
		c.failQueued()
		return pendingRequest{}, false
	case req := <-c.requests:
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
			select {
			case req := <-c.requests:
				if c.wouldExceed(batch, req) {
					c.deferRequest(req)
					return batch
				}
				batch.requests = append(batch.requests, req)
				if c.limitReached(batch) {
					return batch
				}
			case <-c.stopCh:
				batch.closed = true
				return batch
			default:
				return batch
			}
		}
	}

	timer := time.NewTimer(c.cfg.FlushWindow)
	defer timer.Stop()
	for {
		select {
		case req := <-c.requests:
			if c.wouldExceed(batch, req) {
				c.deferRequest(req)
				return batch
			}
			batch.requests = append(batch.requests, req)
			if c.limitReached(batch) {
				return batch
			}
		case <-timer.C:
			return batch
		case <-c.stopCh:
			batch.closed = true
			return batch
		}
	}
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
		reqs.completeAll(ErrClosed)
		return
	}
	batch := c.db.NewBatch()
	defer batch.Close()
	buildStarted := time.Now()
	for _, req := range reqs.requests {
		if err := req.Build(batch); err != nil {
			buildDuration := time.Since(buildStarted)
			c.observeBatch(BatchEvent{
				Requests:        requests,
				Records:         records,
				Bytes:           bytes,
				CollectDuration: collectDuration,
				BuildDuration:   buildDuration,
				TotalDuration:   collectDuration + buildDuration,
				Err:             err,
			})
			reqs.completeAll(err)
			return
		}
	}
	buildDuration := time.Since(buildStarted)
	c.mu.Lock()
	commitFunc := c.commitFunc
	c.mu.Unlock()
	commitStarted := time.Now()
	if err := commitFunc(batch); err != nil {
		commitDuration := time.Since(commitStarted)
		c.observeBatch(BatchEvent{
			Requests:        requests,
			Records:         records,
			Bytes:           bytes,
			CollectDuration: collectDuration,
			BuildDuration:   buildDuration,
			CommitDuration:  commitDuration,
			TotalDuration:   collectDuration + buildDuration + commitDuration,
			Err:             err,
		})
		reqs.completeAll(err)
		return
	}
	commitDuration := time.Since(commitStarted)
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
	c.observeBatch(BatchEvent{
		Requests:        requests,
		Records:         records,
		Bytes:           bytes,
		CollectDuration: collectDuration,
		BuildDuration:   buildDuration,
		CommitDuration:  commitDuration,
		PublishDuration: publishDuration,
		TotalDuration:   collectDuration + buildDuration + commitDuration + publishDuration,
		Err:             publishErr,
	})
	for i, req := range reqs.requests {
		req.complete(publishResults[i])
	}
}

func (c *Coordinator) deferRequest(req pendingRequest) {
	c.mu.Lock()
	c.deferred = append(c.deferred, req)
	c.mu.Unlock()
	c.observeQueueDepth()
}

func (c *Coordinator) failQueued() {
	for {
		select {
		case req := <-c.requests:
			req.complete(ErrClosed)
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
	c.mu.Lock()
	depth := len(c.deferred)
	c.mu.Unlock()
	depth += len(c.requests)
	c.cfg.Observer.SetQueueDepth(depth)
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
	observer Observer
	mu       sync.Mutex
	depths   []int
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
