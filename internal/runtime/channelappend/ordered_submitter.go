package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// BatchSender retains the existing routed append and item-aligned result contract.
// Implementations must join their routing calls before returning and be concurrency-safe.
// Durable work already admitted below a canceled caller retains its original owner.
type BatchSender interface {
	SendBatch([]SendBatchItem) []SendBatchItemResult
}

// OrderedSubmitterOptions bounds asynchronous ownership independently of caller waits.
type OrderedSubmitterOptions struct {
	// Workers is the maximum number of batches executing or publishing completion.
	Workers int
	// Capacity counts all admitted records, including running and callback-owned work.
	Capacity int
	// PayloadCapacity counts borrowed payload bytes through completion callback return.
	// Protocol metadata remains bounded by the caller's frame and record limits.
	PayloadCapacity int
	// BatchMaxRecords and BatchMaxBytes bound coalescing of already-ready jobs.
	// Zero disables coalescing. One admitted job is never split to meet a target.
	// Bytes count immutable payloads, while record capacity bounds descriptors.
	BatchMaxRecords int
	BatchMaxBytes   int
	// CommandChannelSuffix must match the wrapped Router's canonical key codec.
	CommandChannelSuffix string
	// Goroutines attributes fixed worker lifetimes to the channel append runtime.
	Goroutines *goruntimeregistry.Registry
}

// OrderedSubmitter separates admission from completion without reordering overlapping
// canonical Channel routing calls. Independent batches may execute concurrently. The caller must
// authorize and prepare items in session order before Submit, and retain its own
// outstanding reservation until any buffered results have been published.
type OrderedSubmitter struct {
	sender   BatchSender
	opts     OrderedSubmitterOptions
	channels runtimechannelid.CommandCodec
	mu       sync.Mutex
	ready    *sync.Cond
	// lanes retain only admitted key dependencies; empty keys are removed immediately.
	lanes map[ChannelID]*orderedSubmitLane
	// head and tail contain dependency-ready jobs, never a scan of blocked work.
	head, tail *orderedSubmitJob
	// records and payloadBytes include queued, executing and callback-owned inputs.
	records, payloadBytes int
	// queuedRecords and busyTasks keep waiting records distinct from executing batches.
	queuedRecords, busyTasks int
	closed                   bool
	// paused fences maintenance admission without terminating the worker generation.
	paused bool
	// idle closes when the current admitted generation, including callbacks, joins.
	idle chan struct{}
	// workers counts live owners; the last one retires pressure before closing done.
	workers        int
	done           chan struct{}
	rejected       atomic.Int64
	unregisterPool func()
}

type orderedSubmitLane struct{ head, tail *orderedSubmitLink }
type orderedSubmitLink struct {
	key  ChannelID
	job  *orderedSubmitJob
	next *orderedSubmitLink
}
type orderedSubmitJob struct {
	items        []SendBatchItem
	complete     func([]SendBatchItemResult)
	payloadBytes int
	// links contain one FIFO dependency per distinct canonical Channel in this batch.
	links   []orderedSubmitLink
	blocked int
	next    *orderedSubmitJob
}

// NewOrderedSubmitter starts a fixed number of node-owned workers. It does not
// construct routing, storage or product policy implementations.
func NewOrderedSubmitter(opts OrderedSubmitterOptions, sender BatchSender) (*OrderedSubmitter, error) {
	if sender == nil || opts.Workers <= 0 || opts.Capacity <= 0 || opts.PayloadCapacity <= 0 || opts.Workers > opts.Capacity || opts.BatchMaxRecords < 0 || opts.BatchMaxBytes < 0 {
		return nil, errors.New("channelappend: invalid ordered submitter configuration")
	}
	s := &OrderedSubmitter{sender: sender, opts: opts, channels: runtimechannelid.CommandCodec{Suffix: opts.CommandChannelSuffix}, lanes: make(map[ChannelID]*orderedSubmitLane), workers: opts.Workers, done: make(chan struct{})}
	s.idle = make(chan struct{})
	close(s.idle)
	s.ready = sync.NewCond(&s.mu)
	unregister, err := opts.Goroutines.RegisterPool(goruntimeregistry.TaskChannelAppendWorkerPool, s.poolStats)
	if err != nil {
		return nil, err
	}
	s.unregisterPool = unregister
	for range opts.Workers {
		goruntimeregistry.SafeGo(opts.Goroutines, goruntimeregistry.TaskChannelAppendWorkerPool, s.run)
	}
	return s, nil
}

// Submit atomically admits a batch or returns an error without invoking complete.
// Nil error transfers exactly one completion to this owner; it may run before
// Submit returns. Descriptors and recipient slices are copied, payload bytes must
// remain immutable. Completion must not synchronously call Close on this owner.
func (s *OrderedSubmitter) Submit(items []SendBatchItem, complete func([]SendBatchItemResult)) error {
	if s == nil {
		return ErrRouteNotReady
	}
	if len(items) == 0 || complete == nil {
		return errors.New("channelappend: invalid ordered submission")
	}
	if len(items) > s.opts.Capacity {
		s.rejected.Add(1)
		return ErrBackpressured
	}
	payloadBytes := 0
	for _, item := range items {
		if len(item.Command.Payload) > s.opts.PayloadCapacity-payloadBytes {
			s.rejected.Add(1)
			return ErrBackpressured
		}
		payloadBytes += len(item.Command.Payload)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.paused {
		return ErrRouteNotReady
	}
	if len(items) > s.opts.Capacity-s.records || payloadBytes > s.opts.PayloadCapacity-s.payloadBytes {
		s.rejected.Add(1)
		return ErrBackpressured
	}
	job := &orderedSubmitJob{items: append([]SendBatchItem(nil), items...), complete: complete, payloadBytes: payloadBytes}
	keys := make(map[ChannelID]struct{}, len(items))
	for i := range job.items {
		job.items[i].Command.MessageScopedUIDs = append([]string(nil), job.items[i].Command.MessageScopedUIDs...)
		key, _, terminal := preRouteChannel(job.items[i].Command, s.channels)
		if !terminal {
			keys[key] = struct{}{}
		}
	}
	job.links = make([]orderedSubmitLink, 0, len(keys))
	for key := range keys {
		job.links = append(job.links, orderedSubmitLink{key: key, job: job})
	}
	for i := range job.links {
		link := &job.links[i]
		lane := s.lanes[link.key]
		if lane == nil {
			lane = &orderedSubmitLane{}
			s.lanes[link.key] = lane
		}
		if lane.tail == nil {
			lane.head = link
		} else {
			lane.tail.next = link
			job.blocked++
		}
		lane.tail = link
	}
	if s.records == 0 {
		s.idle = make(chan struct{})
	}
	s.records += len(items)
	s.queuedRecords += len(items)
	s.payloadBytes += payloadBytes
	if job.blocked == 0 {
		s.enqueueReady(job)
	}
	return nil
}

// Close fences new admission and joins every accepted execution and callback.
// A caller timeout never cancels accepted work; later callers join the same drain.
// It does not replace the append Group's drain for writes that outlive a caller result.
func (s *OrderedSubmitter) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	s.closed = true
	s.ready.Broadcast()
	s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enqueueReady requires mu and schedules only the dependency-ready edge.
func (s *OrderedSubmitter) enqueueReady(job *orderedSubmitJob) {
	if s.tail == nil {
		s.head = job
	} else {
		s.tail.next = job
	}
	s.tail = job
	s.ready.Signal()
}

func (s *OrderedSubmitter) run() {
	for {
		s.mu.Lock()
		for s.head == nil && !(s.closed && s.records == 0) {
			s.ready.Wait()
		}
		if s.head == nil {
			s.workers--
			last := s.workers == 0
			s.mu.Unlock()
			if last {
				s.unregisterPool()
				close(s.done)
			}
			return
		}
		first, last := s.head, s.head
		records, payloadBytes := len(first.items), first.payloadBytes
		// Ready jobs have no unresolved Channel predecessor, including one another.
		// Merge only an already-queued prefix; never wait to fill a batch or split a job.
		if s.opts.BatchMaxRecords > 0 && s.opts.BatchMaxBytes > 0 {
			for next := last.next; next != nil; next = last.next {
				if len(next.items) > s.opts.BatchMaxRecords-records || next.payloadBytes > s.opts.BatchMaxBytes-payloadBytes {
					break
				}
				records += len(next.items)
				payloadBytes += next.payloadBytes
				last = next
			}
		}
		s.head = last.next
		last.next = nil
		if s.head == nil {
			s.tail = nil
		}
		s.queuedRecords -= records
		s.busyTasks++
		s.mu.Unlock()
		items := first.items
		if first != last {
			items = make([]SendBatchItem, 0, records)
			for job := first; job != nil; job = job.next {
				items = append(items, job.items...)
			}
		}
		// Preserve the registry's critical Router panic policy. Never invent successful
		// results or a completed drain after a routing or publication panic.
		results := normalizeRouterGroupResults(records, s.sender.SendBatch(items))
		offset := 0
		for job := first; job != nil; {
			next, count := job.next, len(job.items)
			job.complete(results[offset : offset+count : offset+count])
			// Release borrowed payload references before returning this job's budget,
			// even if a later callback in the merged execution blocks.
			clear(items[offset : offset+count])
			offset += count
			job.next = nil
			s.finish(job, next == nil)
			job = next
		}
	}
}

// finish releases exactly this job's ownership and wakes each newly ready successor.
// Linked dependencies are bounded by admitted records, with no historical key cache.
func (s *OrderedSubmitter) finish(job *orderedSubmitJob, batchDone bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range job.links {
		link := &job.links[i]
		lane := s.lanes[link.key]
		if lane == nil || lane.head != link {
			panic("channelappend: ordered submitter dependency mismatch")
		}
		lane.head = link.next
		if lane.head == nil {
			delete(s.lanes, link.key)
		} else {
			next := lane.head.job
			next.blocked--
			if next.blocked == 0 {
				s.enqueueReady(next)
			}
		}
		*link = orderedSubmitLink{}
	}
	s.records -= len(job.items)
	if batchDone {
		s.busyTasks--
	}
	s.payloadBytes -= job.payloadBytes
	job.items = nil
	job.links = nil
	job.complete = nil
	if s.records == 0 {
		close(s.idle)
	}
	if s.closed && s.records == 0 {
		s.ready.Broadcast()
	}
}

// poolStats reports records waiting separately from batches executing. SafeGo
// accounts for the direct worker goroutines, so they must not be counted twice.
func (s *OrderedSubmitter) poolStats() goruntimeregistry.PoolStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return goruntimeregistry.PoolStats{
		BusyTasks: int64(s.busyTasks), Capacity: int64(s.opts.Workers),
		QueueDepth: int64(s.queuedRecords), QueueCapacity: int64(s.opts.Capacity),
		RejectedTotal: s.rejected.Load(),
	}
}

// Pause fences new submissions and joins the same accepted routing/callback work.
// Timeout affects only this caller. Group still owns storage effects that outlive
// a caller result and must be drained separately before data replacement.
func (s *OrderedSubmitter) Pause(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	s.paused = true
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Resume reopens a fully joined maintenance fence, never a terminal Close.
// Composition must resume append/storage dependencies before calling it.
func (s *OrderedSubmitter) Resume() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrRouteNotReady
	}
	if s.records != 0 {
		return ErrBackpressured
	}
	s.paused = false
	return nil
}
