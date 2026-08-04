package chatlifecycle

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const relationshipLogicalBase = uint64(1) << 62

var (
	errEngineConfig     = errors.New("chat lifecycle engine: configuration is invalid")
	errEngineRunning    = errors.New("chat lifecycle engine: already running")
	errEngineNotRunning = errors.New("chat lifecycle engine: not running")
)

// EngineConfig fixes every local retained-state and per-advance CPU bound.
type EngineConfig struct {
	Clock     SessionClock
	Sessions  *SessionPool
	Schedule  ScheduleModel
	Graph     RelationshipGraph
	Traffic   TrafficModel
	Generator *TrafficGenerator
	Retry     RetryPolicy
	Verifier  *Verifier
	Evidence  *EvidenceRecorder

	CommandCapacity   int
	WorkCapacity      int
	RetryCapacity     int
	InflightCapacity  int
	MaxWorkPerAdvance int
	AttemptTimeout    time.Duration
}

// EngineSnapshot is constant-size worker runtime evidence.
type EngineSnapshot struct {
	Running                 bool
	Generation              uint64
	ActiveLoops             int
	Online                  int
	LoginStarting           int
	TrafficReady            int
	QueueCurrent            int
	FutureCurrent           int
	ActivityCurrent         int
	QueuePeak               int
	QueueCapacity           int
	RetryQueueDepth         int
	RetryQueuePeak          int
	RetryQueueCapacity      int
	InflightCurrent         int
	InflightPeak            int
	InflightCapacity        int
	TransportQueueDepth     int
	TransportQueueCapacity  int
	TransportInflight       int
	RelationshipLookback    int
	ActiveLifecycleTimers   int
	ColdEvidencePending     int
	RetryAttempts           uint64
	FinalFailures           uint64
	HarnessInvalid          uint64
	CommandSaturation       uint64
	CompletionQueueDepth    int
	CompletionQueueCapacity int
	Classification          SyncClassification
	NextFutureAt            time.Time
	NextRetryAt             time.Time
}

type engineWorkKind uint8

const (
	engineWorkSend engineWorkKind = iota + 1
	engineWorkTimeout
	engineWorkLifecycle
)

type engineWork struct {
	due                 time.Time
	kind                engineWorkKind
	intent              TrafficIntent
	attempt             uint8
	order               uint64
	index               int
	edge                RelationshipEdge
	schedule            ChannelSchedule
	relationshipOrdinal uint64
	coldConfirmed       bool
}

type engineWorkHeap []*engineWork

func (h engineWorkHeap) Len() int { return len(h) }
func (h engineWorkHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].order < h[j].order
	}
	return h[i].due.Before(h[j].due)
}
func (h engineWorkHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *engineWorkHeap) Push(value any) {
	work := value.(*engineWork)
	work.index = len(*h)
	*h = append(*h, work)
}
func (h *engineWorkHeap) Pop() any {
	old := *h
	last := len(old) - 1
	work := old[last]
	old[last] = nil
	work.index = -1
	*h = old[:last]
	return work
}

type engineInflight struct {
	intent         TrafficIntent
	attempt        uint8
	retryScheduled bool
	timeout        *engineWork
}

type engineCommand struct {
	run func()
}

type engineCompletion struct {
	ack             *frame.SendackPacket
	verificationErr error
}

type advanceResult struct {
	processed int
	err       error
}

type activationResult struct {
	activated bool
	err       error
}

// Engine owns one bounded command loop, all future work heaps, and only the
// online SessionPool. It creates no goroutine or timer per user or channel.
type Engine struct {
	clock     SessionClock
	sessions  *SessionPool
	schedule  ScheduleModel
	graph     RelationshipGraph
	traffic   TrafficModel
	generator *TrafficGenerator
	retry     RetryPolicy
	verifier  *Verifier
	evidence  *EvidenceRecorder

	commandCapacity  int
	workCapacity     int
	retryCapacity    int
	inflightCapacity int
	maxWork          int
	attemptTimeout   time.Duration

	lifecycleMu sync.Mutex
	running     bool
	accepting   bool
	stopping    bool
	generation  uint64
	commands    chan engineCommand
	completions chan engineCompletion
	stop        chan struct{}
	done        chan struct{}
	cached      EngineSnapshot
	sessionOps  sync.WaitGroup

	activeLoops       atomic.Int64
	commandSaturation atomic.Uint64

	// The fields below are owned exclusively by the active command loop.
	work                  engineWorkHeap
	activity              engineWorkHeap
	retries               *RetryScheduler
	inflight              map[string]*engineInflight
	workPeak              int
	queuedSends           int
	inflightPeak          int
	nextOrder             uint64
	activeLifecycleTimers int
	lifecycleByChannel    map[string]*engineWork
	retryAttempts         uint64
	finalFailures         uint64
	harnessInvalid        uint64
	now                   time.Time
}

// NewEngine wires the existing deterministic models and bounded verifier.
func NewEngine(config EngineConfig) (*Engine, error) {
	if config.Clock == nil || config.Sessions == nil || config.Schedule.identity == nil ||
		config.Graph.identity == nil || config.Traffic.identity == nil || config.Generator == nil ||
		config.Retry.identity == nil || config.Verifier == nil || config.Evidence == nil ||
		config.CommandCapacity <= 0 || config.WorkCapacity <= 0 || config.RetryCapacity <= 0 ||
		config.InflightCapacity <= 0 || config.MaxWorkPerAdvance <= 0 || config.AttemptTimeout <= 0 ||
		config.CommandCapacity > maxVerifierCapacity || config.WorkCapacity > maxVerifierCapacity ||
		config.RetryCapacity > maxVerifierCapacity || config.InflightCapacity > maxVerifierCapacity {
		return nil, errEngineConfig
	}
	identity := config.Schedule.identity
	if config.Graph.identity != identity || config.Traffic.identity != identity || config.Retry.identity != identity ||
		config.Generator.identity != identity || config.Sessions.identity != identity {
		return nil, errEngineConfig
	}
	engine := &Engine{
		clock: config.Clock, sessions: config.Sessions, schedule: config.Schedule, graph: config.Graph,
		traffic: config.Traffic, generator: config.Generator, retry: config.Retry,
		verifier: config.Verifier, evidence: config.Evidence,
		commandCapacity: config.CommandCapacity, workCapacity: config.WorkCapacity,
		retryCapacity: config.RetryCapacity, inflightCapacity: config.InflightCapacity,
		maxWork: config.MaxWorkPerAdvance, attemptTimeout: config.AttemptTimeout,
	}
	if err := config.Sessions.setSendackObserver(engine.sessionSendack); err != nil {
		return nil, err
	}
	engine.cached = engine.emptySnapshot(false)
	return engine, nil
}

// Start creates one fresh generation and discards all prior generator credit.
func (e *Engine) Start(ctx context.Context) error {
	if e == nil {
		return errEngineConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.running {
		return errEngineRunning
	}
	e.evidence.reset()
	e.verifier.resetRuntime()
	if err := e.sessions.resetRuntime(); err != nil {
		return err
	}
	if err := e.generator.reset(e.clock.Now()); err != nil {
		return err
	}
	retries, err := NewRetryScheduler(e.retry, e.retryCapacity)
	if err != nil {
		return err
	}
	e.work = nil
	heap.Init(&e.work)
	e.activity = nil
	heap.Init(&e.activity)
	e.retries = retries
	e.inflight = make(map[string]*engineInflight)
	e.lifecycleByChannel = make(map[string]*engineWork)
	e.workPeak = 0
	e.queuedSends = 0
	e.inflightPeak = 0
	e.nextOrder = 0
	e.activeLifecycleTimers = 0
	e.retryAttempts = 0
	e.finalFailures = 0
	e.harnessInvalid = 0
	e.commandSaturation.Store(0)
	e.now = e.clock.Now()
	e.commands = make(chan engineCommand, e.commandCapacity)
	e.completions = make(chan engineCompletion, e.commandCapacity)
	e.stop = make(chan struct{})
	e.done = make(chan struct{})
	e.generation++
	e.running = true
	e.accepting = true
	e.stopping = false
	e.cached = e.emptySnapshot(true)
	e.activeLoops.Add(1)
	go e.loop(e.commands, e.stop, e.done)
	return nil
}

// Stop fences admission, joins every session drain, then joins the sole engine loop.
func (e *Engine) Stop() error {
	if e == nil {
		return nil
	}
	e.lifecycleMu.Lock()
	if !e.running {
		e.lifecycleMu.Unlock()
		return nil
	}
	if e.stopping {
		done := e.done
		e.lifecycleMu.Unlock()
		<-done
		return nil
	}
	e.stopping = true
	e.accepting = false
	stop := e.stop
	done := e.done
	e.lifecycleMu.Unlock()

	e.sessionOps.Wait()
	closeErr := e.sessions.CloseAll()
	barrier := make(chan struct{}, 1)
	e.commands <- engineCommand{run: func() {
		e.drainCompletions()
		barrier <- struct{}{}
	}}
	<-barrier
	close(stop)
	<-done
	e.lifecycleMu.Lock()
	e.running = false
	e.stopping = false
	e.cached.Running = false
	e.cached.ActiveLoops = int(e.activeLoops.Load())
	e.lifecycleMu.Unlock()
	return closeErr
}

// Login serializes fresh session ownership through the engine generation.
func (e *Engine) Login(ctx context.Context, login SessionLogin) (SessionSnapshot, error) {
	if !e.beginSessionOp() {
		return SessionSnapshot{}, errEngineNotRunning
	}
	defer e.sessionOps.Done()
	return e.sessions.Login(ctx, login)
}

// Logout applies the joined session boundary inside the active generation.
func (e *Engine) Logout(uid string) error {
	if !e.beginSessionOp() {
		return errEngineNotRunning
	}
	defer e.sessionOps.Done()
	return e.sessions.Logout(uid)
}

func (e *Engine) beginSessionOp() bool {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || !e.accepting {
		return false
	}
	e.sessionOps.Add(1)
	return true
}

// SubmitGranted retains one transient SEND whose caller already owns a primary
// grant (or the explicitly separate canary grant). Lifecycle activity itself
// never calls this method; Tick substitutes it inside person primary grants.
func (e *Engine) SubmitGranted(intent TrafficIntent, due time.Time) error {
	response := make(chan error, 1)
	if err := e.enqueue(engineCommand{run: func() { response <- e.addSendWork(intent, 0, due) }}); err != nil {
		return err
	}
	return <-response
}

// Tick streams one global TrafficGenerator grant into the bounded future heap
// and admits at most one independently due canary.
func (e *Engine) Tick(now time.Time, demand []uint64) (TrafficTickSnapshot, error) {
	response := make(chan struct {
		snapshot TrafficTickSnapshot
		err      error
	}, 1)
	if err := e.enqueue(engineCommand{run: func() {
		e.now = now
		snapshot, tickErr := e.generator.Tick(demand, func(intent TrafficIntent) error {
			if intent.Kind == TrafficPerson && len(e.activity) > 0 && !e.activity[0].due.After(now) {
				activity := heap.Pop(&e.activity).(*engineWork)
				var retargetErr error
				intent, retargetErr = e.retargetPersonGrant(intent, activity.intent)
				if retargetErr != nil {
					return retargetErr
				}
			}
			return e.addSendWork(intent, 0, now)
		})
		if tickErr == nil {
			if canary, due, canaryErr := e.generator.NextCanary(now); canaryErr != nil {
				tickErr = canaryErr
			} else if due {
				tickErr = e.addSendWork(canary, 0, now)
			}
		}
		response <- struct {
			snapshot TrafficTickSnapshot
			err      error
		}{snapshot: snapshot, err: tickErr}
	}}); err != nil {
		return TrafficTickSnapshot{}, err
	}
	result := <-response
	return result.snapshot, result.err
}

// ActivateRelationship schedules the existing Phase 2 initial burst and one
// bounded lifecycle deadline, but only while both endpoints are online.
func (e *Engine) ActivateRelationship(edge RelationshipEdge, relationshipOrdinal uint64) (bool, error) {
	response := make(chan activationResult, 1)
	if err := e.enqueue(engineCommand{run: func() {
		activated, activationErr := e.activateRelationship(edge, relationshipOrdinal)
		response <- activationResult{activated: activated, err: activationErr}
	}}); err != nil {
		return false, err
	}
	result := <-response
	return result.activated, result.err
}

// ObserveNewUser reconstructs only the previous five possible owners and
// retains no historical adjacency map.
func (e *Engine) ObserveNewUser(userIndex uint64) (considered, activated int, err error) {
	response := make(chan struct {
		considered int
		activated  int
		err        error
	}, 1)
	if enqueueErr := e.enqueue(engineCommand{run: func() {
		incoming := e.graph.Incoming(userIndex)
		result := struct {
			considered int
			activated  int
			err        error
		}{considered: incoming.Count}
		for index := 0; index < incoming.Count; index++ {
			edge := incoming.Items[index]
			ordinal := edge.OwnerIndex*MaxForwardRelationships + uint64(index)
			wasActivated, activationErr := e.activateRelationship(edge, ordinal)
			if activationErr != nil {
				result.err = activationErr
				break
			}
			if wasActivated {
				result.activated++
			}
		}
		response <- result
	}}); enqueueErr != nil {
		return 0, 0, enqueueErr
	}
	result := <-response
	return result.considered, result.activated, result.err
}

// ApproveColdRevisit attaches prior all-node cold evidence to one still-active
// revisit timer. It never creates a timer or retains historical channel state.
func (e *Engine) ApproveColdRevisit(personChannelID string) (bool, error) {
	response := make(chan bool, 1)
	if err := e.enqueue(engineCommand{run: func() {
		work := e.lifecycleByChannel[personChannelID]
		if work == nil || work.schedule.Class != LifecycleRevisit || !work.schedule.RequiresColdRuntimeEvidence {
			response <- false
			return
		}
		work.coldConfirmed = true
		response <- true
	}}); err != nil {
		return false, err
	}
	return <-response, nil
}

// Advance processes due heaps with a fixed CPU work budget.
func (e *Engine) Advance(now time.Time) (int, error) {
	if !e.beginSessionOp() {
		return 0, errEngineNotRunning
	}
	e.sessions.Expire(now)
	e.sessionOps.Done()
	response := make(chan advanceResult, 1)
	if err := e.enqueue(engineCommand{run: func() { response <- e.advance(now) }}); err != nil {
		return 0, err
	}
	result := <-response
	return result.processed, result.err
}

// ObserveSendack completes engine inflight ownership after Verifier processing.
func (e *Engine) ObserveSendack(_ string, ack *frame.SendackPacket, verificationErr error) error {
	response := make(chan error, 1)
	if err := e.enqueueBlocking(engineCommand{run: func() { response <- e.observeSendack(ack, verificationErr) }}); err != nil {
		return err
	}
	return <-response
}

// Snapshot works both while running and after the joined stop baseline.
func (e *Engine) Snapshot() (EngineSnapshot, error) {
	e.lifecycleMu.Lock()
	running := e.running
	cached := e.cached
	e.lifecycleMu.Unlock()
	if !running {
		return cached, nil
	}
	response := make(chan EngineSnapshot, 1)
	if err := e.enqueueBlocking(engineCommand{run: func() {
		e.drainCompletions()
		response <- e.buildSnapshot(true)
	}}); err != nil {
		return EngineSnapshot{}, err
	}
	return <-response, nil
}

func (e *Engine) loop(commands <-chan engineCommand, stop <-chan struct{}, done chan<- struct{}) {
	defer func() {
		e.cleanupInflight()
		e.work = nil
		e.activity = nil
		e.queuedSends = 0
		if e.retries != nil {
			e.retries.entries = nil
			e.retries.byMessage = nil
		}
		e.activeLifecycleTimers = 0
		e.lifecycleByChannel = nil
		e.activeLoops.Add(-1)
		snapshot := e.buildSnapshot(false)
		e.lifecycleMu.Lock()
		e.cached = snapshot
		e.lifecycleMu.Unlock()
		close(done)
	}()
	for {
		select {
		case command := <-commands:
			command.run()
		case completion := <-e.completions:
			_ = e.observeSendack(completion.ack, completion.verificationErr)
		case <-stop:
			return
		}
	}
}

func (e *Engine) enqueue(command engineCommand) error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || !e.accepting {
		return errEngineNotRunning
	}
	commands := e.commands
	select {
	case commands <- command:
		return nil
	default:
		e.commandSaturation.Add(1)
		_ = e.evidence.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeEngineQueueSaturated, Value: uint64(e.commandCapacity)})
		return &RuntimeError{code: RuntimeFailureEngineQueueSaturated}
	}
}

func (e *Engine) enqueueBlocking(command engineCommand) error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || e.stopping {
		return errEngineNotRunning
	}
	e.commands <- command
	return nil
}

func (e *Engine) sessionSendack(_ string, ack *frame.SendackPacket, verificationErr error) {
	e.lifecycleMu.Lock()
	if !e.running {
		e.lifecycleMu.Unlock()
		return
	}
	completions := e.completions
	stop := e.stop
	e.lifecycleMu.Unlock()
	select {
	case completions <- engineCompletion{ack: ack, verificationErr: verificationErr}:
	case <-stop:
	}
}

func (e *Engine) addSendWork(intent TrafficIntent, attempt uint8, due time.Time) error {
	if e.futureCount() >= e.workCapacity {
		return e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	work := &engineWork{due: due, kind: engineWorkSend, intent: intent, attempt: attempt, order: e.nextOrder}
	e.nextOrder++
	heap.Push(&e.work, work)
	e.queuedSends++
	e.observeWorkPeak()
	return nil
}

func (e *Engine) addWork(work *engineWork) error {
	if e.futureCount() >= e.workCapacity {
		return e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	work.order = e.nextOrder
	e.nextOrder++
	heap.Push(&e.work, work)
	e.observeWorkPeak()
	return nil
}

func (e *Engine) addActivity(work *engineWork) error {
	if e.futureCount() >= e.workCapacity {
		return e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	work.order = e.nextOrder
	e.nextOrder++
	heap.Push(&e.activity, work)
	e.observeWorkPeak()
	return nil
}

func (e *Engine) futureCount() int { return len(e.work) + len(e.activity) }

func (e *Engine) observeWorkPeak() {
	if current := e.futureCount(); current > e.workPeak {
		e.workPeak = current
	}
}

func (e *Engine) advance(now time.Time) advanceResult {
	e.now = now
	e.verifier.ExpireCorrelations(now)
	e.drainCompletions()
	var result advanceResult
	for result.processed < e.maxWork {
		workDue := len(e.work) > 0 && !e.work[0].due.After(now)
		retryDue := e.retries != nil && e.retries.due(now)
		if !workDue && !retryDue {
			break
		}
		if retryDue && (!workDue || !e.work[0].due.Before(e.retries.entries[0].Due)) {
			retries := e.retries.PopDue(now, 1)
			if len(retries) == 1 {
				result.err = errors.Join(result.err, e.processAttempt(retries[0].Intent, retries[0].Attempt.Attempt, now))
			}
		} else {
			work := heap.Pop(&e.work).(*engineWork)
			if work.kind == engineWorkSend {
				e.queuedSends--
			}
			result.err = errors.Join(result.err, e.processWork(work, now))
		}
		result.processed++
		e.drainCompletions()
	}
	if (len(e.work) > 0 && !e.work[0].due.After(now)) || (e.retries != nil && e.retries.due(now)) {
		result.err = errors.Join(result.err, e.recordRuntimeFailure(RuntimeFailureEngineCPUSaturated, uint64(e.maxWork)))
	}
	return result
}

func (e *Engine) drainCompletions() {
	for {
		select {
		case completion := <-e.completions:
			_ = e.observeSendack(completion.ack, completion.verificationErr)
		default:
			return
		}
	}
}

func (e *Engine) processWork(work *engineWork, now time.Time) error {
	switch work.kind {
	case engineWorkSend:
		return e.processAttempt(work.intent, work.attempt, now)
	case engineWorkTimeout:
		inflight := e.inflight[work.intent.Logical.ClientMsgNo]
		if inflight == nil || inflight.attempt != work.attempt {
			return nil
		}
		inflight.timeout = nil
		return e.scheduleRetry(inflight, now)
	case engineWorkLifecycle:
		delete(e.lifecycleByChannel, work.edge.PersonChannelID)
		if e.activeLifecycleTimers > 0 {
			e.activeLifecycleTimers--
		}
		if work.schedule.Class != LifecycleRevisit || !e.sessions.CanActivate(work.edge) {
			return nil
		}
		if work.schedule.RequiresColdRuntimeEvidence && !work.coldConfirmed {
			return nil
		}
		return e.scheduleRelationshipMessages(work.edge, work.relationshipOrdinal, 8, work.schedule.RevisitMessages, work.due, work.schedule.InitialBurst.Window)
	default:
		return errEngineConfig
	}
}

func (e *Engine) processAttempt(intent TrafficIntent, attempt uint8, now time.Time) error {
	logical := intent.Logical
	inflight := e.inflight[logical.ClientMsgNo]
	if attempt == 0 {
		if inflight != nil {
			return errEngineConfig
		}
		if len(e.inflight) >= e.inflightCapacity {
			return e.recordRuntimeFailure(RuntimeFailureInflightSaturated, uint64(e.inflightCapacity))
		}
		if err := e.verifier.RegisterSend(logical, now); err != nil {
			return err
		}
		inflight = &engineInflight{intent: intent}
		e.inflight[logical.ClientMsgNo] = inflight
		if len(e.inflight) > e.inflightPeak {
			e.inflightPeak = len(e.inflight)
		}
	} else if inflight == nil {
		return nil
	}
	inflight.retryScheduled = false
	attemptPlan, err := e.retry.Attempt(logical, attempt)
	if err != nil {
		return e.abortHarness(inflight, err)
	}
	if err := e.verifier.ObserveAttempt(logical, attemptPlan); err != nil {
		return e.abortHarness(inflight, err)
	}
	inflight.attempt = attempt
	if attempt > 0 {
		e.retryAttempts++
	}
	if err := e.sessions.Send(context.Background(), logical.Sender, intent.Packet); err != nil {
		return e.scheduleRetry(inflight, now)
	}
	deadline := now.Add(e.attemptTimeout)
	if deadline.Before(now) {
		return e.abortHarness(inflight, errEngineConfig)
	}
	timeout := &engineWork{due: deadline, kind: engineWorkTimeout, intent: intent, attempt: attempt}
	if err := e.addWork(timeout); err != nil {
		return e.abortHarness(inflight, err)
	}
	inflight.timeout = timeout
	return nil
}

func (e *Engine) scheduleRetry(inflight *engineInflight, now time.Time) error {
	if inflight.retryScheduled {
		return nil
	}
	e.cancelAttemptTimeout(inflight)
	retry, err := e.retries.Schedule(inflight.intent, inflight.attempt, now)
	if err == nil {
		_ = retry
		inflight.retryScheduled = true
		return nil
	}
	if errors.Is(err, ErrRetryLimitReached) {
		logical := inflight.intent.Logical
		terminalErr := e.verifier.CompleteTerminal(logical, TerminalSendRetryExhausted)
		_ = e.verifier.ReleaseSend(logical)
		delete(e.inflight, logical.ClientMsgNo)
		e.finalFailures++
		return terminalErr
	}
	if runtimeErr := new(RuntimeError); errors.As(err, &runtimeErr) {
		e.recordRuntimeFailure(RuntimeFailureRetryQueueSaturated, uint64(e.retryCapacity))
		return e.abortHarness(inflight, err)
	}
	return e.abortHarness(inflight, err)
}

func (e *Engine) observeSendack(ack *frame.SendackPacket, verificationErr error) error {
	if ack == nil {
		return verificationErr
	}
	inflight := e.inflight[ack.ClientMsgNo]
	if inflight == nil {
		return verificationErr
	}
	var rejected *SendackRejectedError
	if errors.As(verificationErr, &rejected) {
		if !retriableSendackReason(rejected.ReasonCode()) {
			logical := inflight.intent.Logical
			e.cancelAttemptTimeout(inflight)
			e.retries.cancel(ack.ClientMsgNo)
			terminalErr := e.verifier.CompleteTerminal(logical, TerminalSendNonRetriable)
			_ = e.verifier.ReleaseSend(logical)
			delete(e.inflight, ack.ClientMsgNo)
			e.finalFailures++
			return terminalErr
		}
		return e.scheduleRetry(inflight, e.clock.Now())
	}
	logical := inflight.intent.Logical
	if ack.ReasonCode == frame.ReasonSuccess && ack.MessageID > 0 && ack.MessageSeq > 0 {
		e.cancelAttemptTimeout(inflight)
		e.retries.cancel(ack.ClientMsgNo)
		delete(e.inflight, ack.ClientMsgNo)
		return errors.Join(verificationErr, e.verifier.ReleaseSend(logical))
	}
	e.cancelAttemptTimeout(inflight)
	e.retries.cancel(ack.ClientMsgNo)
	terminalErr := e.verifier.CompleteTerminal(logical, TerminalSendNonRetriable)
	releaseErr := e.verifier.ReleaseSend(logical)
	delete(e.inflight, ack.ClientMsgNo)
	e.finalFailures++
	return errors.Join(verificationErr, terminalErr, releaseErr)
}

func retriableSendackReason(reason frame.ReasonCode) bool {
	switch reason {
	case frame.ReasonUnknown, frame.ReasonUserNotOnNode, frame.ReasonForwardSendPacketError,
		frame.ReasonSystemError, frame.ReasonNodeMatchError, frame.ReasonNodeNotMatch,
		frame.ReasonRateLimit:
		return true
	default:
		return false
	}
}

func (e *Engine) abortHarness(inflight *engineInflight, cause error) error {
	if inflight != nil {
		logical := inflight.intent.Logical
		e.cancelAttemptTimeout(inflight)
		e.retries.cancel(logical.ClientMsgNo)
		_ = e.verifier.abortSendHarness(logical)
		delete(e.inflight, logical.ClientMsgNo)
	}
	return cause
}

func (e *Engine) cancelAttemptTimeout(inflight *engineInflight) {
	if inflight == nil || inflight.timeout == nil {
		return
	}
	if inflight.timeout.index >= 0 {
		heap.Remove(&e.work, inflight.timeout.index)
	}
	inflight.timeout = nil
}

func (e *Engine) activateRelationship(edge RelationshipEdge, relationshipOrdinal uint64) (bool, error) {
	if !e.sessions.CanActivate(edge) {
		return false, nil
	}
	schedule, err := e.schedule.Channel(relationshipOrdinal, edge.OwnerIndex, edge.PeerIndex)
	if err != nil {
		return false, err
	}
	needed := schedule.InitialBurst.MessageCount
	if schedule.Class != LifecycleOneShot {
		needed++
	}
	if needed > e.workCapacity-e.futureCount() {
		return false, e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	if err := e.scheduleRelationshipMessages(edge, relationshipOrdinal, 0, schedule.InitialBurst.MessageCount, e.now, schedule.InitialBurst.Window); err != nil {
		return false, err
	}
	var lifecycleDue time.Time
	switch schedule.Class {
	case LifecycleRevisit:
		lifecycleDue = e.now.Add(schedule.RevisitAfter)
	case LifecycleRotating, LifecycleLong:
		lifecycleDue = e.now.Add(schedule.ActiveFor)
	}
	if !lifecycleDue.IsZero() {
		work := &engineWork{
			due: lifecycleDue, kind: engineWorkLifecycle, edge: edge, schedule: schedule,
			relationshipOrdinal: relationshipOrdinal,
		}
		if err := e.addWork(work); err != nil {
			return false, err
		}
		if schedule.RequiresColdRuntimeEvidence {
			e.lifecycleByChannel[edge.PersonChannelID] = work
		}
		e.activeLifecycleTimers++
	}
	return true, nil
}

func (e *Engine) scheduleRelationshipMessages(edge RelationshipEdge, relationshipOrdinal, logicalOffset uint64, count int, start time.Time, window time.Duration) error {
	direction, err := e.traffic.DirectionFor(relationshipOrdinal)
	if err != nil {
		return err
	}
	for messageIndex := 0; messageIndex < count; messageIndex++ {
		var offset time.Duration
		if count > 1 {
			offset = time.Duration((uint64(window) * uint64(messageIndex)) / uint64(count-1))
		}
		sender, err := SenderFor(direction, uint64(messageIndex), edge.OwnerUID, edge.PeerUID)
		if err != nil {
			return err
		}
		target := edge.OwnerUID
		if sender == edge.OwnerUID {
			target = edge.PeerUID
		}
		messageOffset := logicalOffset + uint64(messageIndex)
		if messageOffset >= 16 || relationshipOrdinal > (math.MaxUint64-relationshipLogicalBase-messageOffset)/16 {
			return errEngineConfig
		}
		logicalOrdinal := relationshipLogicalBase + relationshipOrdinal*16 + messageOffset
		workerID := edge.OwnerIndex % e.traffic.identity.Workers()
		logical, err := e.traffic.NewLogicalSend(workerID, logicalOrdinal, TrafficPerson, sender, target)
		if err != nil {
			return err
		}
		payloadBytes, err := e.traffic.PayloadSizeFor(logicalOrdinal)
		if err != nil {
			return err
		}
		payload, err := e.traffic.BuildPayload(logical, payloadBytes)
		if err != nil {
			return err
		}
		intent := TrafficIntent{
			Logical: logical, Packet: packetForTrafficIntent(logical, payload), Kind: TrafficPerson,
			Direction: direction, ChannelID: edge.PersonChannelID, PayloadBytes: payloadBytes,
		}
		if err := e.addActivity(&engineWork{due: start.Add(offset), kind: engineWorkSend, intent: intent}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) retargetPersonGrant(grant, activity TrafficIntent) (TrafficIntent, error) {
	logical, err := e.traffic.NewLogicalSend(
		uint64(grant.Logical.WorkerID), grant.Logical.LogicalSend, TrafficPerson,
		activity.Logical.Sender, activity.Logical.Target,
	)
	if err != nil {
		return TrafficIntent{}, err
	}
	payload, err := e.traffic.BuildPayload(logical, grant.PayloadBytes)
	if err != nil {
		return TrafficIntent{}, err
	}
	activity.Logical = logical
	activity.Packet = packetForTrafficIntent(logical, payload)
	activity.PayloadBytes = grant.PayloadBytes
	return activity, nil
}

func (e *Engine) cleanupInflight() {
	for clientMsgNo, inflight := range e.inflight {
		logical := inflight.intent.Logical
		if err := e.verifier.CompleteTerminal(logical, TerminalSendSessionClosed); err != nil {
			_ = e.verifier.abortSendHarness(logical)
		} else {
			_ = e.verifier.ReleaseSend(logical)
		}
		delete(e.inflight, clientMsgNo)
	}
}

func (e *Engine) recordRuntimeFailure(code RuntimeFailureCode, value uint64) error {
	e.harnessInvalid++
	failureCode := FailureCodeEngineQueueSaturated
	switch code {
	case RuntimeFailureEngineCPUSaturated:
		failureCode = FailureCodeEngineCPUSaturated
	case RuntimeFailureInflightSaturated:
		failureCode = FailureCodeEngineInflightSaturated
	case RuntimeFailureRetryQueueSaturated:
		failureCode = FailureCodeEngineRetrySaturated
	}
	_ = e.evidence.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: failureCode, Value: value})
	return &RuntimeError{code: code}
}

func (e *Engine) buildSnapshot(running bool) EngineSnapshot {
	sessions := e.sessions.Snapshot()
	retries := RetrySchedulerSnapshot{}
	if e.retries != nil {
		retries = e.retries.Snapshot()
	}
	snapshot := EngineSnapshot{
		Running: running, Generation: e.generation, ActiveLoops: int(e.activeLoops.Load()),
		Online: sessions.Online, LoginStarting: sessions.Starting, TrafficReady: sessions.TrafficReady,
		QueueCurrent: e.queuedSends, FutureCurrent: e.futureCount(), ActivityCurrent: len(e.activity),
		QueuePeak: e.workPeak, QueueCapacity: e.workCapacity,
		RetryQueueDepth: retries.Depth, RetryQueuePeak: retries.Peak, RetryQueueCapacity: e.retryCapacity,
		InflightCurrent: len(e.inflight), InflightPeak: e.inflightPeak, InflightCapacity: e.inflightCapacity,
		TransportQueueDepth: sessions.QueueDepth, TransportQueueCapacity: sessions.QueueCapacity,
		TransportInflight: sessions.TransportInflight, RelationshipLookback: MaxForwardRelationships,
		ActiveLifecycleTimers: e.activeLifecycleTimers, ColdEvidencePending: len(e.lifecycleByChannel),
		RetryAttempts: e.retryAttempts,
		FinalFailures: e.finalFailures, HarnessInvalid: e.harnessInvalid + e.commandSaturation.Load(),
		CommandSaturation: e.commandSaturation.Load(), Classification: e.evidence.Snapshot().Classification,
		CompletionQueueDepth: len(e.completions), CompletionQueueCapacity: cap(e.completions),
	}
	if len(e.work) > 0 {
		snapshot.NextFutureAt = e.work[0].due
	}
	if len(e.activity) > 0 && (snapshot.NextFutureAt.IsZero() || e.activity[0].due.Before(snapshot.NextFutureAt)) {
		snapshot.NextFutureAt = e.activity[0].due
	}
	if e.retries != nil && len(e.retries.entries) > 0 {
		snapshot.NextRetryAt = e.retries.entries[0].Due
	}
	return snapshot
}

func (e *Engine) emptySnapshot(running bool) EngineSnapshot {
	return EngineSnapshot{
		Running: running, Generation: e.generation, ActiveLoops: int(e.activeLoops.Load()),
		QueueCapacity: e.workCapacity, RetryQueueCapacity: e.retryCapacity,
		InflightCapacity: e.inflightCapacity, CompletionQueueCapacity: e.commandCapacity,
		RelationshipLookback: MaxForwardRelationships,
		Classification:       e.evidence.Snapshot().Classification,
	}
}
