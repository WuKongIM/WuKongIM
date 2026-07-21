package workload

import (
	"context"
	"time"
)

func runScheduledMessages(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, send func(context.Context, int) error) error {
	return runScheduledMessagesByKey(ctx, totalMessages, interval, maxConcurrency, nil, send)
}

// runSequentialMessagesWithinWindow preserves the zero/one-concurrency path
// while refusing to start another operation after the measured window closes.
// The operation already in flight is allowed to settle as the single phase
// tail covered by the coordinator timeout budget.
func runSequentialMessagesWithinWindow(ctx context.Context, duration time.Duration, totalMessages int, interval time.Duration, sleep func(context.Context, time.Duration) error, send func(context.Context, int) error) error {
	if totalMessages <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stopAt := time.Now().Add(duration)
	for offset := 0; offset < totalMessages; offset++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if offset > 0 && duration > 0 && !time.Now().Before(stopAt) {
			return nil
		}
		if err := send(ctx, offset); err != nil {
			return err
		}
		if duration > 0 && !time.Now().Before(stopAt) {
			return nil
		}
		if interval <= 0 || sleep == nil {
			continue
		}
		wait := interval
		if duration > 0 {
			remaining := time.Until(stopAt)
			if remaining <= 0 {
				return nil
			}
			if remaining < wait {
				wait = remaining
			}
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
	return nil
}

// runScheduledMessagesByKey bounds global in-flight sends and serializes sends
// that share a key, which keeps one simulated client from pipelining sendacks.
func runScheduledMessagesByKey(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, keyForOffset func(int) string, send func(context.Context, int) error) error {
	return runScheduledMessagesByKeyWithStats(ctx, totalMessages, interval, maxConcurrency, keyForOffset, send, nil)
}

func runScheduledMessagesByKeyWithStats(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, keyForOffset func(int) string, send func(context.Context, int) error, stats *scheduledMessageStats) error {
	return runScheduledMessagesByKeyLimitWithStats(ctx, totalMessages, interval, maxConcurrency, 1, keyForOffset, send, stats)
}

func runScheduledMessagesByKeyLimitWithStats(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, maxInFlightPerKey int, keyForOffset func(int) string, send func(context.Context, int) error, stats *scheduledMessageStats) error {
	if totalMessages <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if maxConcurrency > totalMessages {
		maxConcurrency = totalMessages
	}
	if maxInFlightPerKey <= 0 {
		maxInFlightPerKey = 1
	}

	startAt := time.Now()
	var stopAt time.Time
	if interval > 0 {
		stopAt = startAt.Add(interval * time.Duration(totalMessages))
	}
	if stats != nil {
		stats.Planned += uint64(totalMessages)
	}
	scheduler := scheduledMessageScheduler{
		totalMessages:  totalMessages,
		maxConcurrency: maxConcurrency,
		keyLimit:       maxInFlightPerKey,
		keyForOffset:   keyForOffset,
		send:           send,
		startAt:        startAt,
		stopAt:         stopAt,
		interval:       interval,
		stats:          stats,
	}
	return scheduler.run(ctx)
}

type scheduledMessageStats struct {
	Planned                       uint64
	Enqueued                      uint64
	Dispatched                    uint64
	DroppedPendingWindowExpired   uint64
	DroppedUnstartedWindowExpired uint64
	BusyKeyStalls                 uint64
	MaxPendingDepth               int
	MaxActive                     int
	MaxBusyKeys                   int
}

type scheduledMessageTask struct {
	offset int
	key    string
}

type scheduledMessageTaskQueue struct {
	tasks []scheduledMessageTask
	head  int
}

func (q *scheduledMessageTaskQueue) len() int {
	if q == nil {
		return 0
	}
	return len(q.tasks) - q.head
}

func (q *scheduledMessageTaskQueue) push(task scheduledMessageTask) {
	q.tasks = append(q.tasks, task)
}

func (q *scheduledMessageTaskQueue) pop() (scheduledMessageTask, bool) {
	if q.len() == 0 {
		return scheduledMessageTask{}, false
	}
	task := q.tasks[q.head]
	q.head++
	if q.head > 1024 && q.head*2 >= len(q.tasks) {
		copy(q.tasks, q.tasks[q.head:])
		q.tasks = q.tasks[:len(q.tasks)-q.head]
		q.head = 0
	}
	return task, true
}

func (q *scheduledMessageTaskQueue) clear() {
	if q == nil {
		return
	}
	q.tasks = q.tasks[:0]
	q.head = 0
}

type scheduledMessageResult struct {
	key string
	err error
}

type scheduledMessageScheduler struct {
	totalMessages  int
	maxConcurrency int
	keyForOffset   func(int) string
	send           func(context.Context, int) error
	startAt        time.Time
	stopAt         time.Time
	interval       time.Duration

	nextOffset     int
	active         int
	keyLimit       int
	pendingNoKey   scheduledMessageTaskQueue
	pendingByKey   map[string]*scheduledMessageTaskQueue
	readyKeys      []string
	readyKeyHead   int
	readyKeyQueued map[string]bool
	pendingCount   int
	busyKeys       map[string]int
	stats          *scheduledMessageStats

	windowClosed bool
}

func (s *scheduledMessageScheduler) run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.pendingNoKey = scheduledMessageTaskQueue{tasks: make([]scheduledMessageTask, 0, s.maxConcurrency)}
	s.pendingByKey = make(map[string]*scheduledMessageTaskQueue)
	s.readyKeys = make([]string, 0, s.maxConcurrency)
	s.readyKeyHead = 0
	s.readyKeyQueued = make(map[string]bool)
	s.busyKeys = make(map[string]int)
	doneCh := make(chan scheduledMessageResult, s.maxConcurrency)

	var firstErr error
	recordResult := func(result scheduledMessageResult) {
		s.active--
		if result.key != "" {
			s.releaseKey(result.key)
			s.markKeyReady(result.key)
		}
		if result.err != nil && firstErr == nil {
			firstErr = result.err
			cancel()
		}
	}
	waitActive := func() {
		for s.active > 0 {
			recordResult(<-doneCh)
		}
	}

	for {
		for {
			select {
			case result := <-doneCh:
				recordResult(result)
			default:
				goto drained
			}
		}

	drained:
		if firstErr != nil {
			waitActive()
			return firstErr
		}

		now := time.Now()
		if s.windowExpired(now) {
			s.closeWindow()
		} else {
			s.enqueueDue(now)
		}
		s.dispatch(runCtx, doneCh)
		s.observeStats()

		if s.done(now) {
			return ctx.Err()
		}

		timer, timerC := s.nextTimer(now)
		select {
		case result := <-doneCh:
			stopTimer(timer)
			recordResult(result)
		case <-timerC:
		case <-runCtx.Done():
			stopTimer(timer)
			waitActive()
			if firstErr != nil {
				return firstErr
			}
			return runCtx.Err()
		}
		stopTimer(timer)
	}
}

func (s *scheduledMessageScheduler) enqueueDue(now time.Time) {
	for s.nextOffset < s.totalMessages {
		if s.interval > 0 {
			dueAt := s.startAt.Add(s.interval * time.Duration(s.nextOffset))
			if now.Before(dueAt) {
				return
			}
		}
		s.enqueueTask(scheduledMessageTask{
			offset: s.nextOffset,
			key:    scheduledMessageKey(s.keyForOffset, s.nextOffset),
		})
		if s.stats != nil {
			s.stats.Enqueued++
		}
		s.nextOffset++
		s.observeStats()
	}
}

func (s *scheduledMessageScheduler) dispatch(ctx context.Context, doneCh chan<- scheduledMessageResult) {
	if s.windowExpired(time.Now()) {
		return
	}
	for s.active < s.maxConcurrency {
		task, ok := s.nextDispatchableTask()
		if !ok {
			if s.stats != nil && s.pendingCount > 0 {
				s.stats.BusyKeyStalls++
			}
			return
		}
		s.active++
		if task.key != "" {
			s.busyKeys[task.key]++
		}
		if s.stats != nil {
			s.stats.Dispatched++
		}
		s.observeStats()
		if task.key != "" {
			s.markKeyReady(task.key)
		}
		go func() {
			doneCh <- scheduledMessageResult{key: task.key, err: s.send(ctx, task.offset)}
		}()
	}
}

func (s *scheduledMessageScheduler) done(now time.Time) bool {
	if s.active > 0 || s.pendingCount > 0 {
		return false
	}
	if s.nextOffset >= s.totalMessages {
		return true
	}
	return s.windowExpired(now)
}

func (s *scheduledMessageScheduler) nextTimer(now time.Time) (*time.Timer, <-chan time.Time) {
	var next time.Time
	if s.nextOffset < s.totalMessages && !s.windowExpired(now) {
		if s.interval > 0 {
			next = s.startAt.Add(s.interval * time.Duration(s.nextOffset))
		} else {
			next = now
		}
	}
	if !s.stopAt.IsZero() && (next.IsZero() || s.stopAt.Before(next)) {
		next = s.stopAt
	}
	if next.IsZero() {
		return nil, nil
	}
	wait := time.Until(next)
	if wait <= 0 {
		wait = time.Nanosecond
	}
	timer := time.NewTimer(wait)
	return timer, timer.C
}

func (s *scheduledMessageScheduler) windowExpired(now time.Time) bool {
	return !s.stopAt.IsZero() && !now.Before(s.stopAt)
}

func (s *scheduledMessageScheduler) closeWindow() {
	if s.windowClosed {
		return
	}
	s.windowClosed = true
	if s.stats != nil {
		s.stats.DroppedPendingWindowExpired += uint64(s.pendingCount)
		if s.nextOffset < s.totalMessages {
			s.stats.DroppedUnstartedWindowExpired += uint64(s.totalMessages - s.nextOffset)
		}
	}
	s.pendingNoKey.clear()
	clear(s.pendingByKey)
	s.readyKeys = s.readyKeys[:0]
	s.readyKeyHead = 0
	clear(s.readyKeyQueued)
	s.pendingCount = 0
	s.nextOffset = s.totalMessages
}

func (s *scheduledMessageScheduler) observeStats() {
	if s.stats == nil {
		return
	}
	if s.pendingCount > s.stats.MaxPendingDepth {
		s.stats.MaxPendingDepth = s.pendingCount
	}
	if s.active > s.stats.MaxActive {
		s.stats.MaxActive = s.active
	}
	if len(s.busyKeys) > s.stats.MaxBusyKeys {
		s.stats.MaxBusyKeys = len(s.busyKeys)
	}
}

func (s *scheduledMessageScheduler) enqueueTask(task scheduledMessageTask) {
	s.pendingCount++
	if task.key == "" {
		s.pendingNoKey.push(task)
		return
	}
	queue := s.pendingByKey[task.key]
	if queue == nil {
		queue = &scheduledMessageTaskQueue{}
		s.pendingByKey[task.key] = queue
	}
	wasEmpty := queue.len() == 0
	queue.push(task)
	if wasEmpty {
		s.markKeyReady(task.key)
	}
}

func (s *scheduledMessageScheduler) markKeyReady(key string) {
	queue := s.pendingByKey[key]
	if key == "" || s.keyAtLimit(key) || s.readyKeyQueued[key] || queue == nil || queue.len() == 0 {
		return
	}
	s.readyKeys = append(s.readyKeys, key)
	s.readyKeyQueued[key] = true
}

func (s *scheduledMessageScheduler) nextDispatchableTask() (scheduledMessageTask, bool) {
	if task, ok := s.pendingNoKey.pop(); ok {
		s.pendingCount--
		return task, true
	}
	for s.readyKeyHead < len(s.readyKeys) {
		key := s.readyKeys[s.readyKeyHead]
		s.readyKeyHead++
		s.compactReadyKeys()
		s.readyKeyQueued[key] = false
		if s.keyAtLimit(key) {
			continue
		}
		queue := s.pendingByKey[key]
		if queue == nil || queue.len() == 0 {
			delete(s.pendingByKey, key)
			continue
		}
		task, _ := queue.pop()
		if queue.len() == 0 {
			delete(s.pendingByKey, key)
		}
		s.pendingCount--
		return task, true
	}
	return scheduledMessageTask{}, false
}

func (s *scheduledMessageScheduler) keyAtLimit(key string) bool {
	if key == "" {
		return false
	}
	limit := s.keyLimit
	if limit <= 0 {
		limit = 1
	}
	return s.busyKeys[key] >= limit
}

func (s *scheduledMessageScheduler) releaseKey(key string) {
	if key == "" {
		return
	}
	count := s.busyKeys[key]
	if count <= 1 {
		delete(s.busyKeys, key)
		return
	}
	s.busyKeys[key] = count - 1
}

func (s *scheduledMessageScheduler) compactReadyKeys() {
	if s.readyKeyHead <= 1024 || s.readyKeyHead*2 < len(s.readyKeys) {
		return
	}
	copy(s.readyKeys, s.readyKeys[s.readyKeyHead:])
	s.readyKeys = s.readyKeys[:len(s.readyKeys)-s.readyKeyHead]
	s.readyKeyHead = 0
}

func scheduledMessageKey(keyForOffset func(int) string, offset int) string {
	if keyForOffset == nil {
		return ""
	}
	return keyForOffset(offset)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
