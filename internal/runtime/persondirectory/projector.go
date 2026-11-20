package persondirectory

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	projectorWorkers        = 8
	projectorReadyWorkers   = 2
	projectorPageSize       = 64
	projectorQueueSize      = projectorWorkers * projectorPageSize
	projectorRepairInterval = time.Second
	projectorAttemptTimeout = 4 * time.Second
)

var (
	errSourceRequired       = errors.New("persondirectory: task source is required")
	errMembershipsRequired  = errors.New("persondirectory: membership writer is required")
	errSourceLeadershipLost = errors.New("persondirectory: source hash slot is not locally led")
)

// TaskSource exposes only current-leader task discovery and authoritative
// completion. Implementations must omit hash slots not led by this node.
type TaskSource interface {
	LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error)
	IsLocalLeaderHashSlot(context.Context, metadb.HashSlot) (bool, error)
	ListPersonDirectoryTaskPage(context.Context, metadb.HashSlot, metadb.PersonDirectoryTaskCursor, int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error)
	ValidatePersonDirectoryTasks(context.Context, []metadb.PersonDirectoryTaskLocation) []error
	CompletePersonDirectoryTasks(context.Context, []metadb.PersonDirectoryTaskLocation) []error
}

// MembershipResult is aligned with one requested create-if-absent membership.
type MembershipResult struct {
	Err error
}

// MembershipWriter projects UID-owned rows through their current Slot leaders.
// Results must have exactly the same length and order as memberships.
type MembershipWriter interface {
	EnsureUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) []MembershipResult
}

// PressureObservation is the bounded scheduler view used by runtime pressure
// metrics. Pending remains nonzero across retries until durable completion.
type PressureObservation struct {
	Pending  int
	Inflight int
	Capacity int
	Workers  int
}

// PressureObserver receives serialized absolute projector pressure snapshots.
type PressureObserver interface {
	ObservePersonDirectoryPressure(PressureObservation)
}

// membershipWaveWriter reports durable membership groups while the wave owns
// its proposal credits. Queued Ready calls keep the supplied context through
// their joined completion.
type membershipWaveWriter interface {
	EnsureUserChannelMembershipWaves(context.Context, []metadb.UserChannelMembership, func(context.Context, []int, error))
}

// Options configures one process-level person-directory projector.
type Options struct {
	Source      TaskSource
	Memberships MembershipWriter
	Goroutines  *goruntimeregistry.Registry
	Observer    PressureObserver
}

type projectorState uint8

const (
	projectorStopped projectorState = iota
	projectorRunning
)

type projectorRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	// direct carries bounded source-leader requests into the shared scheduler.
	direct chan preparationRequest
	tasks  chan ownedTaskBatch
	// takeMu protects only queue ownership, never projection or callbacks.
	// next holds one unfitting FIFO head, still charged to the 512-task bound.
	takeMu  sync.Mutex
	next    ownedTaskBatch
	results chan taskResult
	// One scanner owns discovery cursors. Its reserved page shares the existing
	// task capacity; it cannot block completion publication or direct admission.
	scans   chan scanRequest
	scanned chan scanResult
	done    chan struct{}
	workers sync.WaitGroup
}

type ownedTaskBatch struct {
	tasks []ownedTask
}

type ownedTask struct {
	// batchGroup preserves the original queued Prepare group while a worker
	// combines its membership writes with other groups.
	batchGroup int
	hashSlot   metadb.HashSlot
	task       metadb.PersonDirectoryTask
}

type ownedTaskKey struct {
	hashSlot    metadb.HashSlot
	channelID   string
	channelType int64
	generation  uint64
}

type taskResult struct {
	// partial publishes completed tasks while their worker still owns the batch.
	partial   bool
	keys      []ownedTaskKey
	completed []ownedTaskKey
	err       error
}

type projectorScanState struct {
	nextSlot int
	cursors  map[metadb.HashSlot]metadb.PersonDirectoryTaskCursor
}

type scanRequest struct {
	inflight map[ownedTaskKey]struct{}
	limit    int
}

type scanResult struct {
	tasks []ownedTask
	led   map[metadb.HashSlot]struct{}
}

// Projector asynchronously materializes both UID memberships for durable
// person-channel tasks. It owns one scanner and exactly eight fixed workers.
type Projector struct {
	source         TaskSource
	memberships    MembershipWriter
	goroutines     *goruntimeregistry.Registry
	observer       PressureObserver
	attemptTimeout time.Duration

	mu    sync.Mutex
	state projectorState
	run   *projectorRun
}

// New constructs a stopped projector.
func New(opts Options) (*Projector, error) {
	if opts.Source == nil {
		return nil, errSourceRequired
	}
	if opts.Memberships == nil {
		return nil, errMembershipsRequired
	}
	return &Projector{
		source: opts.Source, memberships: opts.Memberships, goroutines: opts.Goroutines,
		observer: opts.Observer, attemptTimeout: projectorAttemptTimeout,
	}, nil
}

// Start launches one repair scanner and eight fixed workers.
func (p *Projector) Start(ctx context.Context) error {
	if p == nil {
		return errSourceRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == projectorRunning {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run := &projectorRun{
		ctx: runCtx, cancel: cancel,
		direct: make(chan preparationRequest, projectorWorkers), wake: make(chan struct{}, 1), tasks: make(chan ownedTaskBatch, projectorWorkers),
		results: make(chan taskResult, projectorQueueSize), done: make(chan struct{}),
		scans: make(chan scanRequest, 1), scanned: make(chan scanResult, 1),
	}
	run.workers.Add(projectorWorkers + 1)
	goruntimeregistry.SafeGo(p.goroutines, goruntimeregistry.TaskMessageDirectoryScanner, func() {
		defer run.workers.Done()
		p.runScanner(run)
	})
	for range projectorWorkers {
		goruntimeregistry.SafeGo(p.goroutines, goruntimeregistry.TaskMessageDirectoryWorker, func() {
			defer run.workers.Done()
			p.runWorker(run)
		})
	}
	goruntimeregistry.SafeGo(p.goroutines, goruntimeregistry.TaskMessageDirectoryProjector, func() {
		p.runScheduler(run)
	})
	p.run = run
	p.state = projectorRunning
	return nil
}

// Wake requests an immediate scan without blocking the caller.
func (p *Projector) Wake() {
	if p == nil {
		return
	}
	p.mu.Lock()
	run := p.run
	running := p.state == projectorRunning
	p.mu.Unlock()
	if !running || run == nil {
		return
	}
	select {
	case run.wake <- struct{}{}:
	default:
	}
}

// Stop cancels the active generation and waits for all fixed workers.
func (p *Projector) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	run := p.run
	if p.state != projectorRunning || run == nil {
		p.mu.Unlock()
		return nil
	}
	run.cancel()
	p.mu.Unlock()

	select {
	case <-run.done:
		p.mu.Lock()
		if p.run == run {
			p.run = nil
			p.state = projectorStopped
		}
		p.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Projector) runScheduler(run *projectorRun) {
	defer func() {
		run.cancel()
		run.workers.Wait()
		close(run.done)
	}()
	ticker := time.NewTicker(projectorRepairInterval)
	defer ticker.Stop()
	waiters := make(map[ownedTaskKey][]preparationWaiter)
	waiterCount := 0
	inflight := make(map[ownedTaskKey]struct{}, projectorQueueSize)
	known := make(map[ownedTaskKey]struct{}, projectorQueueSize)
	scanWanted, scanning := true, false
	scanReserved := 0
	var pending ownedTaskBatch
	p.observePressure(len(known), len(inflight))
	defer p.observePressure(0, 0)
	for {
		if scanWanted && !scanning && len(inflight) < projectorQueueSize {
			snapshot := make(map[ownedTaskKey]struct{}, len(inflight))
			for key := range inflight {
				snapshot[key] = struct{}{}
			}
			scanReserved = min(projectorPageSize, projectorQueueSize-len(inflight))
			run.scans <- scanRequest{inflight: snapshot, limit: scanReserved}
			scanning, scanWanted = true, false
		}
		// Keep at most one admitted batch awaiting dispatch, charged to inflight.
		// Queue space wakes this select directly; results and Stop stay responsive.
		direct, scanned := run.direct, run.scanned
		var dispatch chan ownedTaskBatch
		if len(pending.tasks) > 0 {
			dispatch = run.tasks
			direct, scanned = nil, nil
		}
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
			scanWanted = true
			p.observePressure(len(known), len(inflight))
		case dispatch <- pending:
			pending = ownedTaskBatch{}
		case request := <-direct:
			pending = p.admitPreparation(request, inflight, known, waiters, &waiterCount, scanReserved)
			p.observePressure(len(known), len(inflight))
		case <-run.wake:
			scanWanted = true
			p.observePressure(len(known), len(inflight))
		case discovery := <-scanned:
			scanning = false
			scanReserved = 0
			if discovery.led != nil {
				for key := range known {
					if _, ok := discovery.led[key.hashSlot]; !ok {
						delete(known, key)
					}
				}
			}
			batch := ownedTaskBatch{tasks: discovery.tasks[:0]}
			for _, task := range discovery.tasks {
				key := ownedTaskKey{hashSlot: task.hashSlot, channelID: task.task.ChannelID, channelType: task.task.ChannelType, generation: task.task.Generation}
				if _, exists := inflight[key]; exists {
					continue
				}
				inflight[key] = struct{}{}
				rememberKnownTask(known, inflight, key)
				batch.tasks = append(batch.tasks, task)
			}
			if len(batch.tasks) > 0 {
				pending = batch
				scanWanted = true
			}
			p.observePressure(len(known), len(inflight))
		case result := <-run.results:
			completed := make(map[ownedTaskKey]struct{}, len(result.completed))
			for _, key := range result.completed {
				completed[key] = struct{}{}
			}
			for _, key := range result.keys {
				err := result.err
				if _, ok := completed[key]; ok {
					err = nil
				} else if err == nil {
					err = metadb.ErrStaleMeta
				}
				for _, waiter := range waiters[key] {
					waiter.results <- preparationResult{index: waiter.index, err: err}
					waiterCount--
				}
				delete(waiters, key)
			}
			if result.partial {
				continue
			}
			for _, key := range result.keys {
				delete(inflight, key)
			}
			for _, key := range result.completed {
				delete(known, key)
			}
			p.observePressure(len(known), len(inflight))
			// A failed projection remains durable and is retried by the bounded
			// repair cadence. Immediate retry here would hot-spin on a failed UID
			// Slot and could starve shutdown. Successful completion may expose the
			// next page immediately when tracked work remains. An empty queue
			// waits for the repair tick instead of scanning all hash slots
			// after every directly prepared first SEND.
			if result.err == nil && len(known) > 0 {
				scanWanted = true
			}
		}
	}
}

func (p *Projector) runScanner(run *projectorRun) {
	state := projectorScanState{cursors: make(map[metadb.HashSlot]metadb.PersonDirectoryTaskCursor)}
	for {
		select {
		case <-run.ctx.Done():
			return
		case request := <-run.scans:
			result := p.scan(run.ctx, request, &state)
			select {
			case run.scanned <- result:
			case <-run.ctx.Done():
				return
			}
		}
	}
}

func (p *Projector) scan(ctx context.Context, request scanRequest, state *projectorScanState) scanResult {
	hashSlots, err := p.source.LocalLeaderHashSlots(ctx)
	if err != nil {
		return scanResult{}
	}
	led := make(map[metadb.HashSlot]struct{}, len(hashSlots))
	for _, hashSlot := range hashSlots {
		led[hashSlot] = struct{}{}
	}
	for hashSlot := range state.cursors {
		if _, stillLed := led[hashSlot]; !stillLed {
			delete(state.cursors, hashSlot)
		}
	}
	if state.nextSlot >= len(hashSlots) {
		state.nextSlot = 0
	}
	remaining := request.limit
	result := scanResult{tasks: make([]ownedTask, 0, remaining), led: led}
	selected := make(map[ownedTaskKey]struct{}, remaining)
	noProgress := 0
	for remaining > 0 && noProgress < len(hashSlots) {
		hashSlot := hashSlots[state.nextSlot]
		state.nextSlot = (state.nextSlot + 1) % len(hashSlots)
		cursor := state.cursors[hashSlot]
		rows, next, done, listErr := p.source.ListPersonDirectoryTaskPage(ctx, hashSlot, cursor, min(projectorPageSize, remaining))
		if listErr != nil {
			noProgress++
			continue
		}
		if done {
			state.cursors[hashSlot] = metadb.PersonDirectoryTaskCursor{}
		} else if next != cursor {
			state.cursors[hashSlot] = next
		}
		added := 0
		for _, task := range rows {
			key := ownedTaskKey{hashSlot: hashSlot, channelID: task.ChannelID, channelType: task.ChannelType, generation: task.Generation}
			if _, exists := request.inflight[key]; exists {
				continue
			}
			if _, exists := selected[key]; exists {
				continue
			}
			selected[key] = struct{}{}
			result.tasks = append(result.tasks, ownedTask{hashSlot: hashSlot, task: task})
			remaining--
			added++
			if remaining == 0 {
				break
			}
		}
		if added == 0 || (!done && next == cursor) {
			noProgress++
		} else {
			noProgress = 0
		}
	}
	return result
}

func rememberKnownTask(known, inflight map[ownedTaskKey]struct{}, key ownedTaskKey) {
	if _, exists := known[key]; exists {
		return
	}
	if len(known) >= projectorQueueSize {
		for candidate := range known {
			if _, active := inflight[candidate]; active {
				continue
			}
			delete(known, candidate)
			break
		}
	}
	if len(known) < projectorQueueSize {
		known[key] = struct{}{}
	}
}

func (p *Projector) observePressure(pending, inflight int) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObservePersonDirectoryPressure(PressureObservation{
		Pending: pending, Inflight: inflight, Capacity: projectorQueueSize, Workers: projectorWorkers,
	})
}

// takeBatch joins only already-queued work within the existing page limit.
// It never waits to fill a cohort. An unfitting head stays first for another
// available worker; the ownership lock is released before any cluster call.
func (run *projectorRun) takeBatch() (ownedTaskBatch, bool) {
	run.takeMu.Lock()
	defer run.takeMu.Unlock()
	if run.ctx.Err() != nil {
		return ownedTaskBatch{}, false
	}
	batch := run.next
	run.next = ownedTaskBatch{}
	if len(batch.tasks) == 0 {
		select {
		case <-run.ctx.Done():
			return ownedTaskBatch{}, false
		case batch = <-run.tasks:
		}
	}
	for group := 1; len(batch.tasks) < projectorPageSize; group++ {
		select {
		case next := <-run.tasks:
			if len(batch.tasks)+len(next.tasks) > projectorPageSize {
				run.next = next
				return batch, true
			}
			for _, task := range next.tasks {
				task.batchGroup = group
				batch.tasks = append(batch.tasks, task)
			}
		default:
			return batch, true
		}
	}
	return batch, true
}

func (p *Projector) runWorker(run *projectorRun) {
	for {
		batch, ok := run.takeBatch()
		if !ok {
			return
		}
		keys := make([]ownedTaskKey, len(batch.tasks))
		for i, owned := range batch.tasks {
			keys[i] = ownedTaskKey{hashSlot: owned.hashSlot, channelID: owned.task.ChannelID, channelType: owned.task.ChannelType, generation: owned.task.Generation}
		}
		attemptCtx, cancel := context.WithTimeout(run.ctx, p.attemptTimeout)
		completed, err := p.projectBatch(attemptCtx, batch, func(ready []ownedTaskKey) {
			select {
			case run.results <- taskResult{keys: ready, completed: ready, partial: true}:
			case <-run.ctx.Done():
			}
		})
		cancel()
		result := taskResult{keys: keys, completed: completed, err: err}
		select {
		case run.results <- result:
		case <-run.ctx.Done():
			return
		}
	}
}

func (p *Projector) projectBatch(ctx context.Context, owned ownedTaskBatch, publish func([]ownedTaskKey)) ([]ownedTaskKey, error) {
	if len(owned.tasks) == 0 || len(owned.tasks) > projectorPageSize {
		return nil, metadb.ErrInvalidArgument
	}
	checkedHashSlots := make(map[metadb.HashSlot]struct{}, len(owned.tasks))
	for _, candidate := range owned.tasks {
		if _, checked := checkedHashSlots[candidate.hashSlot]; checked {
			continue
		}
		localLeader, err := p.source.IsLocalLeaderHashSlot(ctx, candidate.hashSlot)
		if err != nil {
			return nil, err
		}
		if !localLeader {
			return nil, errSourceLeadershipLost
		}
		checkedHashSlots[candidate.hashSlot] = struct{}{}
	}
	locations := make([]metadb.PersonDirectoryTaskLocation, len(owned.tasks))
	for i, candidate := range owned.tasks {
		locations[i] = metadb.PersonDirectoryTaskLocation{
			HashSlot: candidate.hashSlot, ChannelID: candidate.task.ChannelID,
			ChannelType: candidate.task.ChannelType, Generation: candidate.task.Generation,
		}
	}
	validation := p.source.ValidatePersonDirectoryTasks(ctx, locations)
	if len(validation) != len(owned.tasks) {
		return nil, errors.New("persondirectory: misaligned task validation results")
	}
	// The scheduler retains the submitted slice long enough to publish its
	// inflight keys. Filtering must not reuse that shared backing array.
	validTasks := make([]ownedTask, 0, len(owned.tasks))
	var validationErr error
	for i, candidate := range owned.tasks {
		if validation[i] != nil {
			if validationErr == nil {
				validationErr = validation[i]
			}
			continue
		}
		validTasks = append(validTasks, candidate)
	}
	owned.tasks = validTasks
	if len(owned.tasks) == 0 {
		return nil, validationErr
	}
	memberships := make([]metadb.UserChannelMembership, 0, len(owned.tasks)*2)
	memberOwners := make([]int, 0, cap(memberships))
	pending := make([]int, len(owned.tasks))
	failed := make([]bool, len(owned.tasks))
	var groups [projectorPageSize]struct {
		first, end, pending int
	}
	for i, candidate := range owned.tasks {
		group := &groups[candidate.batchGroup]
		if group.pending == 0 {
			group.first = i
		}
		group.end = i + 1
		group.pending++
		task := candidate.task
		left, right, err := runtimechannelid.DecodePersonChannel(task.ChannelID)
		if err != nil || runtimechannelid.EncodePersonChannel(left, right) != task.ChannelID || task.ChannelType != 1 || task.Generation == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		joinSeq := task.CommittedTail + 1
		if task.CommittedTail == math.MaxUint64 {
			joinSeq = task.CommittedTail
		}
		memberships = append(memberships, projectedMembership(left, task, joinSeq))
		memberOwners = append(memberOwners, i)
		pending[i] = 1
		if right != left {
			memberships = append(memberships, projectedMembership(right, task, joinSeq))
			memberOwners = append(memberOwners, i)
			pending[i]++
		}
	}

	completed := make([]ownedTaskKey, 0, len(owned.tasks))
	ready := make([]metadb.PersonDirectoryTaskLocation, 0, len(owned.tasks))
	seen := make([]bool, len(memberships))
	remaining := len(memberships)
	firstErr := validationErr
	var invalid bool
	accept := func(index int, err error) {
		if invalid {
			return
		}
		if index < 0 || index >= len(seen) || seen[index] {
			invalid = true
			return
		}
		seen[index] = true
		remaining--
		owner := memberOwners[index]
		pending[owner]--
		if err != nil {
			failed[owner] = true
			if firstErr == nil {
				firstErr = err
			}
		}
		if pending[owner] != 0 {
			return
		}
		group := &groups[owned.tasks[owner].batchGroup]
		group.pending--
		if group.pending != 0 {
			return
		}
		// A Prepare caller joins its full batch. Preserve the original group
		// so a partial Ready write cannot add work without freeing that caller.
		for i := group.first; i < group.end; i++ {
			if failed[i] {
				continue
			}
			task := owned.tasks[i]
			ready = append(ready, metadb.PersonDirectoryTaskLocation{
				HashSlot: task.hashSlot, ChannelID: task.task.ChannelID, ChannelType: task.task.ChannelType, Generation: task.task.Generation,
			})
		}
	}
	recordCompletion := func(result projectionCompletion) {
		if result.misaligned {
			invalid = true
			return
		}
		if firstErr == nil {
			firstErr = result.err
		}
		completed = append(completed, result.keys...)
	}
	complete := func(commitCtx context.Context) {
		if invalid || len(ready) == 0 {
			return
		}
		result := p.commitProjection(commitCtx, ready)
		recordCompletion(result)
		ready = ready[:0]
		if publish != nil && remaining != 0 && len(result.keys) != 0 {
			publish(result.keys)
		}
	}
	if waves, ok := p.memberships.(membershipWaveWriter); ok {
		// Independent groups in the same cohort must keep receiving durable
		// membership results while an earlier group's Ready call is outstanding.
		// Each item generates at most one job; joined calls share the original
		// Slot proposal credits carried in the writer's context.
		parallel := owned.tasks[0].batchGroup != owned.tasks[len(owned.tasks)-1].batchGroup
		type readyJob struct {
			ctx   context.Context
			tasks []metadb.PersonDirectoryTaskLocation
		}
		var jobs chan readyJob
		var done chan projectionCompletion
		var workers sync.WaitGroup
		waves.EnsureUserChannelMembershipWaves(ctx, memberships, func(waveCtx context.Context, indices []int, err error) {
			for _, index := range indices {
				accept(index, err)
			}
			if !parallel {
				complete(waveCtx)
				return
			}
			if invalid || len(ready) == 0 {
				return
			}
			if jobs == nil {
				jobs = make(chan readyJob, len(owned.tasks))
				done = make(chan projectionCompletion, len(owned.tasks))
				workers.Add(projectorReadyWorkers)
				goruntimeregistry.SafeGoN(p.goroutines, goruntimeregistry.TaskMessageDirectoryBatch, projectorReadyWorkers, func(int) {
					defer workers.Done()
					for job := range jobs {
						result := p.commitProjection(job.ctx, job.tasks)
						if publish != nil && len(result.keys) != 0 {
							publish(result.keys)
						}
						done <- result
					}
				})
			}
			jobs <- readyJob{ctx: waveCtx, tasks: ready}
			ready = nil
		})
		if jobs != nil {
			close(jobs)
			workers.Wait()
			close(done)
			for result := range done {
				recordCompletion(result)
			}
		}
	} else {
		results := p.memberships.EnsureUserChannelMembershipBatch(ctx, memberships)
		if len(results) != len(memberships) {
			return nil, errors.New("persondirectory: misaligned membership results")
		}
		for i, result := range results {
			accept(i, result.Err)
		}
		complete(ctx)
	}
	if invalid || remaining != 0 {
		return completed, errors.New("persondirectory: misaligned projection results")
	}
	return completed, firstErr
}

type projectionCompletion struct {
	keys       []ownedTaskKey
	err        error
	misaligned bool
}

// commitProjection retains only source generations that were durably marked Ready.
func (p *Projector) commitProjection(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation) projectionCompletion {
	errs := p.source.CompletePersonDirectoryTasks(ctx, tasks)
	if len(errs) != len(tasks) {
		return projectionCompletion{misaligned: true}
	}
	result := projectionCompletion{keys: make([]ownedTaskKey, 0, len(tasks))}
	for i, err := range errs {
		if err != nil {
			if result.err == nil {
				result.err = err
			}
			continue
		}
		task := tasks[i]
		result.keys = append(result.keys, ownedTaskKey{hashSlot: task.HashSlot, channelID: task.ChannelID, channelType: task.ChannelType, generation: task.Generation})
	}
	return result
}

func projectedMembership(uid string, task metadb.PersonDirectoryTask, joinSeq uint64) metadb.UserChannelMembership {
	return metadb.UserChannelMembership{
		UID: uid, ChannelID: task.ChannelID, ChannelType: task.ChannelType,
		JoinSeq: joinSeq, ReadSeq: task.CommittedTail, DeletedToSeq: task.CommittedTail,
		SourceVersion: task.Generation, UpdatedAt: task.CreatedAt,
	}
}
