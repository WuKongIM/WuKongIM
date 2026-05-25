package commit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
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

// Config controls group commit behavior.
type Config struct {
	// FlushWindow is the maximum time spent collecting adjacent requests.
	FlushWindow time.Duration
	// QueueSize bounds waiting requests.
	QueueSize int
	// MaxRequests caps requests per physical commit when positive.
	MaxRequests int
	// MaxRecords caps logical records per physical commit when positive.
	MaxRecords int
	// MaxBytes caps approximate payload bytes per physical commit when positive.
	MaxBytes int
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
}

// Coordinator batches logical requests into fewer physical commits.
type Coordinator struct {
	db *engine.DB

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
	done chan error
}

// NewCoordinator starts a commit coordinator for db.
func NewCoordinator(db *engine.DB, cfg Config) *Coordinator {
	cfg = effectiveConfig(cfg)
	c := &Coordinator{
		db:       db,
		cfg:      cfg,
		requests: make(chan pendingRequest, cfg.QueueSize),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	c.commitFunc = func(batch *engine.Batch) error { return batch.Commit(true) }
	go c.run()
	return c
}

// SetCommitFunc overrides the physical commit function. It is intended for tests.
func (c *Coordinator) SetCommitFunc(fn func(batch *engine.Batch) error) {
	if c == nil || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commitFunc = fn
}

// Submit queues one logical request and waits for its result.
func (c *Coordinator) Submit(ctx context.Context, req Request) error {
	if c == nil || c.db == nil || req.Build == nil {
		return dberrors.ErrInvalidArgument
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pending := pendingRequest{Request: req, done: make(chan error, 1)}

	c.acceptMu.RLock()
	if c.closed {
		c.acceptMu.RUnlock()
		return ErrClosed
	}
	select {
	case c.requests <- pending:
		c.acceptMu.RUnlock()
	case <-ctx.Done():
		c.acceptMu.RUnlock()
		return ctx.Err()
	case <-c.stopCh:
		c.acceptMu.RUnlock()
		return ErrClosed
	}

	select {
	case err := <-pending.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.doneCh:
		select {
		case err := <-pending.done:
			return err
		default:
			return ErrClosed
		}
	}
}

// Close stops the coordinator and fails work that was not committed.
func (c *Coordinator) Close() {
	if c == nil {
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
		batch := c.collect(req)
		c.commit(batch)
	}
}

func (c *Coordinator) nextRequest() (pendingRequest, bool) {
	c.mu.Lock()
	if len(c.deferred) > 0 {
		req := c.deferred[0]
		copy(c.deferred, c.deferred[1:])
		c.deferred = c.deferred[:len(c.deferred)-1]
		c.mu.Unlock()
		return req, true
	}
	c.mu.Unlock()

	select {
	case <-c.stopCh:
		c.failQueued()
		return pendingRequest{}, false
	case req := <-c.requests:
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

func (c *Coordinator) commit(reqs requestBatch) {
	if reqs.closed {
		reqs.completeAll(ErrClosed)
		return
	}
	batch := c.db.NewBatch()
	defer batch.Close()
	for _, req := range reqs.requests {
		if err := req.Build(batch); err != nil {
			reqs.completeAll(err)
			return
		}
	}
	c.mu.Lock()
	commitFunc := c.commitFunc
	c.mu.Unlock()
	if err := commitFunc(batch); err != nil {
		reqs.completeAll(err)
		return
	}
	for _, req := range reqs.requests {
		if req.Publish != nil {
			if err := req.Publish(); err != nil {
				req.done <- err
				continue
			}
		}
		req.done <- nil
	}
}

func (c *Coordinator) deferRequest(req pendingRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deferred = append(c.deferred, req)
}

func (c *Coordinator) failQueued() {
	for {
		select {
		case req := <-c.requests:
			req.done <- ErrClosed
		default:
			return
		}
	}
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
	return cfg
}
