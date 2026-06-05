package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
)

// appendQueueConfig bounds one channel's append queue and flush batch size.
type appendQueueConfig struct {
	// MaxRecords is the record count that triggers a store append flush.
	MaxRecords int
	// MaxBytes is the payload byte budget that triggers a store append flush.
	MaxBytes int
	// MaxWait is the maximum age of the oldest queued append before flushing.
	MaxWait time.Duration
	// MaxPending bounds queued client append requests.
	MaxPending int
	// MaxPendingBytes bounds queued client append payload bytes.
	MaxPendingBytes int
}

// appendQueue owns pending append requests before the reactor proposes a durable batch.
// It is the per-channel queue primitive for the Task 5 async append flush path;
// store appends run on the bounded async worker pool after the reactor proposes a batch.
type appendQueue struct {
	cfg appendQueueConfig
	// pending preserves client request order for deterministic batching.
	pending []appendRequest
	// records is the total queued record count.
	records int
	// bytes is the total queued payload byte count.
	bytes int
	// flushDue is the deadline derived from the oldest queued request.
	flushDue time.Time
	// storeBlocked prevents new flushes while one store append is in flight.
	storeBlocked bool
}

// appendRequest is one queued client append request.
type appendRequest struct {
	opID   ch.OpID
	req    ch.AppendBatchRequest
	future *Future
	// ctx lets the owning reactor drop not-yet-started appends after caller cancellation.
	ctx        context.Context
	enqueuedAt time.Time
	records    []ch.Record
	commitMode ch.CommitMode
	// traceItems preserves selected transient trace sidecars across restore/retry.
	traceItems []appendTraceItem
	// traceEvaluated records that detail sampling already ran for this request.
	traceEvaluated bool
}

type appendTiming struct {
	mode                 ch.CommitMode
	enqueuedAt           time.Time
	storeSubmittedAt     time.Time
	storeCompletedAt     time.Time
	targetOffset         uint64
	followerPullServedAt time.Time
	ackOffsetObservedAt  time.Time
	hwAdvancedAt         time.Time
	traceItems           []appendTraceItem
	recordCount          int
}

// appendBatch is one durable store append assembled from queued requests.
type appendBatch struct {
	batchOpID ch.OpID
	fence     ch.Fence
	requests  []appendRequest
	records   []ch.Record
	trace     appendTraceBatch
}

func newAppendQueue(cfg appendQueueConfig) appendQueue {
	return appendQueue{cfg: cfg}
}

// PendingRequests returns the number of queued append requests.
func (q *appendQueue) PendingRequests() int {
	if q == nil {
		return 0
	}
	return len(q.pending)
}

// PendingBytes returns the queued append payload bytes.
func (q *appendQueue) PendingBytes() int {
	if q == nil {
		return 0
	}
	return q.bytes
}

func (q *appendQueue) push(req appendRequest) error {
	if q == nil {
		return ch.ErrClosed
	}
	if len(req.records) == 0 {
		return ch.ErrInvalidConfig
	}
	reqBytes := recordsBytes(req.records)
	if q.cfg.MaxPending > 0 && len(q.pending)+1 > q.cfg.MaxPending {
		return ch.ErrBackpressured
	}
	if q.cfg.MaxPendingBytes > 0 && q.bytes+reqBytes > q.cfg.MaxPendingBytes {
		return ch.ErrBackpressured
	}
	q.pending = append(q.pending, req)
	q.records += len(req.records)
	q.bytes += reqBytes
	if len(q.pending) == 1 && q.cfg.MaxWait > 0 {
		q.flushDue = req.enqueuedAt.Add(q.cfg.MaxWait)
	}
	return nil
}

func (q *appendQueue) shouldFlush(now time.Time) bool {
	if q == nil || q.storeBlocked || len(q.pending) == 0 {
		return false
	}
	if q.cfg.MaxRecords > 0 && q.records >= q.cfg.MaxRecords {
		return true
	}
	if q.cfg.MaxBytes > 0 && q.bytes >= q.cfg.MaxBytes {
		return true
	}
	return !q.flushDue.IsZero() && !now.Before(q.flushDue)
}

func (q *appendQueue) popBatch(batchOpID ch.OpID, state *machine.ChannelState) appendBatch {
	if q == nil || len(q.pending) == 0 {
		return appendBatch{batchOpID: batchOpID, fence: appendQueueFence(batchOpID, state)}
	}
	take := q.batchRequestCount()
	requests := append([]appendRequest(nil), q.pending[:take]...)
	batch := appendBatch{
		batchOpID: batchOpID,
		fence:     appendQueueFence(batchOpID, state),
		requests:  requests,
		records:   make([]ch.Record, 0, requestsRecordCount(requests)),
	}
	for _, req := range requests {
		batch.records = append(batch.records, req.records...)
	}
	q.pending = append([]appendRequest(nil), q.pending[take:]...)
	q.storeBlocked = true
	q.recount()
	return batch
}

func (q *appendQueue) restoreFront(batch appendBatch) {
	if q == nil || len(batch.requests) == 0 {
		return
	}
	requests := append([]appendRequest(nil), batch.requests...)
	if batch.trace.evaluated {
		for reqIdx := range requests {
			requests[reqIdx].traceItems = traceItemsForRequest(batch.trace.items, reqIdx)
			requests[reqIdx].traceEvaluated = true
		}
	}
	restored := make([]appendRequest, 0, len(batch.requests)+len(q.pending))
	restored = append(restored, requests...)
	restored = append(restored, q.pending...)
	q.pending = restored
	q.storeBlocked = false
	q.recount()
}

func (q *appendQueue) remove(opID ch.OpID) (*appendRequest, bool) {
	if q == nil {
		return nil, false
	}
	for i := range q.pending {
		if q.pending[i].opID != opID {
			continue
		}
		removed := q.pending[i]
		copy(q.pending[i:], q.pending[i+1:])
		last := len(q.pending) - 1
		q.pending[last] = appendRequest{}
		q.pending = q.pending[:last]
		q.recount()
		return &removed, true
	}
	return nil, false
}

func (q *appendQueue) failAll(err error) {
	if q == nil {
		return
	}
	for _, req := range q.pending {
		if req.future != nil {
			req.future.Complete(Result{Err: err})
		}
	}
	q.pending = nil
	q.records = 0
	q.bytes = 0
	q.flushDue = time.Time{}
	q.storeBlocked = false
}

func (q *appendQueue) clear() {
	if q == nil {
		return
	}
	q.pending = nil
	q.records = 0
	q.bytes = 0
	q.flushDue = time.Time{}
	q.storeBlocked = false
}

func (q *appendQueue) recount() {
	q.records = 0
	q.bytes = 0
	q.flushDue = time.Time{}
	for i, req := range q.pending {
		q.records += len(req.records)
		q.bytes += recordsBytes(req.records)
		if i == 0 && q.cfg.MaxWait > 0 {
			q.flushDue = req.enqueuedAt.Add(q.cfg.MaxWait)
		}
	}
}

func (q *appendQueue) batchRequestCount() int {
	take := 0
	records := 0
	bytes := 0
	for _, req := range q.pending {
		reqRecords := len(req.records)
		reqBytes := recordsBytes(req.records)
		if take > 0 && q.cfg.MaxRecords > 0 && records+reqRecords > q.cfg.MaxRecords {
			break
		}
		if take > 0 && q.cfg.MaxBytes > 0 && bytes+reqBytes > q.cfg.MaxBytes {
			break
		}
		take++
		records += reqRecords
		bytes += reqBytes
	}
	if take == 0 {
		return 1
	}
	return take
}

func appendQueueFence(opID ch.OpID, state *machine.ChannelState) ch.Fence {
	fence := ch.Fence{OpID: opID}
	if state == nil {
		return fence
	}
	fence.ChannelKey = state.Key
	fence.Generation = state.Generation
	fence.Epoch = state.Epoch
	fence.LeaderEpoch = state.LeaderEpoch
	return fence
}

func requestsRecordCount(requests []appendRequest) int {
	total := 0
	for _, req := range requests {
		total += len(req.records)
	}
	return total
}

func recordsBytes(records []ch.Record) int {
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
