package store

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/cockroachdb/pebble/v2"
)

const (
	defaultCommitCoordinatorFlushWindow = 200 * time.Microsecond
	defaultCommitCoordinatorQueueSize   = 1024
)

// CommitCoordinatorConfig controls cross-channel Pebble sync batching.
type CommitCoordinatorConfig struct {
	// FlushWindow is the maximum time spent collecting adjacent commit requests.
	FlushWindow time.Duration
	// QueueSize bounds waiting commit requests before callers apply backpressure.
	QueueSize int
	// MaxRequests caps how many logical commit requests one Pebble sync may include.
	MaxRequests int
	// MaxRecords caps how many channel log records one Pebble sync may include.
	MaxRecords int
	// MaxBytes caps the approximate payload bytes one Pebble sync may include.
	MaxBytes int
}

type commitRequest struct {
	channelKey  channel.ChannelKey
	recordCount int
	byteCount   int
	enqueuedAt  time.Time
	build       func(*pebble.Batch) error
	publish     func() error
	done        chan error
}

type commitCoordinator struct {
	db     *pebble.DB
	cfg    CommitCoordinatorConfig
	commit func(*pebble.Batch) error

	requests       chan commitRequest
	stopAcceptCh   chan struct{}
	stopCh         chan struct{}
	doneCh         chan struct{}
	closeOnce      sync.Once
	stopAcceptOnce sync.Once
	acceptMu       sync.RWMutex
	submitWG       sync.WaitGroup
	batchMu        sync.Mutex
	// deferred stores requests that were read while collecting a batch but would exceed its configured limits.
	deferred []commitRequest
}

func newCommitCoordinator(db *pebble.DB) *commitCoordinator {
	return newCommitCoordinatorWithConfig(db, CommitCoordinatorConfig{})
}

func newCommitCoordinatorWithConfig(db *pebble.DB, cfg CommitCoordinatorConfig) *commitCoordinator {
	cfg = effectiveCommitCoordinatorConfig(cfg)
	c := &commitCoordinator{
		db:  db,
		cfg: cfg,
		commit: func(batch *pebble.Batch) error {
			return batch.Commit(pebble.Sync)
		},
		requests:     make(chan commitRequest, cfg.QueueSize),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	go c.run()
	return c
}

func effectiveCommitCoordinatorConfig(cfg CommitCoordinatorConfig) CommitCoordinatorConfig {
	if cfg.FlushWindow == 0 {
		cfg.FlushWindow = defaultCommitCoordinatorFlushWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultCommitCoordinatorQueueSize
	}
	return cfg
}

func (c *commitCoordinator) configure(cfg CommitCoordinatorConfig) {
	if c == nil {
		return
	}
	cfg = effectiveCommitCoordinatorConfig(cfg)
	c.batchMu.Lock()
	defer c.batchMu.Unlock()
	c.cfg.FlushWindow = cfg.FlushWindow
	c.cfg.MaxRequests = cfg.MaxRequests
	c.cfg.MaxRecords = cfg.MaxRecords
	c.cfg.MaxBytes = cfg.MaxBytes
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
	if req.enqueuedAt.IsZero() {
		req.enqueuedAt = time.Now()
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
		req, ok := c.nextRequest()
		if !ok {
			return
		}
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

func (c *commitCoordinator) nextRequest() (commitRequest, bool) {
	c.batchMu.Lock()
	if len(c.deferred) > 0 {
		req := c.deferred[0]
		copy(c.deferred, c.deferred[1:])
		c.deferred = c.deferred[:len(c.deferred)-1]
		c.batchMu.Unlock()
		return req, true
	}
	c.batchMu.Unlock()

	select {
	case <-c.stopCh:
		c.failPendingRequests(channel.ErrInvalidArgument)
		return commitRequest{}, false
	case req := <-c.requests:
		return req, true
	}
}

func (c *commitCoordinator) collectBatch(first commitRequest) commitBatch {
	collectStartedAt := time.Now()
	batch := commitBatch{requests: []commitRequest{first}}
	if c.batchLimitReached(batch) {
		c.recordBatchCollect(batch, collectStartedAt, time.Now())
		return batch
	}
	if c.cfg.FlushWindow <= 0 {
		for {
			if c.stopRequested() {
				batch.closed = true
				c.recordBatchCollect(batch, collectStartedAt, time.Now())
				return batch
			}
			select {
			case <-c.stopAcceptCh:
				batch.closed = true
				c.recordBatchCollect(batch, collectStartedAt, time.Now())
				return batch
			case req := <-c.requests:
				if c.batchWouldExceedLimit(batch, req) {
					c.deferRequest(req)
					c.recordBatchCollect(batch, collectStartedAt, time.Now())
					return batch
				}
				batch.requests = append(batch.requests, req)
				if c.batchLimitReached(batch) {
					c.recordBatchCollect(batch, collectStartedAt, time.Now())
					return batch
				}
			default:
				c.recordBatchCollect(batch, collectStartedAt, time.Now())
				return batch
			}
		}
	}

	timer := time.NewTimer(c.cfg.FlushWindow)
	defer timer.Stop()
	for {
		select {
		case req := <-c.requests:
			if c.batchWouldExceedLimit(batch, req) {
				c.deferRequest(req)
				c.recordBatchCollect(batch, collectStartedAt, time.Now())
				return batch
			}
			batch.requests = append(batch.requests, req)
			if c.batchLimitReached(batch) {
				c.recordBatchCollect(batch, collectStartedAt, time.Now())
				return batch
			}
		case <-timer.C:
			c.recordBatchCollect(batch, collectStartedAt, time.Now())
			return batch
		case <-c.stopAcceptCh:
			batch.closed = true
			c.recordBatchCollect(batch, collectStartedAt, time.Now())
			return batch
		}
	}
}

func (c *commitCoordinator) deferRequest(req commitRequest) {
	c.deferred = append(c.deferred, req)
}

func (c *commitCoordinator) recordBatchCollect(batch commitBatch, startedAt, doneAt time.Time) {
	requests, records, bytes := batch.stats()
	sendtrace.Record(sendtrace.Event{
		Stage:        sendtrace.StageStoreCommitBatchCollect,
		At:           startedAt,
		Duration:     sendtrace.Elapsed(startedAt, doneAt),
		ChannelKey:   batch.channelKey(),
		RequestCount: requests,
		RecordCount:  records,
		ByteCount:    bytes,
		QueueDepth:   c.queueDepth(),
		Result:       sendtrace.ResultOK,
	})
}

func (c *commitCoordinator) batchWouldExceedLimit(batch commitBatch, req commitRequest) bool {
	if c == nil {
		return false
	}
	requests, records, bytes := batch.stats()
	return (c.cfg.MaxRequests > 0 && requests+1 > c.cfg.MaxRequests) ||
		(c.cfg.MaxRecords > 0 && records+req.recordCount > c.cfg.MaxRecords) ||
		(c.cfg.MaxBytes > 0 && bytes+req.byteCount > c.cfg.MaxBytes)
}

func (c *commitCoordinator) batchLimitReached(batch commitBatch) bool {
	if c == nil {
		return false
	}
	requests, records, bytes := batch.stats()
	return (c.cfg.MaxRequests > 0 && requests >= c.cfg.MaxRequests) ||
		(c.cfg.MaxRecords > 0 && records >= c.cfg.MaxRecords) ||
		(c.cfg.MaxBytes > 0 && bytes >= c.cfg.MaxBytes)
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
	for _, req := range c.deferred {
		req.done <- err
	}
	c.deferred = nil
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

func (b commitBatch) stats() (requests int, records int, bytes int) {
	requests = len(b.requests)
	for _, req := range b.requests {
		records += req.recordCount
		bytes += req.byteCount
	}
	return requests, records, bytes
}

func (b commitBatch) commit(db *pebble.DB, commit func(*pebble.Batch) error) error {
	if db == nil || commit == nil {
		return channel.ErrInvalidArgument
	}

	commitStartedAt := time.Now()
	requests, records, bytes := b.stats()
	for _, req := range b.requests {
		sendtrace.Record(sendtrace.Event{
			Stage:        sendtrace.StageStoreCommitQueueWait,
			At:           req.enqueuedAt,
			Duration:     sendtrace.Elapsed(req.enqueuedAt, commitStartedAt),
			ChannelKey:   string(req.channelKey),
			RequestCount: 1,
			RecordCount:  req.recordCount,
			ByteCount:    req.byteCount,
			Result:       sendtrace.ResultOK,
		})
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
	syncStartedAt := time.Now()
	err := commit(writeBatch)
	sendtrace.Record(sendtrace.Event{
		Stage:        sendtrace.StageStoreCommitPebbleSync,
		At:           syncStartedAt,
		Duration:     sendtrace.Elapsed(syncStartedAt, time.Now()),
		ChannelKey:   b.channelKey(),
		RequestCount: requests,
		RecordCount:  records,
		ByteCount:    bytes,
		Result:       commitTraceResult(err),
		ErrorCode:    commitTraceErrorCode(err),
		Error:        commitTraceError(err),
	})
	return err
}

func (b commitBatch) publish() {
	startedAt := time.Now()
	requests, records, bytes := b.stats()
	var publishErr error
	for _, req := range b.requests {
		var err error
		if req.publish != nil {
			err = req.publish()
		}
		if err != nil && publishErr == nil {
			publishErr = err
		}
		req.done <- err
	}
	sendtrace.Record(sendtrace.Event{
		Stage:        sendtrace.StageStoreCommitPublish,
		At:           startedAt,
		Duration:     sendtrace.Elapsed(startedAt, time.Now()),
		ChannelKey:   b.channelKey(),
		RequestCount: requests,
		RecordCount:  records,
		ByteCount:    bytes,
		Result:       commitTraceResult(publishErr),
		ErrorCode:    commitTraceErrorCode(publishErr),
		Error:        commitTraceError(publishErr),
	})
}

func (b commitBatch) completeAll(err error) {
	for _, req := range b.requests {
		req.done <- err
	}
}

func (b commitBatch) channelKey() string {
	if len(b.requests) == 0 {
		return ""
	}
	return string(b.requests[0].channelKey)
}

func (c *commitCoordinator) queueDepth() int {
	if c == nil || c.requests == nil {
		return 0
	}
	return len(c.requests) + len(c.deferred)
}

func commitTraceResult(err error) sendtrace.Result {
	if err == nil {
		return sendtrace.ResultOK
	}
	return sendtrace.ResultError
}

func commitTraceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return "unknown_error"
}

func commitTraceError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 256 {
		return msg[:256]
	}
	return msg
}

func (s *ChannelStore) StoreApplyFetch(req channel.ApplyFetchStoreRequest) (uint64, error) {
	return s.applyFetchedRecords(req, nil)
}

// StoreApplyFetchWithEpoch atomically persists fetched records, an optional
// checkpoint, and an optional epoch boundary in the same Pebble batch.
func (s *ChannelStore) StoreApplyFetchWithEpoch(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	return s.applyFetchedRecords(req, epochPoint)
}

func (s *ChannelStore) applyFetchedRecords(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	records := req.Records
	checkpoint := req.Checkpoint

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	base, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if len(records) == 0 && checkpoint == nil && epochPoint == nil {
		s.mu.Unlock()
		return base, nil
	}
	nextLEO := base + uint64(len(records))
	if checkpoint != nil {
		if err := validateStoreCheckpoint(*checkpoint); err != nil {
			s.mu.Unlock()
			return 0, err
		}
		if checkpoint.HW < req.PreviousCommittedHW {
			s.mu.Unlock()
			return 0, channel.ErrCorruptState
		}
		if checkpoint.HW > nextLEO {
			s.mu.Unlock()
			return 0, channel.ErrCorruptState
		}
	}
	if epochPoint != nil && epochPoint.StartOffset != base {
		s.mu.Unlock()
		return 0, channel.ErrCorruptState
	}
	var historyPoint *channel.EpochPoint
	if epochPoint != nil {
		points, err := s.loadHistoryOrEmpty()
		if err != nil {
			s.mu.Unlock()
			return 0, err
		}
		needsAppend, err := shouldAppendHistoryPoint(points, *epochPoint)
		if err != nil {
			s.mu.Unlock()
			return 0, err
		}
		if needsAppend {
			point := *epochPoint
			historyPoint = &point
		}
	}
	if checkpoint != nil {
		s.checkpointMu.Lock()
		defer s.checkpointMu.Unlock()
		if err := s.validateCheckpointMonotonicAgainstCurrent(*checkpoint); err != nil {
			s.mu.Unlock()
			return 0, err
		}
	}
	s.writeInProgress.Store(true)
	s.mu.Unlock()
	defer s.failPendingWrite()

	if coordinator := s.commitCoordinator(); coordinator != nil {
		err := coordinator.submit(commitRequest{
			channelKey:  s.key,
			recordCount: len(records),
			byteCount:   recordByteCount(records),
			build: func(writeBatch *pebble.Batch) error {
				return s.writeApplyFetchedRecords(writeBatch, base, records, checkpoint, historyPoint)
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

	if err := s.writeApplyFetchedRecords(batch, base, records, checkpoint, historyPoint); err != nil {
		return 0, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, err
	}
	s.publishDurableWrite(nextLEO)
	return nextLEO, nil
}

func recordByteCount(records []channel.Record) int {
	total := 0
	for _, record := range records {
		if record.SizeBytes > 0 {
			total += record.SizeBytes
			continue
		}
		total += len(record.Payload)
	}
	return total
}

func (s *ChannelStore) writeApplyFetchedRecords(writeBatch *pebble.Batch, base uint64, records []channel.Record, checkpoint *channel.Checkpoint, epochPoint *channel.EpochPoint) error {
	rows, err := s.appendRowsFromCompatibilityRecordsLocked(base+1, records)
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
	if epochPoint != nil {
		if err := s.writeHistoryPoint(writeBatch, *epochPoint); err != nil {
			return err
		}
	}
	return nil
}
