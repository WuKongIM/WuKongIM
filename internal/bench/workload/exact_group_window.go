package workload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrExactGroupWindowInfeasible identifies a measured group window that
// reached its admission deadline before every planned message was admitted.
// GroupWorkload converts this typed result into the existing scheduler drop
// evidence so capacity shortfall remains a rate verdict rather than a worker
// hook failure.
var ErrExactGroupWindowInfeasible = errors.New("exact group window infeasible")

// ExactGroupWindowInfeasibleError carries bounded accounting for an exact
// group window that could not admit its complete plan before stopAt.
type ExactGroupWindowInfeasibleError struct {
	Planned    uint64
	Admitted   uint64
	Unadmitted uint64
}

func (e *ExactGroupWindowInfeasibleError) Error() string {
	if e == nil {
		return ErrExactGroupWindowInfeasible.Error()
	}
	return fmt.Sprintf("%s: planned=%d admitted=%d unadmitted=%d", ErrExactGroupWindowInfeasible, e.Planned, e.Admitted, e.Unadmitted)
}

func (e *ExactGroupWindowInfeasibleError) Unwrap() error {
	return ErrExactGroupWindowInfeasible
}

const (
	assignmentSenderCreditShardCount = 256
	exactGroupMatchBatchLimit        = 64
)

// AssignmentSenderCredits is the assignment-scoped sender-credit module used
// by high-concurrency round-robin group windows. The exported interface is
// intentionally only construction plus GroupConfig injection; acquisition,
// release, and wakeup ordering stay private to the exact-window scheduler.
type AssignmentSenderCredits struct {
	shards [assignmentSenderCreditShardCount]assignmentSenderCreditShard
	busy   atomic.Int64

	changeMu sync.Mutex
	changed  chan struct{}
}

type assignmentSenderCreditShard struct {
	mu      sync.Mutex
	senders map[string]struct{}
}

// NewAssignmentSenderCredits creates one credit domain to share across every
// round-robin GroupWorkload built for an assignment generation.
func NewAssignmentSenderCredits() *AssignmentSenderCredits {
	return &AssignmentSenderCredits{changed: make(chan struct{})}
}

func (c *AssignmentSenderCredits) available(senderUID string) bool {
	if c == nil || senderUID == "" {
		return false
	}
	shard := &c.shards[assignmentSenderCreditShardIndex(senderUID)]
	shard.mu.Lock()
	_, busy := shard.senders[senderUID]
	shard.mu.Unlock()
	return !busy
}

func (c *AssignmentSenderCredits) tryAcquire(senderUID string) bool {
	if c == nil || senderUID == "" {
		return false
	}
	shard := &c.shards[assignmentSenderCreditShardIndex(senderUID)]
	shard.mu.Lock()
	if _, busy := shard.senders[senderUID]; busy {
		shard.mu.Unlock()
		return false
	}
	if shard.senders == nil {
		shard.senders = make(map[string]struct{})
	}
	shard.senders[senderUID] = struct{}{}
	shard.mu.Unlock()
	c.busy.Add(1)
	return true
}

func (c *AssignmentSenderCredits) release(senderUID string) bool {
	if c == nil || senderUID == "" {
		return false
	}
	shard := &c.shards[assignmentSenderCreditShardIndex(senderUID)]
	shard.mu.Lock()
	if _, busy := shard.senders[senderUID]; !busy {
		shard.mu.Unlock()
		return false
	}
	delete(shard.senders, senderUID)
	shard.mu.Unlock()
	c.busy.Add(-1)
	c.notifyRelease()
	return true
}

func (c *AssignmentSenderCredits) busyCount() int {
	if c == nil {
		return 0
	}
	return int(c.busy.Load())
}

func (c *AssignmentSenderCredits) watchReleases() <-chan struct{} {
	if c == nil {
		return nil
	}
	c.changeMu.Lock()
	if c.changed == nil {
		c.changed = make(chan struct{})
	}
	changed := c.changed
	c.changeMu.Unlock()
	return changed
}

func (c *AssignmentSenderCredits) notifyRelease() {
	c.changeMu.Lock()
	if c.changed == nil {
		c.changed = make(chan struct{})
	} else {
		close(c.changed)
		c.changed = make(chan struct{})
	}
	c.changeMu.Unlock()
}

func assignmentSenderCreditShardIndex(senderUID string) uint8 {
	var hash uint32 = 2166136261
	for index := 0; index < len(senderUID); index++ {
		hash ^= uint32(senderUID[index])
		hash *= 16777619
	}
	return uint8(hash)
}

type exactGroupWindowIntent struct {
	senders   []string
	preferred int
}

type exactGroupWindowConfig struct {
	totalMessages  int
	streamCount    int
	interval       time.Duration
	duration       time.Duration
	maxConcurrency int
	stopAt         time.Time
	clock          exactGroupWindowClock
	credits        *AssignmentSenderCredits
	intent         func(int) exactGroupWindowIntent
	send           func(context.Context, int, string) error
	stats          *scheduledMessageStats
}

type exactGroupWindowTask struct {
	stream int
	offset int
}

type exactGroupWindowResult struct {
	senderUID string
	err       error
}

// exactGroupWindow owns one paced stream set. dueCount is the arithmetic
// watermark of logically enqueued messages; ledger compresses all unadmitted
// messages into O(streamCount) state; active counts operations holding shared
// sender credits. closed permanently forbids new admission after stopAt.
type exactGroupWindow struct {
	cfg     exactGroupWindowConfig
	startAt time.Time
	stopAt  time.Time

	dueCount int
	active   int
	ledger   *exactGroupDueLedger
	closed   bool

	droppedPending   uint64
	droppedUnstarted uint64
	firstErr         error
	doneCh           chan exactGroupWindowResult
	runCtx           context.Context
	cancelRun        context.CancelFunc

	frontier     []exactGroupWindowTask
	taskToSender []string
	senderToTask map[string]int
	seenSenders  map[string]uint64
	seenEpoch    uint64
}

func runExactGroupWindow(ctx context.Context, cfg exactGroupWindowConfig) error {
	if cfg.totalMessages <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.intent == nil {
		return fmt.Errorf("exact group window: intent source is required")
	}
	if cfg.send == nil {
		return fmt.Errorf("exact group window: send function is required")
	}
	if cfg.maxConcurrency <= 0 {
		cfg.maxConcurrency = 1
	}
	if cfg.maxConcurrency > cfg.totalMessages {
		cfg.maxConcurrency = cfg.totalMessages
	}
	if cfg.streamCount <= 0 {
		cfg.streamCount = 1
	}
	if cfg.streamCount > cfg.totalMessages {
		cfg.streamCount = cfg.totalMessages
	}
	if cfg.clock == nil {
		cfg.clock = realExactGroupWindowClock{}
	}
	if cfg.credits == nil {
		cfg.credits = NewAssignmentSenderCredits()
	}
	if cfg.stats != nil {
		cfg.stats.Planned += uint64(cfg.totalMessages)
	}

	capacity := minInt(exactGroupMatchBatchLimit, cfg.streamCount)
	window := &exactGroupWindow{
		cfg:          cfg,
		ledger:       newExactGroupDueLedger(cfg.streamCount),
		frontier:     make([]exactGroupWindowTask, 0, capacity),
		taskToSender: make([]string, capacity),
		senderToTask: make(map[string]int, minInt(exactGroupMatchBatchLimit, cfg.maxConcurrency)),
		seenSenders:  make(map[string]uint64, minInt(exactGroupMatchBatchLimit, cfg.maxConcurrency)),
	}
	window.startAt = cfg.clock.Now()
	window.stopAt = cfg.stopAt
	if window.stopAt.IsZero() && cfg.duration > 0 {
		window.stopAt = window.startAt.Add(cfg.duration)
	}
	window.runCtx, window.cancelRun = context.WithCancel(ctx)
	defer window.cancelRun()
	window.doneCh = make(chan exactGroupWindowResult, cfg.maxConcurrency)
	return window.run(ctx)
}

func (w *exactGroupWindow) run(parent context.Context) error {
	for {
		w.drainResults()
		if w.firstErr != nil {
			w.waitActive()
			return w.firstErr
		}
		if err := parent.Err(); err != nil {
			w.cancelRun()
			w.waitActive()
			if w.firstErr != nil {
				return w.firstErr
			}
			return err
		}

		creditReleased := w.cfg.credits.watchReleases()
		now := w.cfg.clock.Now()
		if w.windowExpired(now) {
			w.closeWindow()
		} else {
			w.enqueueDue(now)
			w.dispatch()
		}
		w.observeStats()

		if w.done() {
			if err := parent.Err(); err != nil {
				return err
			}
			if unadmitted := w.unadmitted(); unadmitted > 0 {
				return &ExactGroupWindowInfeasibleError{
					Planned:    uint64(w.cfg.totalMessages),
					Admitted:   uint64(w.cfg.totalMessages) - unadmitted,
					Unadmitted: unadmitted,
				}
			}
			return nil
		}

		timer := w.nextTimer(now)
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C()
		}
		select {
		case result := <-w.doneCh:
			stopExactGroupWindowTimer(timer)
			w.recordResult(result)
		case <-timerC:
		case <-creditReleased:
		case <-w.runCtx.Done():
			stopExactGroupWindowTimer(timer)
			w.waitActive()
			if w.firstErr != nil {
				return w.firstErr
			}
			return w.runCtx.Err()
		}
		stopExactGroupWindowTimer(timer)
	}
}

func (w *exactGroupWindow) drainResults() {
	for {
		select {
		case result := <-w.doneCh:
			w.recordResult(result)
		default:
			return
		}
	}
}

func (w *exactGroupWindow) recordResult(result exactGroupWindowResult) {
	w.active--
	if !w.cfg.credits.release(result.senderUID) && w.firstErr == nil {
		w.firstErr = fmt.Errorf("exact group window: sender credit release mismatch")
		w.cancelRun()
	}
	if result.err != nil && w.firstErr == nil {
		w.firstErr = result.err
		w.cancelRun()
	}
}

func (w *exactGroupWindow) waitActive() {
	for w.active > 0 {
		w.recordResult(<-w.doneCh)
	}
}

func (w *exactGroupWindow) enqueueDue(now time.Time) {
	target := w.dueExclusive(now)
	if target <= w.dueCount {
		return
	}
	w.ledger.enqueueRange(w.dueCount, target)
	if w.cfg.stats != nil {
		w.cfg.stats.Enqueued += uint64(target - w.dueCount)
	}
	w.dueCount = target
	w.observeStats()
}

func (w *exactGroupWindow) dueExclusive(now time.Time) int {
	if w.cfg.interval <= 0 {
		return w.cfg.totalMessages
	}
	if now.Before(w.startAt) {
		return 0
	}
	due := int(now.Sub(w.startAt)/w.cfg.interval) + 1
	if due > w.cfg.totalMessages {
		return w.cfg.totalMessages
	}
	return due
}

func (w *exactGroupWindow) dispatch() {
	for w.active < w.cfg.maxConcurrency && w.ledger.pendingCount > 0 {
		if w.runCtx.Err() != nil {
			return
		}
		if w.windowExpired(w.cfg.clock.Now()) {
			w.closeWindow()
			return
		}
		before := w.active
		w.dispatchBatch()
		if w.closed || w.active == before {
			if !w.closed && w.cfg.stats != nil {
				w.cfg.stats.BusyKeyStalls++
			}
			return
		}
	}
}

func (w *exactGroupWindow) dispatchBatch() {
	limit := minInt(exactGroupMatchBatchLimit, w.cfg.maxConcurrency-w.active)
	limit = minInt(limit, w.ledger.ready.len())
	if limit <= 0 {
		return
	}
	w.frontier = w.frontier[:0]
	for len(w.frontier) < limit {
		stream, ok := w.ledger.ready.pop()
		if !ok {
			break
		}
		w.frontier = append(w.frontier, exactGroupWindowTask{stream: stream, offset: w.ledger.headOffset(stream)})
	}
	w.matchFrontier()

	for taskIndex, task := range w.frontier {
		if w.runCtx.Err() != nil {
			w.requeueFrontier(taskIndex)
			return
		}
		if w.windowExpired(w.cfg.clock.Now()) {
			w.closeWindow()
			return
		}
		senderUID, acquired := w.acquireFrontierSender(taskIndex, task)
		if !acquired {
			w.ledger.ready.push(task.stream)
			continue
		}
		if w.runCtx.Err() != nil {
			w.cfg.credits.release(senderUID)
			w.requeueFrontier(taskIndex)
			return
		}
		if w.windowExpired(w.cfg.clock.Now()) {
			w.cfg.credits.release(senderUID)
			w.closeWindow()
			return
		}
		w.ledger.admit(task.stream)
		if w.ledger.pendingByStream[task.stream] > 0 {
			w.ledger.ready.push(task.stream)
		}
		w.startTask(task, senderUID)
	}
}

func (w *exactGroupWindow) acquireFrontierSender(taskIndex int, task exactGroupWindowTask) (string, bool) {
	matched := w.taskToSender[taskIndex]
	if matched != "" && w.cfg.credits.tryAcquire(matched) {
		return matched, true
	}
	intent := w.cfg.intent(task.offset)
	for probe := 0; probe < len(intent.senders); probe++ {
		candidateIndex := (intent.preferred + probe) % len(intent.senders)
		if candidateIndex < 0 {
			candidateIndex += len(intent.senders)
		}
		senderUID := intent.senders[candidateIndex]
		if senderUID == "" || senderUID == matched || w.senderReservedForLaterTask(senderUID, taskIndex) {
			continue
		}
		if w.cfg.credits.tryAcquire(senderUID) {
			return senderUID, true
		}
	}
	return "", false
}

func (w *exactGroupWindow) senderReservedForLaterTask(senderUID string, taskIndex int) bool {
	for index := taskIndex + 1; index < len(w.frontier); index++ {
		if w.taskToSender[index] == senderUID {
			return true
		}
	}
	return false
}

func (w *exactGroupWindow) requeueFrontier(start int) {
	for index := start; index < len(w.frontier); index++ {
		w.ledger.ready.push(w.frontier[index].stream)
	}
}

func (w *exactGroupWindow) matchFrontier() {
	for index := range w.taskToSender {
		w.taskToSender[index] = ""
	}
	clear(w.senderToTask)
	if len(w.frontier) == 0 {
		return
	}
	if len(w.frontier) == 1 {
		if senderUID, ok := w.firstAvailableSender(w.cfg.intent(w.frontier[0].offset)); ok {
			w.taskToSender[0] = senderUID
		}
		return
	}
	for taskIndex := range w.frontier {
		w.seenEpoch++
		if w.seenEpoch == 0 {
			clear(w.seenSenders)
			w.seenEpoch = 1
		}
		w.augmentMatch(taskIndex)
	}
}

func (w *exactGroupWindow) firstAvailableSender(intent exactGroupWindowIntent) (string, bool) {
	for probe := 0; probe < len(intent.senders); probe++ {
		candidateIndex := (intent.preferred + probe) % len(intent.senders)
		if candidateIndex < 0 {
			candidateIndex += len(intent.senders)
		}
		senderUID := intent.senders[candidateIndex]
		if senderUID != "" && w.cfg.credits.available(senderUID) {
			return senderUID, true
		}
	}
	return "", false
}

func (w *exactGroupWindow) augmentMatch(taskIndex int) bool {
	intent := w.cfg.intent(w.frontier[taskIndex].offset)
	for probe := 0; probe < len(intent.senders); probe++ {
		candidateIndex := (intent.preferred + probe) % len(intent.senders)
		if candidateIndex < 0 {
			candidateIndex += len(intent.senders)
		}
		senderUID := intent.senders[candidateIndex]
		if senderUID == "" || !w.cfg.credits.available(senderUID) || w.seenSenders[senderUID] == w.seenEpoch {
			continue
		}
		w.seenSenders[senderUID] = w.seenEpoch
		otherTask, occupied := w.senderToTask[senderUID]
		if occupied && !w.augmentMatch(otherTask) {
			continue
		}
		w.senderToTask[senderUID] = taskIndex
		w.taskToSender[taskIndex] = senderUID
		return true
	}
	return false
}

func (w *exactGroupWindow) startTask(task exactGroupWindowTask, senderUID string) {
	w.active++
	if w.cfg.stats != nil {
		w.cfg.stats.Dispatched++
	}
	w.observeStats()
	go func(offset int, sender string) {
		w.doneCh <- exactGroupWindowResult{senderUID: sender, err: w.cfg.send(w.runCtx, offset, sender)}
	}(task.offset, senderUID)
}

func (w *exactGroupWindow) windowExpired(now time.Time) bool {
	return !w.stopAt.IsZero() && !now.Before(w.stopAt)
}

func (w *exactGroupWindow) closeWindow() {
	if w.closed {
		return
	}
	w.closed = true
	w.droppedPending = uint64(w.ledger.pendingCount)
	if w.dueCount < w.cfg.totalMessages {
		w.droppedUnstarted = uint64(w.cfg.totalMessages - w.dueCount)
	}
	if w.cfg.stats != nil {
		w.cfg.stats.DroppedPendingWindowExpired += w.droppedPending
		w.cfg.stats.DroppedUnstartedWindowExpired += w.droppedUnstarted
	}
	w.ledger.clear()
	w.dueCount = w.cfg.totalMessages
}

func (w *exactGroupWindow) done() bool {
	return w.active == 0 && w.ledger.pendingCount == 0 && w.dueCount >= w.cfg.totalMessages
}

func (w *exactGroupWindow) unadmitted() uint64 {
	return w.droppedPending + w.droppedUnstarted
}

func (w *exactGroupWindow) nextTimer(now time.Time) exactGroupWindowTimer {
	if w.closed {
		return nil
	}
	var next time.Time
	if w.dueCount < w.cfg.totalMessages {
		if w.cfg.interval > 0 {
			next = w.startAt.Add(w.cfg.interval * time.Duration(w.dueCount))
		} else {
			next = now
		}
	}
	if !w.stopAt.IsZero() && (next.IsZero() || w.stopAt.Before(next)) {
		next = w.stopAt
	}
	if next.IsZero() {
		return nil
	}
	wait := next.Sub(now)
	if wait <= 0 {
		wait = time.Nanosecond
	}
	return w.cfg.clock.NewTimer(wait)
}

func (w *exactGroupWindow) observeStats() {
	if w.cfg.stats == nil {
		return
	}
	if w.ledger.pendingCount > w.cfg.stats.MaxPendingDepth {
		w.cfg.stats.MaxPendingDepth = w.ledger.pendingCount
	}
	if w.active > w.cfg.stats.MaxActive {
		w.cfg.stats.MaxActive = w.active
	}
	if w.active > w.cfg.stats.MaxBusyKeys {
		w.cfg.stats.MaxBusyKeys = w.active
	}
}

// exactGroupDueLedger stores one pending count and admitted ordinal per
// channel stream. Retained state is O(streamCount), independent of the number
// of due messages.
type exactGroupDueLedger struct {
	streamCount      int
	pendingCount     int
	pendingByStream  []int
	admittedByStream []int
	ready            exactGroupReadyQueue
}

func newExactGroupDueLedger(streamCount int) *exactGroupDueLedger {
	if streamCount <= 0 {
		streamCount = 1
	}
	return &exactGroupDueLedger{
		streamCount:      streamCount,
		pendingByStream:  make([]int, streamCount),
		admittedByStream: make([]int, streamCount),
		ready:            newExactGroupReadyQueue(streamCount),
	}
}

func (l *exactGroupDueLedger) enqueueRange(start, end int) {
	if l == nil || end <= start {
		return
	}
	delta := end - start
	full := delta / l.streamCount
	remainder := delta % l.streamCount
	visited := l.streamCount
	if full == 0 {
		visited = remainder
	}
	for step := 0; step < visited; step++ {
		count := full
		if step < remainder {
			count++
		}
		l.addPending((start+step)%l.streamCount, count)
	}
	l.pendingCount += delta
}

func (l *exactGroupDueLedger) addPending(stream, count int) {
	if count <= 0 {
		return
	}
	wasEmpty := l.pendingByStream[stream] == 0
	l.pendingByStream[stream] += count
	if wasEmpty {
		l.ready.push(stream)
	}
}

func (l *exactGroupDueLedger) headOffset(stream int) int {
	return stream + l.admittedByStream[stream]*l.streamCount
}

func (l *exactGroupDueLedger) admit(stream int) {
	l.pendingByStream[stream]--
	l.pendingCount--
	l.admittedByStream[stream]++
}

func (l *exactGroupDueLedger) clear() {
	for stream := range l.pendingByStream {
		l.pendingByStream[stream] = 0
	}
	l.pendingCount = 0
	l.ready.clear()
}

type exactGroupReadyQueue struct {
	values []int
	queued []bool
	head   int
	count  int
}

func newExactGroupReadyQueue(streamCount int) exactGroupReadyQueue {
	return exactGroupReadyQueue{values: make([]int, streamCount), queued: make([]bool, streamCount)}
}

func (q *exactGroupReadyQueue) push(stream int) {
	if q == nil || stream < 0 || stream >= len(q.values) || q.queued[stream] {
		return
	}
	position := (q.head + q.count) % len(q.values)
	q.values[position] = stream
	q.queued[stream] = true
	q.count++
}

func (q *exactGroupReadyQueue) pop() (int, bool) {
	if q == nil || q.count == 0 {
		return 0, false
	}
	stream := q.values[q.head]
	q.head = (q.head + 1) % len(q.values)
	q.count--
	q.queued[stream] = false
	return stream, true
}

func (q *exactGroupReadyQueue) len() int {
	if q == nil {
		return 0
	}
	return q.count
}

func (q *exactGroupReadyQueue) clear() {
	if q == nil {
		return
	}
	clear(q.queued)
	q.head = 0
	q.count = 0
}

type exactGroupWindowClock interface {
	Now() time.Time
	NewTimer(time.Duration) exactGroupWindowTimer
}

type exactGroupWindowTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type realExactGroupWindowClock struct{}

func (realExactGroupWindowClock) Now() time.Time { return time.Now() }

func (realExactGroupWindowClock) NewTimer(wait time.Duration) exactGroupWindowTimer {
	return realExactGroupWindowTimer{timer: time.NewTimer(wait)}
}

type realExactGroupWindowTimer struct {
	timer *time.Timer
}

func (t realExactGroupWindowTimer) C() <-chan time.Time { return t.timer.C }
func (t realExactGroupWindowTimer) Stop() bool          { return t.timer.Stop() }

func stopExactGroupWindowTimer(timer exactGroupWindowTimer) {
	if timer != nil {
		timer.Stop()
	}
}
