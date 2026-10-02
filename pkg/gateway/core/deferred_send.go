package core

import (
	"errors"
	"sync"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

var errDeferredSendCompletion = errors.New("gateway: invalid deferred SEND completion")

// deferredSendShard owns active session chains only. The shard mutex protects
// links and ownership flags, never handler preparation or a publication callback.
type deferredSendShard struct {
	mu    sync.Mutex
	lanes map[*sessionState]*deferredSendLane
}
type deferredSendLane struct {
	state      *sessionState
	head, tail *deferredSendRecord
	publishing bool
}
type deferredSendRecord struct {
	job                        *deferredSendBatch
	lane                       *deferredSendLane
	next                       *deferredSendRecord
	task                       asyncDispatchTask
	write                      func() error
	err                        error
	ready, published, released bool
	// errorReported prevents item cleanup from reporting the batch failure again.
	errorReported bool
}

// deferredSendBatch keeps early inline completions from releasing frame ownership
// before handler preparation returns. Record slots release individually thereafter.
type deferredSendBatch struct {
	executor                    *sendExecutor
	shard                       int
	owner                       *deferredSendShard
	records                     []deferredSendRecord
	started                     time.Time
	sealed, completed, returned bool
}

func (e *sendExecutor) dispatchDeferredMailboxBatch(shard int, tasks []asyncDispatchTask) {
	limits := gatewaySendBatchLimits(e.server)
	start, bytes := 0, 0
	for i, task := range tasks {
		// Independent protocol packets share this shard's ordered worker, but
		// never enter WK SEND preparation or retain its publication records.
		if task.packet != nil {
			if i > start {
				e.dispatchDeferredBatch(shard, tasks[start:i])
			}
			e.dispatchJoinedMailboxBatch(shard, tasks[i:i+1])
			start, bytes = i+1, 0
			continue
		}
		size := asyncDispatchTaskByteCount(task)
		if i > start && limits.maxBytes > 0 && bytes+size > limits.maxBytes {
			e.dispatchDeferredBatch(shard, tasks[start:i])
			start = i
			bytes = 0
		}
		bytes += size
		if limits.maxRecords > 0 && i-start+1 >= limits.maxRecords {
			e.dispatchDeferredBatch(shard, tasks[start:i+1])
			start = i + 1
			bytes = 0
		}
	}
	if start < len(tasks) {
		e.dispatchDeferredBatch(shard, tasks[start:])
	}
}

// dispatchDeferredBatch runs on the existing ordered mailbox preparation worker.
// It copies task descriptors before the mailbox can recycle its batch slice.
func (e *sendExecutor) dispatchDeferredBatch(shard int, tasks []asyncDispatchTask) {
	job := &deferredSendBatch{executor: e, shard: shard, owner: &e.deferred[shard], records: make([]deferredSendRecord, len(tasks)), started: time.Now()}
	owner := job.owner
	owner.mu.Lock()
	if owner.lanes == nil {
		owner.lanes = make(map[*sessionState]*deferredSendLane)
	}
	for i, task := range tasks {
		lane := owner.lanes[task.state]
		if lane == nil {
			lane = &deferredSendLane{state: task.state}
			owner.lanes[task.state] = lane
		}
		record := &job.records[i]
		*record = deferredSendRecord{job: job, lane: lane, task: task}
		if lane.tail == nil {
			lane.head = record
		} else {
			lane.tail.next = record
		}
		lane.tail = record
	}
	owner.mu.Unlock()
	defer func() {
		if v := recover(); v != nil {
			e.recordPanic(v, firstAsyncDispatchTask(tasks))
			job.finish(errDeferredSendCompletion)
		}
		job.markReturned()
	}()
	e.server.observeAsyncSendQueue(e)
	e.server.observeAsyncSendBatch(tasks)
	for _, task := range tasks {
		e.server.recordAsyncDispatchWait(task)
	}
	items := e.server.sendBatchItems(tasks)
	if len(items) != len(tasks) {
		job.finish(errDeferredSendCompletion)
		return
	}
	if err := e.server.dispatcher.deferredHandler.OnSendBatchDeferred(items, job.publish, job.finish); err != nil {
		job.finish(err)
	}
}

func (b *deferredSendBatch) publish(index int, write func() error) error {
	b.owner.mu.Lock()
	if index < 0 || index >= len(b.records) || write == nil || b.sealed || b.records[index].ready {
		b.owner.mu.Unlock()
		return errDeferredSendCompletion
	}
	record := &b.records[index]
	record.ready = true
	record.write = write
	run := record.lane.startLocked()
	b.owner.mu.Unlock()
	if run {
		b.runLane(record.lane)
	}
	return nil
}

func (l *deferredSendLane) startLocked() bool {
	if l.publishing || l.head == nil || !l.head.ready {
		return false
	}
	l.publishing = true
	return true
}

// finish seals handler ownership and turns any missing result into a terminal
// failure. Buffered results keep their session position until the head completes.
func (b *deferredSendBatch) finish(err error) {
	b.owner.mu.Lock()
	if b.sealed {
		b.owner.mu.Unlock()
		return
	}
	b.sealed = true
	if err == nil {
		for i := range b.records {
			if !b.records[i].ready {
				err = errDeferredSendCompletion
				break
			}
		}
	}
	lanes := make([]*deferredSendLane, 0)
	for i := range b.records {
		record := &b.records[i]
		if !record.ready {
			record.ready = true
			record.err = err
			record.errorReported = true
		}
		if record.lane.startLocked() {
			lanes = append(lanes, record.lane)
		}
	}
	b.owner.mu.Unlock()
	if err != nil {
		seen := make(map[*sessionState]struct{}, len(b.records))
		for i := range b.records {
			state := b.records[i].lane.state
			if _, ok := seen[state]; ok {
				continue
			}
			seen[state] = struct{}{}
			b.executor.server.handleHandlerError(state, err)
		}
	}
	for _, lane := range lanes {
		b.runLane(lane)
	}
	b.owner.mu.Lock()
	b.completed = true
	b.owner.mu.Unlock()
	b.releasePublished()
}

func (b *deferredSendBatch) markReturned() {
	b.owner.mu.Lock()
	b.returned = true
	b.owner.mu.Unlock()
	b.releasePublished()
}

// releasePublished needs both preparation fences as well as actual publication.
// The original admission slots, not an additional completion queue, bound storage.
func (b *deferredSendBatch) releasePublished() {
	b.owner.mu.Lock()
	count := 0
	if b.completed && b.returned {
		for i := range b.records {
			record := &b.records[i]
			if record.published && !record.released {
				record.released = true
				record.task = asyncDispatchTask{}
				count++
			}
		}
	}
	b.owner.mu.Unlock()
	b.releaseCount(count)
}

func (b *deferredSendBatch) releaseCount(count int) {
	if count == 0 {
		return
	}
	b.executor.consumeShard(b.shard, count)
	b.executor.consume(count)
	b.executor.server.observeAsyncSendQueue(b.executor)
	for range count {
		b.executor.completeAdmission()
	}
}

// runLane is claimed under the shard mutex but executes every callback outside
// it. Concurrent result arrivals may start other sessions, never a second writer
// for this exact session. Empty session chains leave no historical map state.
func (b *deferredSendBatch) runLane(lane *deferredSendLane) {
	for {
		b.owner.mu.Lock()
		record := lane.head
		if record == nil || !record.ready {
			lane.publishing = false
			b.owner.mu.Unlock()
			return
		}
		write, err, task := record.write, record.err, record.task
		errorReported := record.errorReported
		b.owner.mu.Unlock()
		if err == nil && task.state != nil && task.state.isClosed() {
			err = gatewaytypes.ErrSessionClosed
		}
		if err == nil && write != nil {
			err = record.job.invokePublication(task, write)
		}
		e := record.job.executor
		e.server.observeFrameHandled(task.state, task.frame, time.Since(record.job.started), err)
		if err != nil && !errorReported {
			e.server.handleHandlerError(task.state, err)
		}
		b.owner.mu.Lock()
		record.published = true
		record.write = nil
		lane.head = record.next
		record.next = nil
		if lane.head == nil {
			lane.tail = nil
			delete(b.owner.lanes, lane.state)
		}
		b.owner.mu.Unlock()
		record.job.releaseRecord(record)
	}
}

// releaseRecord is constant-time on the common publication path; batch scans
// occur only at the two preparation/completion fences, not once per item.
func (b *deferredSendBatch) releaseRecord(record *deferredSendRecord) {
	b.owner.mu.Lock()
	release := b.completed && b.returned && !record.released
	if release {
		record.released = true
		record.task = asyncDispatchTask{}
	}
	b.owner.mu.Unlock()
	if release {
		b.releaseCount(1)
	}
}

func (b *deferredSendBatch) invokePublication(task asyncDispatchTask, write func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			b.executor.recordPanic(v, task)
			err = errDeferredSendCompletion
		}
	}()
	return write()
}
