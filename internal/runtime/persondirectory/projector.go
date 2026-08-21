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
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	tasks   chan ownedTaskBatch
	results chan taskResult
	done    chan struct{}
	workers sync.WaitGroup
}

type ownedTaskBatch struct {
	tasks []ownedTask
}

type ownedTask struct {
	hashSlot metadb.HashSlot
	task     metadb.PersonDirectoryTask
}

type ownedTaskKey struct {
	hashSlot    metadb.HashSlot
	channelID   string
	channelType int64
	generation  uint64
}

type taskResult struct {
	keys      []ownedTaskKey
	completed []ownedTaskKey
	err       error
}

type projectorScanState struct {
	nextSlot int
	cursors  map[metadb.HashSlot]metadb.PersonDirectoryTaskCursor
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
		wake: make(chan struct{}, 1), tasks: make(chan ownedTaskBatch, projectorWorkers),
		results: make(chan taskResult, projectorQueueSize), done: make(chan struct{}),
	}
	run.workers.Add(projectorWorkers)
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
	inflight := make(map[ownedTaskKey]struct{}, projectorQueueSize)
	known := make(map[ownedTaskKey]struct{}, projectorQueueSize)
	scanState := projectorScanState{cursors: make(map[metadb.HashSlot]metadb.PersonDirectoryTaskCursor)}
	p.scan(run, inflight, known, &scanState)
	p.observePressure(len(known), len(inflight))
	defer p.observePressure(0, 0)
	for {
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
			p.scan(run, inflight, known, &scanState)
			p.observePressure(len(known), len(inflight))
		case <-run.wake:
			p.scan(run, inflight, known, &scanState)
			p.observePressure(len(known), len(inflight))
		case result := <-run.results:
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
			// next page immediately.
			if result.err == nil {
				p.scan(run, inflight, known, &scanState)
				p.observePressure(len(known), len(inflight))
			}
		}
	}
}

func (p *Projector) scan(run *projectorRun, inflight, known map[ownedTaskKey]struct{}, state *projectorScanState) {
	ctx := run.ctx
	hashSlots, err := p.source.LocalLeaderHashSlots(ctx)
	if err != nil {
		return
	}
	if len(hashSlots) == 0 {
		return
	}
	led := make(map[metadb.HashSlot]struct{}, len(hashSlots))
	for _, hashSlot := range hashSlots {
		led[hashSlot] = struct{}{}
	}
	for key := range known {
		if _, stillLed := led[key.hashSlot]; !stillLed {
			delete(known, key)
		}
	}
	for hashSlot := range state.cursors {
		if _, stillLed := led[hashSlot]; !stillLed {
			delete(state.cursors, hashSlot)
		}
	}
	if state.nextSlot >= len(hashSlots) {
		state.nextSlot = 0
	}
	remaining := projectorQueueSize - len(inflight)
	if remaining <= 0 {
		return
	}
	batch := ownedTaskBatch{tasks: make([]ownedTask, 0, projectorPageSize)}
	selected := make(map[ownedTaskKey]struct{}, remaining)
	flush := func() bool {
		if len(batch.tasks) == 0 {
			return true
		}
		select {
		case run.tasks <- batch:
			for _, task := range batch.tasks {
				key := ownedTaskKey{hashSlot: task.hashSlot, channelID: task.task.ChannelID, channelType: task.task.ChannelType, generation: task.task.Generation}
				inflight[key] = struct{}{}
				rememberKnownTask(known, inflight, key)
			}
			batch = ownedTaskBatch{tasks: make([]ownedTask, 0, projectorPageSize)}
			return true
		case <-ctx.Done():
			return false
		}
	}
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
			if _, exists := inflight[key]; exists {
				continue
			}
			if _, exists := selected[key]; exists {
				continue
			}
			selected[key] = struct{}{}
			batch.tasks = append(batch.tasks, ownedTask{hashSlot: hashSlot, task: task})
			remaining--
			added++
			if len(batch.tasks) == projectorPageSize && !flush() {
				return
			}
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
	flush()
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

func (p *Projector) runWorker(run *projectorRun) {
	for {
		select {
		case <-run.ctx.Done():
			return
		case batch := <-run.tasks:
			keys := make([]ownedTaskKey, len(batch.tasks))
			for i, owned := range batch.tasks {
				keys[i] = ownedTaskKey{hashSlot: owned.hashSlot, channelID: owned.task.ChannelID, channelType: owned.task.ChannelType, generation: owned.task.Generation}
			}
			attemptCtx, cancel := context.WithTimeout(run.ctx, p.attemptTimeout)
			completed, err := p.projectBatch(attemptCtx, batch)
			cancel()
			result := taskResult{keys: keys, completed: completed, err: err}
			select {
			case run.results <- result:
			case <-run.ctx.Done():
				return
			}
		}
	}
}

func (p *Projector) projectBatch(ctx context.Context, owned ownedTaskBatch) ([]ownedTaskKey, error) {
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
	for _, candidate := range owned.tasks {
		task := candidate.task
		left, right, err := runtimechannelid.DecodePersonChannel(task.ChannelID)
		if err != nil || runtimechannelid.EncodePersonChannel(left, right) != task.ChannelID || task.ChannelType != 1 || task.Generation == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		joinSeq := task.CommittedTail + 1
		if task.CommittedTail == math.MaxUint64 {
			joinSeq = task.CommittedTail
		}
		memberships = append(memberships,
			projectedMembership(left, task, joinSeq),
			projectedMembership(right, task, joinSeq),
		)
	}
	results := p.memberships.EnsureUserChannelMembershipBatch(ctx, memberships)
	if len(results) != len(memberships) {
		return nil, errors.New("persondirectory: misaligned membership results")
	}
	completionTasks := make([]metadb.PersonDirectoryTaskLocation, 0, len(owned.tasks))
	completionKeys := make([]ownedTaskKey, 0, len(owned.tasks))
	firstErr := validationErr
	for i, candidate := range owned.tasks {
		leftResult, rightResult := results[i*2], results[i*2+1]
		if leftResult.Err == nil && rightResult.Err == nil {
			completionTasks = append(completionTasks, metadb.PersonDirectoryTaskLocation{
				HashSlot: candidate.hashSlot, ChannelID: candidate.task.ChannelID, ChannelType: candidate.task.ChannelType, Generation: candidate.task.Generation,
			})
			completionKeys = append(completionKeys, ownedTaskKey{
				hashSlot: candidate.hashSlot, channelID: candidate.task.ChannelID, channelType: candidate.task.ChannelType, generation: candidate.task.Generation,
			})
			continue
		}
		if firstErr == nil {
			if leftResult.Err != nil {
				firstErr = leftResult.Err
			} else {
				firstErr = rightResult.Err
			}
		}
	}
	completed := make([]ownedTaskKey, 0, len(completionTasks))
	if len(completionTasks) > 0 {
		completionResults := p.source.CompletePersonDirectoryTasks(ctx, completionTasks)
		if len(completionResults) != len(completionTasks) {
			return nil, errors.New("persondirectory: misaligned completion results")
		}
		for i, completionErr := range completionResults {
			if completionErr == nil {
				completed = append(completed, completionKeys[i])
				continue
			}
			if firstErr == nil {
				firstErr = completionErr
			}
		}
	}
	return completed, firstErr
}

func projectedMembership(uid string, task metadb.PersonDirectoryTask, joinSeq uint64) metadb.UserChannelMembership {
	return metadb.UserChannelMembership{
		UID: uid, ChannelID: task.ChannelID, ChannelType: task.ChannelType,
		JoinSeq: joinSeq, ReadSeq: task.CommittedTail, DeletedToSeq: task.CommittedTail,
		SourceVersion: task.Generation, UpdatedAt: task.CreatedAt,
	}
}
