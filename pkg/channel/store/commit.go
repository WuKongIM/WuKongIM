package store

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

const (
	defaultCommitCoordinatorFlushWindow = 200 * time.Microsecond
	defaultCommitCoordinatorQueueSize   = 1024
)

type commitRequest struct {
	channelKey channel.ChannelKey
	build      func(*pebble.Batch) error
	publish    func() error
	done       chan error
}

type commitCoordinator struct {
	db          *pebble.DB
	flushWindow time.Duration
	commit      func(*pebble.Batch) error

	requests       chan commitRequest
	stopAcceptCh   chan struct{}
	stopCh         chan struct{}
	doneCh         chan struct{}
	closeOnce      sync.Once
	stopAcceptOnce sync.Once
	acceptMu       sync.RWMutex
	submitWG       sync.WaitGroup
	batchMu        sync.Mutex
}

func newCommitCoordinator(db *pebble.DB) *commitCoordinator {
	c := &commitCoordinator{
		db:          db,
		flushWindow: defaultCommitCoordinatorFlushWindow,
		commit: func(batch *pebble.Batch) error {
			return batch.Commit(pebble.Sync)
		},
		requests:     make(chan commitRequest, defaultCommitCoordinatorQueueSize),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *commitCoordinator) submit(req commitRequest) error {
	if c == nil || c.db == nil {
		return channel.ErrInvalidArgument
	}
	if req.build == nil {
		return channel.ErrInvalidArgument
	}
	if req.done == nil {
		req.done = make(chan error, 1)
	}

	c.acceptMu.RLock()
	select {
	case <-c.stopAcceptCh:
		c.acceptMu.RUnlock()
		return channel.ErrInvalidArgument
	default:
	}
	c.submitWG.Add(1)
	c.acceptMu.RUnlock()

	select {
	case <-c.doneCh:
		c.submitWG.Done()
		return channel.ErrInvalidArgument
	case c.requests <- req:
		c.submitWG.Done()
	}

	return c.awaitRequestResult(req.done)
}

func (c *commitCoordinator) awaitRequestResult(done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-c.doneCh:
		select {
		case err := <-done:
			return err
		default:
			return channel.ErrInvalidArgument
		}
	}
}

func (c *commitCoordinator) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.stopAccepting()
		c.batchMu.Lock()
		close(c.stopCh)
		c.batchMu.Unlock()
		<-c.doneCh
	})
}

func (c *commitCoordinator) run() {
	defer close(c.doneCh)

	for {
		select {
		case <-c.stopCh:
			c.failPendingRequests(channel.ErrInvalidArgument)
			return
		case req := <-c.requests:
			c.batchMu.Lock()
			if c.stopRequested() {
				c.batchMu.Unlock()
				req.done <- channel.ErrInvalidArgument
				c.failPendingRequests(channel.ErrInvalidArgument)
				return
			}
			batch := c.collectBatch(req)
			if batch.closed {
				c.batchMu.Unlock()
				batch.completeAll(channel.ErrInvalidArgument)
				c.failPendingRequests(channel.ErrInvalidArgument)
				return
			}
			if err := batch.commit(c.db, c.commit); err != nil {
				c.batchMu.Unlock()
				batch.completeAll(err)
				continue
			}
			batch.publish()
			c.batchMu.Unlock()
		}
	}
}

func (c *commitCoordinator) collectBatch(first commitRequest) commitBatch {
	batch := commitBatch{requests: []commitRequest{first}}
	if c.flushWindow <= 0 {
		for {
			if c.stopRequested() {
				batch.closed = true
				return batch
			}
			select {
			case <-c.stopAcceptCh:
				batch.closed = true
				return batch
			case req := <-c.requests:
				batch.requests = append(batch.requests, req)
			default:
				return batch
			}
		}
	}

	timer := time.NewTimer(c.flushWindow)
	defer timer.Stop()
	for {
		select {
		case req := <-c.requests:
			batch.requests = append(batch.requests, req)
		case <-timer.C:
			return batch
		case <-c.stopAcceptCh:
			batch.closed = true
			return batch
		}
	}
}

func (c *commitCoordinator) stopRequested() bool {
	select {
	case <-c.stopAcceptCh:
		return true
	default:
		return false
	}
}

func (c *commitCoordinator) stopAccepting() {
	c.stopAcceptOnce.Do(func() {
		c.acceptMu.Lock()
		close(c.stopAcceptCh)
		c.acceptMu.Unlock()
		c.submitWG.Wait()
	})
}

func (c *commitCoordinator) failPendingRequests(err error) {
	for {
		select {
		case req := <-c.requests:
			req.done <- err
		default:
			return
		}
	}
}

type commitBatch struct {
	requests []commitRequest
	closed   bool
}

func (b commitBatch) commit(db *pebble.DB, commit func(*pebble.Batch) error) error {
	if db == nil || commit == nil {
		return channel.ErrInvalidArgument
	}

	writeBatch := db.NewBatch()
	defer writeBatch.Close()

	for _, req := range b.requests {
		if req.build == nil {
			return channel.ErrInvalidArgument
		}
		if err := req.build(writeBatch); err != nil {
			return err
		}
	}
	return commit(writeBatch)
}

func (b commitBatch) publish() {
	for _, req := range b.requests {
		var err error
		if req.publish != nil {
			err = req.publish()
		}
		req.done <- err
	}
}

func (b commitBatch) completeAll(err error) {
	for _, req := range b.requests {
		req.done <- err
	}
}

func (s *ChannelStore) StoreApplyFetch(req channel.ApplyFetchStoreRequest) (uint64, error) {
	return s.applyFetchedRecords(req.Records, req.Checkpoint)
}

func (s *ChannelStore) applyFetchedRecords(records []channel.Record, checkpoint *channel.Checkpoint) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	base, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if len(records) == 0 && checkpoint == nil {
		s.mu.Unlock()
		return base, nil
	}
	s.writeInProgress.Store(true)
	s.mu.Unlock()
	defer s.failPendingWrite()

	nextLEO := base + uint64(len(records))
	if coordinator := s.commitCoordinator(); coordinator != nil {
		err := coordinator.submit(commitRequest{
			channelKey: s.key,
			build: func(writeBatch *pebble.Batch) error {
				return s.writeApplyFetchedRecords(writeBatch, base, records, checkpoint)
			},
			publish: func() error {
				s.publishDurableWrite(nextLEO)
				return nil
			},
		})
		if err != nil {
			return 0, err
		}
		return nextLEO, nil
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()

	if err := s.writeApplyFetchedRecords(batch, base, records, checkpoint); err != nil {
		return 0, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, err
	}
	s.publishDurableWrite(nextLEO)
	return nextLEO, nil
}

func (s *ChannelStore) writeApplyFetchedRecords(writeBatch *pebble.Batch, base uint64, records []channel.Record, checkpoint *channel.Checkpoint) error {
	rows, err := structuredRowsFromCompatibilityRecords(base+1, records)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		if err := s.messageTable().append(writeBatch, rows); err != nil {
			return err
		}
	}
	if checkpoint != nil {
		if err := s.writeCheckpoint(writeBatch, *checkpoint); err != nil {
			return err
		}
	}
	return nil
}
