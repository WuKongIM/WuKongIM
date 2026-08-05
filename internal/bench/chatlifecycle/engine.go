package chatlifecycle

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const relationshipLogicalBase = uint64(1) << 62

const (
	maxActivityRouteScans = 64
	activityRouteDeferral = time.Nanosecond
	// completionFairnessQuantum bounds consecutive SEND work before the engine
	// yields one scheduler turn to session drains and rechecks completions.
	completionFairnessQuantum = 32
)

var (
	errEngineConfig     = errors.New("chat lifecycle engine: configuration is invalid")
	errEngineRunning    = errors.New("chat lifecycle engine: already running")
	errEngineNotRunning = errors.New("chat lifecycle engine: not running")
	errSchedulerClock   = errors.New("chat lifecycle scheduler: clock moved backwards or login credit overflowed")
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
	// WorkerID is this engine's zero-based worker partition.
	WorkerID uint64
	// WorkerCount must match the workload and identity partition count.
	WorkerCount uint64

	CommandCapacity   int
	WorkCapacity      int
	RetryCapacity     int
	InflightCapacity  int
	MaxWorkPerAdvance int
	AttemptTimeout    time.Duration
	// ActivityEligibilityWindow bounds how long a due mandatory initial or
	// revisit SEND may wait for an eligible online route.
	ActivityEligibilityWindow time.Duration
}

// EngineSnapshot is constant-size worker runtime evidence.
type EngineSnapshot struct {
	Running                 bool
	Generation              uint64
	WorkerID                uint64
	WorkerCount             uint64
	OnlineTarget            int
	ActiveLoops             int
	ActiveSteps             int
	Online                  int
	LoginStarting           int
	TrafficReady            int
	GatewayConnectLatency   WorkerHistogramSnapshot
	ConversationSyncLatency WorkerHistogramSnapshot
	QueueCurrent            int
	FutureCurrent           int
	ActivityCurrent         int
	ActivityUnderDelivered  uint64
	ActivityFutureCanceled  uint64
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
	ActiveHotChannels       int
	PendingHotChannels      int
	ColdEvidencePending     int
	LoginPlannedNew         uint64
	LoginPlannedReturning   uint64
	LoginAdmittedNew        uint64
	LoginAdmittedReturning  uint64
	LoginCompletedNew       uint64
	LoginCompletedReturning uint64
	LoginSkipped            uint64
	LoginReplacements       uint64
	SessionsExpired         uint64
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

// EngineStepSnapshot is one bounded orchestration result. Login counters
// distinguish requested shape from actual admission and completed sync.
type EngineStepSnapshot struct {
	PlannedNew         int
	PlannedReturning   int
	AdmittedNew        int
	AdmittedReturning  int
	CompletedNew       int
	CompletedReturning int
	LoginsCompleted    int
	BootstrapNew       int
	LoginsSkipped      int
	ReplacementLogins  int
	Expired            int
	Online             int
	Traffic            TrafficTickSnapshot
	Advanced           int
}

type sessionScheduler struct {
	workload                      WorkloadConfig
	workerID                      uint64
	workerCount                   uint64
	onlineTarget                  int
	lastStep                      time.Time
	credit                        uint64
	creditRemainder               uint64
	globalLoginTokens             uint64
	replacements                  uint64
	loginOrdinal                  uint64
	nextNewIndex                  uint64
	bootstrapping                 bool
	groupReturningOrdinal         uint64
	groupReturningCategoryOrdinal [4]uint64
}

type sessionSchedulerMetrics struct {
	plannedNew         atomic.Uint64
	plannedReturning   atomic.Uint64
	admittedNew        atomic.Uint64
	admittedReturning  atomic.Uint64
	completedNew       atomic.Uint64
	completedReturning atomic.Uint64
	skipped            atomic.Uint64
	replacements       atomic.Uint64
	expired            atomic.Uint64
}

func (m *sessionSchedulerMetrics) reset() {
	m.plannedNew.Store(0)
	m.plannedReturning.Store(0)
	m.admittedNew.Store(0)
	m.admittedReturning.Store(0)
	m.completedNew.Store(0)
	m.completedReturning.Store(0)
	m.skipped.Store(0)
	m.replacements.Store(0)
	m.expired.Store(0)
}

func (s *sessionScheduler) reset(now time.Time) {
	s.lastStep = now
	s.credit = 0
	s.creditRemainder = 0
	s.globalLoginTokens = 0
	s.replacements = 0
	s.loginOrdinal = 0
	s.bootstrapping = true
	s.groupReturningOrdinal = 0
	s.groupReturningCategoryOrdinal = [4]uint64{}
}

func (s *sessionScheduler) addReplacements(count uint64) {
	limit := uint64(s.onlineTarget)
	if count > limit-s.replacements {
		s.replacements = limit
		return
	}
	s.replacements += count
}

func (s *sessionScheduler) release(now time.Time) (int, error) {
	if now.Before(s.lastStep) {
		return 0, errSchedulerClock
	}
	elapsed := uint64(now.Sub(s.lastStep))
	s.lastStep = now
	numerator := uint64(s.workload.NewUsersPerDay) * distributionCycle
	denominator := uint64(secondsPerDay) * uint64(time.Second) * uint64(s.workload.Login.NewPercent)
	hi, lo := bits.Mul64(elapsed, numerator)
	if denominator == 0 || hi >= denominator {
		return 0, errSchedulerClock
	}
	globalWhole, remainder := bits.Div64(hi, lo, denominator)
	remainder += s.creditRemainder
	if remainder >= denominator {
		globalWhole++
		remainder -= denominator
	}
	s.creditRemainder = remainder
	nextGlobalTokens := s.globalLoginTokens + globalWhole
	if nextGlobalTokens < s.globalLoginTokens {
		return 0, errSchedulerClock
	}
	before, err := workerTokenCount(s.globalLoginTokens, s.workerID, s.workerCount)
	if err != nil {
		return 0, err
	}
	after, err := workerTokenCount(nextGlobalTokens, s.workerID, s.workerCount)
	if err != nil {
		return 0, err
	}
	s.globalLoginTokens = nextGlobalTokens
	localWhole := after - before
	localBurst, err := workerOnlineTarget(s.workload.MaxGlobalBurst, s.workerID, s.workerCount)
	if err != nil {
		return 0, err
	}
	burst := uint64(localBurst)
	if localWhole >= burst || s.credit >= burst-localWhole {
		s.credit = burst
	} else {
		s.credit += localWhole
	}
	due := s.credit
	if s.replacements > due {
		due = s.replacements
	}
	if due > uint64(s.onlineTarget) {
		due = uint64(s.onlineTarget)
	}
	return int(due), nil
}

// workerTokenCount returns how many ordinals in [0,total) belong to workerID
// under the stable ordinal modulo partition. It is exact for every prefix.
func workerTokenCount(total, workerID, workerCount uint64) (uint64, error) {
	if workerCount == 0 || workerCount > maxRateWorkers || workerID >= workerCount {
		return 0, errEngineConfig
	}
	count := total / workerCount
	if workerID < total%workerCount {
		count++
	}
	return count, nil
}

func (s *sessionScheduler) consumeOne() {
	if s.credit > 0 {
		s.credit--
	}
	if s.replacements > 0 {
		s.replacements--
	}
}

// nextGlobalLoginOrdinal advances this worker's local attempt sequence and
// maps it into the shared interleaved login schedule without shared state.
func (s *sessionScheduler) nextGlobalLoginOrdinal() (uint64, error) {
	hi, lo := bits.Mul64(s.loginOrdinal, s.workerCount)
	if hi != 0 || lo > math.MaxUint64-s.workerID {
		return 0, errEngineConfig
	}
	ordinal := lo + s.workerID
	s.loginOrdinal++
	return ordinal, nil
}

func (s *sessionScheduler) planLogin(
	sessions *SessionPool,
	graph RelationshipGraph,
	schedule ScheduleModel,
	catalog GroupCatalog,
	loginOrdinal uint64,
	kind LoginIdentity,
) (SessionLogin, LoginIdentity, ReturningCandidate, bool, error) {
	if kind == LoginNew || s.bootstrapping {
		globalIndex, err := graph.identity.GlobalIndex(s.workerID, s.nextNewIndex)
		if err != nil {
			return SessionLogin{}, kind, ReturningCandidate{}, false, err
		}
		uid := graph.identity.UID(globalIndex)
		return SessionLogin{UID: uid, UserIndex: globalIndex, LoginOrdinal: loginOrdinal, NewIdentity: true}, LoginNew, ReturningCandidate{}, true, nil
	}
	frontier, err := graph.identity.GlobalIndex(s.workerID, s.nextNewIndex)
	if err != nil {
		return SessionLogin{}, kind, ReturningCandidate{}, false, err
	}
	for probe := uint64(0); probe < 64; probe++ {
		if probe > (math.MaxUint64-loginOrdinal)/distributionCycle {
			return SessionLogin{}, kind, ReturningCandidate{}, false, errEngineConfig
		}
		candidateOrdinal := loginOrdinal + probe*distributionCycle
		candidate, err := graph.ReturningCandidate(schedule, frontier, candidateOrdinal, uint64(schedule.newUsersPerDay))
		if err != nil {
			return SessionLogin{}, kind, ReturningCandidate{}, false, err
		}
		if !candidate.Available {
			continue
		}
		candidateWorker, _ := graph.identity.Owner(candidate.UserIndex)
		if candidateWorker != s.workerID {
			continue
		}
		if candidate.ActualBucket == HistoryOlder {
			_, older := returningCandidateRanges(frontier, uint64(schedule.newUsersPerDay))
			member, memberOK, memberErr := s.nextGroupReturningMember(catalog, older.min, older.max)
			if memberErr != nil {
				return SessionLogin{}, kind, ReturningCandidate{}, false, memberErr
			}
			if memberOK {
				rosterCandidate, rosterErr := graph.returningCandidateAt(
					schedule, frontier, candidateOrdinal, uint64(schedule.newUsersPerDay), member.UserIndex,
				)
				if rosterErr != nil {
					return SessionLogin{}, kind, ReturningCandidate{}, false, rosterErr
				}
				rosterWorker, _ := graph.identity.Owner(rosterCandidate.UserIndex)
				if rosterCandidate.Available && rosterWorker == s.workerID && rosterCandidate.ActualBucket == HistoryOlder && !sessions.isOwned(rosterCandidate.UserUID) {
					return SessionLogin{
						UID: rosterCandidate.UserUID, UserIndex: rosterCandidate.UserIndex, LoginOrdinal: loginOrdinal,
					}, LoginReturning, rosterCandidate, true, nil
				}
			}
		}
		if sessions.isOwned(candidate.UserUID) {
			continue
		}
		return SessionLogin{
			UID: candidate.UserUID, UserIndex: candidate.UserIndex, LoginOrdinal: loginOrdinal,
		}, LoginReturning, candidate, true, nil
	}
	return SessionLogin{}, kind, ReturningCandidate{}, false, nil
}

func (s *sessionScheduler) nextGroupReturningMember(catalog GroupCatalog, minimum, maximum uint64) (GroupReturningMember, bool, error) {
	if s.groupReturningOrdinal == math.MaxUint64 {
		return GroupReturningMember{}, false, errEngineConfig
	}
	position := s.groupReturningOrdinal % 100
	s.groupReturningOrdinal++
	categoryIndex := 3
	switch {
	case position < 80:
		categoryIndex = 0
	case position < 95:
		categoryIndex = 1
	case position < 99:
		categoryIndex = 2
	}
	for probe := 0; probe < len(catalog.counts); probe++ {
		candidate := (categoryIndex + probe) % len(catalog.counts)
		if catalog.counts[candidate] == 0 {
			continue
		}
		ordinal := s.groupReturningCategoryOrdinal[candidate]
		if ordinal == math.MaxUint64 {
			return GroupReturningMember{}, false, errEngineConfig
		}
		s.groupReturningCategoryOrdinal[candidate]++
		return catalog.ReturningMemberForWorker(GroupCategory(candidate+1), ordinal, minimum, maximum, s.workerID)
	}
	return GroupReturningMember{}, false, errEngineConfig
}

type engineWorkKind uint8

const (
	engineWorkSend engineWorkKind = iota + 1
	engineWorkTimeout
	engineWorkLifecycle
)

type engineWork struct {
	due                 time.Time
	eligibilityDeadline time.Time
	kind                engineWorkKind
	intent              TrafficIntent
	attempt             uint8
	clientSeq           uint64
	order               uint64
	index               int
	edge                RelationshipEdge
	schedule            ChannelSchedule
	relationshipOrdinal uint64
	coldConfirmed       bool
	requiredSender      string
	offered             bool
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
	intent           TrafficIntent
	attempt          uint8
	currentClientSeq uint64
	clientSeqs       [maxSendAttemptIdentities]uint64
	clientSeqCount   uint8
	retryScheduled   bool
	timeout          *engineWork
}

func (i *engineInflight) registerClientSeq(clientSeq uint64) bool {
	if i == nil || clientSeq == 0 || i.clientSeqCount >= uint8(maxSendAttemptIdentities) || i.hasClientSeq(clientSeq) {
		return false
	}
	i.clientSeqs[i.clientSeqCount] = clientSeq
	i.clientSeqCount++
	return true
}

func (i *engineInflight) hasClientSeq(clientSeq uint64) bool {
	if i == nil || clientSeq == 0 {
		return false
	}
	for index := uint8(0); index < i.clientSeqCount; index++ {
		if i.clientSeqs[index] == clientSeq {
			return true
		}
	}
	return false
}

type engineActiveChannel struct {
	edge      RelationshipEdge
	direction PersonDirection
}

type enginePendingChannel struct {
	active    engineActiveChannel
	lifecycle *engineWork
}

type engineCommand struct {
	run func()
}

type engineCompletion struct {
	ack             *frame.SendackPacket
	verificationErr error
	clientSeq       uint64
	clientMsgNo     string
}

type engineLoginResult struct {
	login            SessionLogin
	kind             LoginIdentity
	candidate        ReturningCandidate
	ordinal          uint64
	globalNewOrdinal uint64
	replacement      bool
	err              error
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
	clock        SessionClock
	sessions     *SessionPool
	schedule     ScheduleModel
	graph        RelationshipGraph
	traffic      TrafficModel
	generator    *TrafficGenerator
	retry        RetryPolicy
	verifier     *Verifier
	evidence     *EvidenceRecorder
	workerID     uint64
	workers      uint64
	onlineTarget int

	commandCapacity           int
	workCapacity              int
	retryCapacity             int
	inflightCapacity          int
	maxWork                   int
	attemptTimeout            time.Duration
	activityEligibilityWindow time.Duration

	lifecycleMu      sync.Mutex
	stepMu           sync.Mutex
	running          bool
	accepting        bool
	stopping         bool
	generation       uint64
	commands         chan engineCommand
	completions      chan engineCompletion
	loginResults     chan engineLoginResult
	stop             chan struct{}
	done             chan struct{}
	generationCtx    context.Context
	generationCancel context.CancelFunc
	cached           EngineSnapshot
	sessionOps       sync.WaitGroup
	stepOps          sync.WaitGroup
	loginOps         sync.WaitGroup
	scheduler        sessionScheduler
	schedulerMetrics sessionSchedulerMetrics

	activeLoops       atomic.Int64
	activeSteps       atomic.Int64
	commandSaturation atomic.Uint64

	// The fields below are owned exclusively by the active command loop.
	work                   engineWorkHeap
	activity               engineWorkHeap
	retries                *RetryScheduler
	inflight               map[string]*engineInflight
	workPeak               int
	queuedSends            int
	inflightPeak           int
	nextOrder              uint64
	nextClientSeq          uint64
	activeLifecycleTimers  int
	lifecycleByChannel     map[string]*engineWork
	activeChannels         []engineActiveChannel
	activePosition         map[string]int
	pendingChannels        []enginePendingChannel
	pendingPosition        map[string]int
	activeCursor           uint64
	retryAttempts          uint64
	finalFailures          uint64
	harnessInvalid         uint64
	activityUnderDelivered uint64
	activityFutureCanceled uint64
	now                    time.Time
}

// NewEngine wires the existing deterministic models and bounded verifier.
func NewEngine(config EngineConfig) (*Engine, error) {
	if config.Clock == nil || config.Sessions == nil || config.Schedule.identity == nil ||
		config.Graph.identity == nil || config.Traffic.identity == nil || config.Generator == nil ||
		config.Retry.identity == nil || config.Verifier == nil || config.Evidence == nil ||
		config.CommandCapacity <= 0 || config.WorkCapacity <= 0 || config.RetryCapacity <= 0 ||
		config.InflightCapacity <= 0 || config.MaxWorkPerAdvance <= 0 || config.AttemptTimeout <= 0 || config.ActivityEligibilityWindow <= 0 ||
		config.CommandCapacity > maxVerifierCapacity || config.WorkCapacity > maxVerifierCapacity ||
		config.RetryCapacity > maxVerifierCapacity || config.InflightCapacity > maxVerifierCapacity ||
		config.WorkerCount == 0 || config.WorkerCount != config.Schedule.identity.Workers() ||
		config.WorkerID >= config.WorkerCount {
		return nil, errEngineConfig
	}
	identity := config.Schedule.identity
	if config.Graph.identity != identity || config.Traffic.identity != identity || config.Retry.identity != identity ||
		config.Generator.identity != identity || config.Sessions.identity != identity || config.Sessions.catalog != config.Generator.catalog {
		return nil, errEngineConfig
	}
	if config.Generator.workerID != config.WorkerID || config.Generator.workers != config.WorkerCount {
		return nil, errEngineConfig
	}
	onlineTarget, err := workerOnlineTarget(config.Generator.workload.OnlineUsers, config.WorkerID, config.WorkerCount)
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		clock: config.Clock, sessions: config.Sessions, schedule: config.Schedule, graph: config.Graph,
		traffic: config.Traffic, generator: config.Generator, retry: config.Retry,
		verifier: config.Verifier, evidence: config.Evidence,
		workerID: config.WorkerID, workers: config.WorkerCount, onlineTarget: onlineTarget,
		commandCapacity: config.CommandCapacity, workCapacity: config.WorkCapacity,
		retryCapacity: config.RetryCapacity, inflightCapacity: config.InflightCapacity,
		maxWork: config.MaxWorkPerAdvance, attemptTimeout: config.AttemptTimeout,
		activityEligibilityWindow: config.ActivityEligibilityWindow,
		scheduler: sessionScheduler{
			workload: config.Generator.workload, workerID: config.WorkerID,
			workerCount: config.WorkerCount, onlineTarget: onlineTarget, bootstrapping: true,
		},
	}
	if err := config.Sessions.setEngineObservers(engine.sessionSendack, engine.sessionAsyncSendError); err != nil {
		return nil, err
	}
	engine.cached = engine.emptySnapshot(false)
	return engine, nil
}

func workerOnlineTarget(total int, workerID, workerCount uint64) (int, error) {
	if total <= 0 || workerCount == 0 || workerCount > maxRateWorkers || workerID >= workerCount {
		return 0, errEngineConfig
	}
	target := total / int(workerCount)
	if workerID < uint64(total%int(workerCount)) {
		target++
	}
	return target, nil
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
	nextGeneration := e.generation + 1
	if nextGeneration == 0 || nextGeneration > maxLogicalGeneration {
		return errEngineConfig
	}
	if err := e.generator.reset(e.clock.Now(), nextGeneration); err != nil {
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
	e.activeChannels = nil
	e.activePosition = make(map[string]int)
	e.pendingChannels = nil
	e.pendingPosition = make(map[string]int)
	e.activeCursor = 0
	e.workPeak = 0
	e.queuedSends = 0
	e.inflightPeak = 0
	e.nextOrder = 0
	e.nextClientSeq = 0
	e.activeLifecycleTimers = 0
	e.retryAttempts = 0
	e.finalFailures = 0
	e.harnessInvalid = 0
	e.activityUnderDelivered = 0
	e.activityFutureCanceled = 0
	e.commandSaturation.Store(0)
	e.now = e.clock.Now()
	e.scheduler.reset(e.now)
	e.schedulerMetrics.reset()
	e.commands = make(chan engineCommand, e.commandCapacity)
	e.completions = make(chan engineCompletion, e.commandCapacity)
	e.loginResults = make(chan engineLoginResult, e.sessions.startingCapacity)
	e.stop = make(chan struct{})
	e.done = make(chan struct{})
	e.generationCtx, e.generationCancel = context.WithCancel(ctx)
	e.generation = nextGeneration
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
	generationCancel := e.generationCancel
	stop := e.stop
	done := e.done
	e.lifecycleMu.Unlock()

	generationCancel()
	e.stepOps.Wait()
	e.loginOps.Wait()
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
	generationCtx, ok := e.beginSessionOp()
	if !ok {
		return SessionSnapshot{}, errEngineNotRunning
	}
	defer e.sessionOps.Done()
	loginCtx, cancel := mergeGenerationContext(generationCtx, ctx)
	defer cancel()
	return e.sessions.login(loginCtx, generationCtx, login)
}

// Logout applies the joined session boundary inside the active generation.
func (e *Engine) Logout(uid string) error {
	if _, ok := e.beginSessionOp(); !ok {
		return errEngineNotRunning
	}
	defer e.sessionOps.Done()
	return e.sessions.Logout(uid)
}

func (e *Engine) beginSessionOp() (context.Context, bool) {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || !e.accepting {
		return nil, false
	}
	e.sessionOps.Add(1)
	return e.generationCtx, true
}

func (e *Engine) beginStep() (context.Context, bool) {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if !e.running || !e.accepting {
		return nil, false
	}
	e.stepOps.Add(1)
	e.activeSteps.Add(1)
	return e.generationCtx, true
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
			var routeErr error
			if intent.Kind == TrafficPerson {
				intent, routeErr = e.routePersonGrant(intent, now)
			} else {
				intent, routeErr = e.routeGroupGrant(intent)
			}
			if routeErr != nil {
				return routeErr
			}
			return e.addSendWork(intent, 0, now)
		})
		if tickErr == nil {
			if canary, due, canaryErr := e.generator.NextCanary(now); canaryErr != nil {
				tickErr = canaryErr
			} else if due {
				if canary, canaryErr = e.routeGroupGrant(canary); canaryErr == nil {
					tickErr = e.addSendWork(canary, 0, now)
				} else {
					tickErr = canaryErr
				}
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

// ObserveNewUser resolves the identity's immutable global new-user ordinal and
// publishes every newly real incoming or outgoing relationship exactly once.
func (e *Engine) ObserveNewUser(userIndex uint64) (considered, activated int, err error) {
	workerID, localNewIndex := e.graph.identity.Owner(userIndex)
	globalNewOrdinal, err := e.schedule.GlobalNewOrdinalFor(workerID, localNewIndex)
	if err != nil {
		return 0, 0, err
	}
	return e.observeNewUser(userIndex, globalNewOrdinal)
}

// ObserveNewUserForOrdinal schedules one planned LoginNew completion using its
// plan-time global new-user ordinal, independent of startup completion order.
func (e *Engine) ObserveNewUserForOrdinal(userIndex, globalNewOrdinal uint64) (considered, activated int, err error) {
	return e.observeNewUser(userIndex, globalNewOrdinal)
}

func (e *Engine) observeNewUser(userIndex, globalNewOrdinal uint64) (considered, activated int, err error) {
	response := make(chan struct {
		considered int
		activated  int
		err        error
	}, 1)
	if enqueueErr := e.enqueue(engineCommand{run: func() {
		result := struct {
			considered int
			activated  int
			err        error
		}{}
		workerID, localNewIndex := e.graph.identity.Owner(userIndex)
		resolvedOrdinal, resolveErr := e.schedule.GlobalNewOrdinalFor(workerID, localNewIndex)
		if resolveErr != nil || resolvedOrdinal != globalNewOrdinal {
			result.err = errors.Join(resolveErr, errScheduleNewOrdinal)
			response <- result
			return
		}
		online, alreadyObserved := e.sessions.relationshipObservation(userIndex)
		if !online || alreadyObserved {
			response <- result
			return
		}

		incoming, incomingErr := e.graph.IncomingForSchedule(e.schedule, userIndex)
		if incomingErr != nil {
			result.err = incomingErr
			response <- result
			return
		}
		for edgeIndex := 0; edgeIndex < incoming.Count; edgeIndex++ {
			edge := incoming.Items[edgeIndex]
			_, ownerObserved := e.sessions.relationshipObservation(edge.OwnerIndex)
			if !ownerObserved {
				continue
			}
			_, ownerLocalNewIndex := e.graph.identity.Owner(edge.OwnerIndex)
			ownerGlobalNewOrdinal, ordinalErr := e.schedule.GlobalNewOrdinalFor(workerID, ownerLocalNewIndex)
			distance := localNewIndex - ownerLocalNewIndex
			if ordinalErr != nil || ownerGlobalNewOrdinal > (math.MaxUint64-(distance-1))/MaxForwardRelationships {
				result.err = errors.Join(ordinalErr, errEngineConfig)
				break
			}
			ordinal := ownerGlobalNewOrdinal*MaxForwardRelationships + distance - 1
			result.considered++
			wasActivated, activationErr := e.activateRelationship(edge, ordinal)
			if activationErr != nil {
				result.err = activationErr
				break
			}
			if wasActivated {
				result.activated++
			}
		}
		if result.err == nil {
			outgoing, outgoingErr := e.graph.OutgoingForOrdinal(userIndex, globalNewOrdinal)
			if outgoingErr != nil {
				result.err = outgoingErr
			} else {
				for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
					edge := outgoing.Items[edgeIndex]
					_, peerObserved := e.sessions.relationshipObservation(edge.PeerIndex)
					if !peerObserved {
						continue
					}
					if globalNewOrdinal > (math.MaxUint64-uint64(edgeIndex))/MaxForwardRelationships {
						result.err = errEngineConfig
						break
					}
					ordinal := globalNewOrdinal*MaxForwardRelationships + uint64(edgeIndex)
					result.considered++
					wasActivated, activationErr := e.activateRelationship(edge, ordinal)
					if activationErr != nil {
						result.err = activationErr
						break
					}
					if wasActivated {
						result.activated++
					}
				}
			}
		}
		if result.err == nil {
			e.sessions.markRelationshipsObserved(userIndex)
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

func (e *Engine) scheduleReturningCandidate(candidate ReturningCandidate, loginOrdinal uint64, now time.Time) error {
	response := make(chan error, 1)
	if err := e.enqueue(engineCommand{run: func() {
		if candidate.ConversationCount <= 0 || candidate.ConversationCount > len(candidate.Conversations) {
			response <- errEngineConfig
			return
		}
		if candidate.ConversationCount > e.workCapacity-e.futureCount() {
			response <- e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
			return
		}
		for index := 0; index < candidate.ConversationCount; index++ {
			conversation := candidate.Conversations[index]
			ownerIndex, peerIndex := candidate.UserIndex, conversation.PeerIndex
			if ownerIndex > peerIndex {
				ownerIndex, peerIndex = peerIndex, ownerIndex
			}
			edge := e.graph.edge(ownerIndex, peerIndex)
			if edge.PersonChannelID != conversation.PersonChannelID {
				response <- errEngineConfig
				return
			}
			if e.lifecycleByChannel[edge.PersonChannelID] != nil {
				continue
			}
			delay, err := e.schedule.durationInRange(
				"returning-login-revisit-delay/v1", minimumRevisitDelay, maximumRevisitDelay,
				loginOrdinal, candidate.UserIndex, uint64(index),
			)
			if err != nil {
				response <- err
				return
			}
			messages, err := e.schedule.intInRange(
				"returning-login-revisit-message-count/v1", e.schedule.relationship.ReturningMessages,
				loginOrdinal, candidate.UserIndex, uint64(index),
			)
			if err != nil {
				response <- err
				return
			}
			work := &engineWork{
				due: now.Add(delay), kind: engineWorkLifecycle, edge: edge,
				schedule: ChannelSchedule{
					Class: LifecycleRevisit, RevisitMessages: messages,
					RequiresColdRuntimeEvidence: true, NaturalCooling: true,
				},
				relationshipOrdinal: loginOrdinal*2 + uint64(index),
				requiredSender:      candidate.UserUID,
			}
			deadline, err := e.newEligibilityDeadline(work.due)
			if err != nil {
				response <- err
				return
			}
			work.eligibilityDeadline = deadline
			if err := e.addWork(work); err != nil {
				response <- err
				return
			}
			e.lifecycleByChannel[edge.PersonChannelID] = work
			e.activeLifecycleTimers++
		}
		response <- nil
	}}); err != nil {
		return err
	}
	return <-response
}

// Advance processes due heaps with a fixed CPU work budget.
func (e *Engine) Advance(now time.Time) (int, error) {
	generationCtx, ok := e.beginSessionOp()
	if !ok {
		return 0, errEngineNotRunning
	}
	defer e.sessionOps.Done()
	e.sessions.Expire(now)
	return e.advanceWithContext(generationCtx, now)
}

func (e *Engine) advanceWithContext(ctx context.Context, now time.Time) (int, error) {
	response := make(chan advanceResult, 1)
	if err := e.enqueue(engineCommand{run: func() { response <- e.advance(ctx, now) }}); err != nil {
		return 0, err
	}
	result := <-response
	return result.processed, result.err
}

// Step is the narrow worker-run boundary: it advances bounded session
// scheduling, optional primary traffic, and due engine work for one fake or
// real clock instant. Control-plane code does not assemble private schedulers.
func (e *Engine) Step(ctx context.Context, now time.Time, demand []uint64) (EngineStepSnapshot, error) {
	if e == nil {
		return EngineStepSnapshot{}, errEngineConfig
	}
	generationCtx, ok := e.beginStep()
	if !ok {
		return EngineStepSnapshot{}, errEngineNotRunning
	}
	defer func() {
		e.activeSteps.Add(-1)
		e.stepOps.Done()
	}()
	stepCtx, cancel := mergeGenerationContext(generationCtx, ctx)
	defer cancel()
	e.stepMu.Lock()
	defer e.stepMu.Unlock()
	if err := stepCtx.Err(); err != nil {
		return EngineStepSnapshot{}, err
	}

	result := EngineStepSnapshot{}
	resultErr := e.drainLoginResults(now, &result)
	expired := e.sessions.Expire(now)
	e.schedulerMetrics.expired.Add(uint64(expired))
	result.Expired = expired
	replacementCount := uint64(expired)
	poolBeforeLogins := e.sessions.Counts()
	if !e.scheduler.bootstrapping && poolBeforeLogins.Online+poolBeforeLogins.Starting < e.onlineTarget {
		shortage := uint64(e.onlineTarget - poolBeforeLogins.Online - poolBeforeLogins.Starting)
		if shortage > replacementCount {
			replacementCount = shortage
		}
	}
	e.scheduler.addReplacements(replacementCount)
	loginBudget, err := e.scheduler.release(now)
	if err != nil {
		return EngineStepSnapshot{}, err
	}
	startingSlots := e.sessions.startingCapacity - poolBeforeLogins.Starting
	scheduled := 0
	for loginBudget > 0 && scheduled < e.maxWork && startingSlots > 0 {
		if poolBeforeLogins.Online+poolBeforeLogins.Starting+scheduled >= e.onlineTarget {
			break
		}
		ordinal, ordinalErr := e.scheduler.nextGlobalLoginOrdinal()
		if ordinalErr != nil {
			return result, ordinalErr
		}
		loginSchedule, scheduleErr := e.schedule.Login(ordinal)
		if scheduleErr != nil {
			return result, scheduleErr
		}
		identityKind := loginSchedule.Identity
		if identityKind == LoginNew {
			result.PlannedNew++
			e.schedulerMetrics.plannedNew.Add(1)
		} else {
			result.PlannedReturning++
			e.schedulerMetrics.plannedReturning.Add(1)
		}
		login, actualKind, candidate, available, planErr := e.scheduler.planLogin(e.sessions, e.graph, e.schedule, e.generator.catalog, ordinal, identityKind)
		if planErr != nil {
			return result, planErr
		}
		wasReplacement := e.scheduler.replacements > 0
		e.scheduler.consumeOne()
		loginBudget--
		if !available {
			result.LoginsSkipped++
			e.schedulerMetrics.skipped.Add(1)
			scheduled++
			continue
		}
		globalNewOrdinal := uint64(0)
		if actualKind == LoginNew {
			workerID, localNewIndex := e.graph.identity.Owner(login.UserIndex)
			globalNewOrdinal, planErr = e.schedule.GlobalNewOrdinalFor(workerID, localNewIndex)
			if planErr != nil {
				return result, planErr
			}
			login.NewIdentity = true
		}
		if reserveErr := e.sessions.reserveLogin(login.UID); reserveErr != nil {
			result.LoginsSkipped++
			e.schedulerMetrics.skipped.Add(1)
			resultErr = errors.Join(resultErr, reserveErr)
			scheduled++
			continue
		}
		if identityKind == LoginReturning && actualKind == LoginNew {
			result.BootstrapNew++
		}
		if actualKind == LoginNew {
			result.AdmittedNew++
			e.schedulerMetrics.admittedNew.Add(1)
			e.scheduler.nextNewIndex++
		} else {
			result.AdmittedReturning++
			e.schedulerMetrics.admittedReturning.Add(1)
		}
		completion := engineLoginResult{
			login: login, kind: actualKind, candidate: candidate, ordinal: ordinal,
			globalNewOrdinal: globalNewOrdinal, replacement: wasReplacement,
		}
		e.startScheduledLogin(generationCtx, completion)
		scheduled++
		startingSlots--
	}
	poolAfterLogins := e.sessions.Counts()
	if e.scheduler.bootstrapping && poolAfterLogins.Online >= e.onlineTarget {
		e.scheduler.bootstrapping = false
		e.scheduler.credit = 0
		e.scheduler.replacements = 0
	}
	if scheduled >= e.maxWork &&
		poolAfterLogins.Online+poolAfterLogins.Starting < e.onlineTarget &&
		(e.scheduler.credit > 0 || e.scheduler.replacements > 0) {
		result.Online = poolAfterLogins.Online
		return result, e.recordRuntimeFailureSync(RuntimeFailureSchedulerCPUSaturated, uint64(e.maxWork))
	}
	if demand != nil {
		traffic, tickErr := e.Tick(now, demand)
		result.Traffic = traffic
		if tickErr != nil {
			resultErr = errors.Join(resultErr, tickErr)
		}
	}
	advanced, advanceErr := e.advanceWithContext(stepCtx, now)
	result.Advanced = advanced
	resultErr = errors.Join(resultErr, advanceErr)
	resultErr = errors.Join(resultErr, e.drainLoginResults(now, &result))
	result.Online = e.sessions.Counts().Online
	return result, resultErr
}

func (e *Engine) startScheduledLogin(generationCtx context.Context, result engineLoginResult) {
	e.loginOps.Add(1)
	go func() {
		defer e.loginOps.Done()
		_, result.err = e.sessions.loginReserved(generationCtx, generationCtx, result.login)
		select {
		case e.loginResults <- result:
		case <-generationCtx.Done():
		}
	}()
}

func (e *Engine) drainLoginResults(now time.Time, snapshot *EngineStepSnapshot) error {
	var result error
	for processed := 0; processed < e.maxWork; processed++ {
		select {
		case completion := <-e.loginResults:
			if completion.err != nil {
				snapshot.LoginsSkipped++
				e.schedulerMetrics.skipped.Add(1)
				result = errors.Join(result, completion.err)
				continue
			}
			snapshot.LoginsCompleted++
			if completion.kind == LoginNew {
				snapshot.CompletedNew++
				e.schedulerMetrics.completedNew.Add(1)
				_, _, observeErr := e.observeNewUser(completion.login.UserIndex, completion.globalNewOrdinal)
				result = errors.Join(result, observeErr)
			} else {
				snapshot.CompletedReturning++
				e.schedulerMetrics.completedReturning.Add(1)
				result = errors.Join(result, e.scheduleReturningCandidate(completion.candidate, completion.ordinal, now))
			}
			if completion.replacement {
				snapshot.ReplacementLogins++
				e.schedulerMetrics.replacements.Add(1)
			}
		default:
			return result
		}
	}
	return result
}

func mergeGenerationContext(generation, caller context.Context) (context.Context, context.CancelFunc) {
	if caller == nil {
		caller = context.Background()
	}
	ctx, cancel := context.WithCancel(generation)
	stopCaller := context.AfterFunc(caller, cancel)
	return ctx, func() {
		stopCaller()
		cancel()
	}
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

// ScheduleRate serializes a control-plane rate update with the generator's
// sole engine owner. The change takes effect on the next traffic tick.
func (e *Engine) ScheduleRate(rate, burst uint64) error {
	if e == nil {
		return errEngineConfig
	}
	response := make(chan error, 1)
	if err := e.enqueueBlocking(engineCommand{run: func() {
		response <- e.generator.allocator.ScheduleRate(rate, burst)
	}}); err != nil {
		return err
	}
	return <-response
}

// EngineWorkerRuntimeSnapshot is one command-serialized engine and generator
// projection used by consistent worker checkpoints.
type EngineWorkerRuntimeSnapshot struct {
	Engine    EngineSnapshot
	Generated TrafficGeneratorSnapshot
}

// WorkerRuntimeSnapshot serializes engine and generator evidence in one owner
// command so a concurrent Step cannot advance between the two projections.
func (e *Engine) WorkerRuntimeSnapshot() (EngineWorkerRuntimeSnapshot, error) {
	if e == nil {
		return EngineWorkerRuntimeSnapshot{}, errEngineConfig
	}
	e.lifecycleMu.Lock()
	running := e.running
	cached := e.cached
	e.lifecycleMu.Unlock()
	if !running {
		return EngineWorkerRuntimeSnapshot{Engine: cached, Generated: e.generator.Snapshot()}, nil
	}
	response := make(chan EngineWorkerRuntimeSnapshot, 1)
	if err := e.enqueueBlocking(engineCommand{run: func() {
		e.drainCompletions()
		response <- EngineWorkerRuntimeSnapshot{Engine: e.buildSnapshot(true), Generated: e.generator.Snapshot()}
	}}); err != nil {
		return EngineWorkerRuntimeSnapshot{}, err
	}
	return <-response, nil
}

func (e *Engine) loop(commands <-chan engineCommand, stop <-chan struct{}, done chan<- struct{}) {
	defer func() {
		e.cleanupInflight()
		e.cleanupPendingActivities()
		e.work = nil
		e.activity = nil
		e.queuedSends = 0
		if e.retries != nil {
			e.retries.entries = nil
			e.retries.byMessage = nil
		}
		e.activeLifecycleTimers = 0
		e.lifecycleByChannel = nil
		e.activeChannels = nil
		e.activePosition = nil
		e.pendingChannels = nil
		e.pendingPosition = nil
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
			e.observeCompletion(completion)
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
	e.enqueueSessionCompletion(engineCompletion{ack: ack, verificationErr: verificationErr})
}

func (e *Engine) sessionAsyncSendError(_ string, clientSeq uint64, clientMsgNo string) {
	e.enqueueSessionCompletion(engineCompletion{clientSeq: clientSeq, clientMsgNo: clientMsgNo})
}

func (e *Engine) enqueueSessionCompletion(completion engineCompletion) {
	e.lifecycleMu.Lock()
	if !e.running {
		e.lifecycleMu.Unlock()
		return
	}
	completions := e.completions
	stop := e.stop
	e.lifecycleMu.Unlock()
	select {
	case completions <- completion:
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
	if work == nil || work.kind != engineWorkSend {
		return errEngineConfig
	}
	if work.eligibilityDeadline.IsZero() {
		deadline, err := e.newEligibilityDeadline(work.due)
		if err != nil {
			return err
		}
		work.eligibilityDeadline = deadline
	}
	if !work.eligibilityDeadline.After(work.due) {
		return errEngineConfig
	}
	if e.futureCount() >= e.workCapacity {
		return e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	work.order = e.nextOrder
	e.nextOrder++
	heap.Push(&e.activity, work)
	e.observeWorkPeak()
	return nil
}

func (e *Engine) newEligibilityDeadline(due time.Time) (time.Time, error) {
	if due.UnixNano() > math.MaxInt64-int64(e.activityEligibilityWindow) {
		return time.Time{}, errEngineConfig
	}
	deadline := due.Add(e.activityEligibilityWindow)
	if !deadline.After(due) {
		return time.Time{}, errEngineConfig
	}
	return deadline, nil
}

func (e *Engine) futureCount() int { return len(e.work) + len(e.activity) }

func (e *Engine) observeWorkPeak() {
	if current := e.futureCount(); current > e.workPeak {
		e.workPeak = current
	}
}

func (e *Engine) advance(ctx context.Context, now time.Time) advanceResult {
	e.now = now
	e.verifier.ExpireCorrelations(now)
	e.drainCompletions()
	var result advanceResult
	sentWorkSinceYield := 0
	for result.processed < e.maxWork {
		workDue := len(e.work) > 0 && !e.work[0].due.After(now)
		retryDue := e.retries != nil && e.retries.due(now)
		if !workDue && !retryDue {
			break
		}
		processedSend := false
		if retryDue && (!workDue || !e.work[0].due.Before(e.retries.entries[0].Due)) {
			retries := e.retries.PopDue(now, 1)
			if len(retries) == 1 {
				processedSend = true
				result.err = errors.Join(result.err, e.processAttempt(ctx, retries[0].Intent, retries[0].Attempt.Attempt, now))
			}
		} else {
			work := heap.Pop(&e.work).(*engineWork)
			if work.kind == engineWorkSend {
				e.queuedSends--
				processedSend = true
			}
			result.err = errors.Join(result.err, e.processWork(ctx, work, now))
		}
		result.processed++
		e.drainCompletions()
		if processedSend {
			sentWorkSinceYield++
		}
		if sentWorkSinceYield >= completionFairnessQuantum {
			if len(e.inflight) > 0 {
				runtime.Gosched()
				e.drainCompletions()
			}
			sentWorkSinceYield = 0
		}
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
			e.observeCompletion(completion)
		default:
			return
		}
	}
}

func (e *Engine) observeCompletion(completion engineCompletion) {
	switch {
	case completion.ack != nil:
		_ = e.observeSendack(completion.ack, completion.verificationErr)
	case completion.clientMsgNo != "":
		_ = e.verifier.ResolveAttemptError(completion.clientMsgNo, completion.clientSeq)
		inflight := e.inflight[completion.clientMsgNo]
		if inflight != nil && inflight.currentClientSeq == completion.clientSeq {
			_ = e.scheduleRetry(inflight, e.clock.Now())
		}
	}
}

func (e *Engine) processWork(ctx context.Context, work *engineWork, now time.Time) error {
	switch work.kind {
	case engineWorkSend:
		return e.processAttempt(ctx, work.intent, work.attempt, now)
	case engineWorkTimeout:
		inflight := e.inflight[work.intent.Logical.ClientMsgNo]
		if inflight == nil || inflight.attempt != work.attempt || inflight.currentClientSeq != work.clientSeq {
			return nil
		}
		inflight.timeout = nil
		return e.scheduleRetry(inflight, now)
	case engineWorkLifecycle:
		if work.schedule.Class != LifecycleRevisit {
			e.completeLifecycleTimer(work, now)
			return nil
		}
		if work.schedule.RequiresColdRuntimeEvidence && !work.coldConfirmed {
			e.completeLifecycleTimer(work, now)
			return nil
		}
		if work.eligibilityDeadline.IsZero() {
			return errEngineConfig
		}
		if !work.eligibilityDeadline.After(now) {
			return e.expireLifecycleWork(work, now)
		}
		sender := work.requiredSender
		if sender != "" {
			if !e.sessions.IsOnline(sender) {
				return e.deferLifecycleWork(work, now)
			}
		} else {
			ownerOnline := e.sessions.IsOnline(work.edge.OwnerUID)
			peerOnline := e.sessions.IsOnline(work.edge.PeerUID)
			switch {
			case !ownerOnline && !peerOnline:
				return e.deferLifecycleWork(work, now)
			case ownerOnline && !peerOnline:
				sender = work.edge.OwnerUID
			case !ownerOnline && peerOnline:
				sender = work.edge.PeerUID
			}
		}
		e.completeLifecycleTimer(work, now)
		return e.scheduleRelationshipMessagesFrom(
			work.edge, work.relationshipOrdinal, 8, work.schedule.RevisitMessages,
			work.due, work.schedule.InitialBurst.Window, sender,
		)
	default:
		return errEngineConfig
	}
}

func (e *Engine) deferLifecycleWork(work *engineWork, now time.Time) error {
	if work == nil || work.eligibilityDeadline.IsZero() {
		return errEngineConfig
	}
	if !work.eligibilityDeadline.After(now) {
		return e.expireLifecycleWork(work, now)
	}
	deferred := now.Add(activityRouteDeferral)
	if !deferred.After(now) {
		return errEngineConfig
	}
	if !deferred.Before(work.eligibilityDeadline) {
		return e.expireLifecycleWork(work, now)
	}
	work.due = deferred
	return e.addWork(work)
}

func (e *Engine) expireLifecycleWork(work *engineWork, now time.Time) error {
	e.completeLifecycleTimer(work, now)
	e.activityUnderDelivered++
	return e.recordRuntimeFailure(RuntimeFailureUnderDelivery, 1)
}

func (e *Engine) completeLifecycleTimer(work *engineWork, now time.Time) {
	if e.lifecycleByChannel[work.edge.PersonChannelID] == work {
		delete(e.lifecycleByChannel, work.edge.PersonChannelID)
	}
	removedActive := false
	if work.schedule.Class == LifecycleRotating || work.schedule.Class == LifecycleLong {
		removedActive = e.removeActiveChannel(work.edge.PersonChannelID)
		e.removePendingChannel(work.edge.PersonChannelID)
	}
	if e.activeLifecycleTimers > 0 {
		e.activeLifecycleTimers--
	}
	if removedActive {
		e.promotePendingChannels(now)
	}
}

func (e *Engine) processAttempt(ctx context.Context, intent TrafficIntent, attempt uint8, now time.Time) error {
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
	if intent.Packet == nil || e.nextClientSeq >= math.MaxUint32 {
		return e.abortHarness(inflight, errEngineConfig)
	}
	e.nextClientSeq++
	clientSeq := e.nextClientSeq
	if err := e.verifier.ObserveAttempt(logical, attemptPlan, clientSeq); err != nil {
		return e.abortHarness(inflight, err)
	}
	if !inflight.registerClientSeq(clientSeq) {
		return e.abortHarness(inflight, errEngineConfig)
	}
	inflight.attempt = attempt
	inflight.currentClientSeq = clientSeq
	if attempt > 0 {
		e.retryAttempts++
	}
	packet := *intent.Packet
	packet.ClientSeq = clientSeq
	if err := e.sessions.Send(ctx, logical.Sender, &packet); err != nil {
		if ctx != nil && ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return e.cancelAttempt(inflight)
		}
		return errors.Join(e.verifier.ResolveAttemptError(logical.ClientMsgNo, clientSeq), e.scheduleRetry(inflight, now))
	}
	deadline := now.Add(e.attemptTimeout)
	if deadline.Before(now) {
		return e.abortHarness(inflight, errEngineConfig)
	}
	timeout := &engineWork{due: deadline, kind: engineWorkTimeout, intent: intent, attempt: attempt, clientSeq: clientSeq}
	if err := e.addWork(timeout); err != nil {
		return e.abortHarness(inflight, err)
	}
	inflight.timeout = timeout
	return nil
}

func (e *Engine) cancelAttempt(inflight *engineInflight) error {
	if inflight == nil {
		return nil
	}
	logical := inflight.intent.Logical
	e.cancelAttemptTimeout(inflight)
	e.retries.cancel(logical.ClientMsgNo)
	if err := e.verifier.abortSendHarness(logical); err != nil {
		return err
	}
	delete(e.inflight, logical.ClientMsgNo)
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
	if !inflight.hasClientSeq(ack.ClientSeq) {
		return verificationErr
	}
	var rejected *SendackRejectedError
	if errors.As(verificationErr, &rejected) {
		if ack.ClientSeq != inflight.currentClientSeq {
			return nil
		}
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
	if ack.ClientSeq != inflight.currentClientSeq {
		return verificationErr
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
	if schedule.Class == LifecycleRotating || schedule.Class == LifecycleLong {
		if _, active := e.activePosition[edge.PersonChannelID]; active {
			return false, nil
		}
		if _, pending := e.pendingPosition[edge.PersonChannelID]; pending {
			return false, nil
		}
	}
	needed := schedule.InitialBurst.MessageCount
	if schedule.Class != LifecycleOneShot {
		needed++
	}
	if needed > e.workCapacity-e.futureCount() {
		return false, e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
	}
	var lifecycleDue time.Time
	var active engineActiveChannel
	switch schedule.Class {
	case LifecycleRevisit:
		lifecycleDue = e.now.Add(schedule.RevisitAfter)
	case LifecycleRotating, LifecycleLong:
		lifecycleDue = e.now.Add(schedule.ActiveFor)
		direction, directionErr := e.traffic.DirectionFor(relationshipOrdinal)
		if directionErr != nil {
			return false, directionErr
		}
		active = engineActiveChannel{edge: edge, direction: direction}
	}
	if err := e.scheduleRelationshipMessages(edge, relationshipOrdinal, 0, schedule.InitialBurst.MessageCount, e.now, schedule.InitialBurst.Window); err != nil {
		return false, err
	}
	if !lifecycleDue.IsZero() {
		work := &engineWork{
			due: lifecycleDue, kind: engineWorkLifecycle, edge: edge, schedule: schedule,
			relationshipOrdinal: relationshipOrdinal,
		}
		if schedule.Class == LifecycleRevisit {
			deadline, deadlineErr := e.newEligibilityDeadline(lifecycleDue)
			if deadlineErr != nil {
				return false, deadlineErr
			}
			work.eligibilityDeadline = deadline
		}
		if err := e.addWork(work); err != nil {
			return false, err
		}
		if schedule.RequiresColdRuntimeEvidence {
			e.lifecycleByChannel[edge.PersonChannelID] = work
		}
		e.activeLifecycleTimers++
		if schedule.Class == LifecycleRotating || schedule.Class == LifecycleLong {
			if len(e.activeChannels) < e.generator.hotSet.PersonChannels {
				e.addActiveChannel(active)
			} else if !e.addPendingChannel(active, work) {
				return true, e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
			}
		}
	}
	return true, nil
}

func (e *Engine) scheduleRelationshipMessages(edge RelationshipEdge, relationshipOrdinal, logicalOffset uint64, count int, start time.Time, window time.Duration) error {
	return e.scheduleRelationshipMessagesFrom(edge, relationshipOrdinal, logicalOffset, count, start, window, "")
}

func (e *Engine) scheduleRelationshipMessagesFrom(edge RelationshipEdge, relationshipOrdinal, logicalOffset uint64, count int, start time.Time, window time.Duration, requiredSender string) error {
	direction, err := e.traffic.DirectionFor(relationshipOrdinal)
	if err != nil {
		return err
	}
	for messageIndex := 0; messageIndex < count; messageIndex++ {
		var offset time.Duration
		if count > 1 {
			offset = time.Duration((uint64(window) * uint64(messageIndex)) / uint64(count-1))
		}
		sender := requiredSender
		if sender == "" {
			sender, err = SenderFor(direction, uint64(messageIndex), edge.OwnerUID, edge.PeerUID)
			if err != nil {
				return err
			}
		} else if sender != edge.OwnerUID && sender != edge.PeerUID {
			return errEngineConfig
		}
		target := edge.OwnerUID
		if sender == edge.OwnerUID {
			target = edge.PeerUID
		}
		messageOffset := logicalOffset + uint64(messageIndex)
		if messageOffset >= 16 || relationshipOrdinal > (math.MaxUint64-relationshipLogicalBase-messageOffset)/16 {
			return errEngineConfig
		}
		domain := LogicalDomainLifecycle
		if logicalOffset >= 8 {
			domain = LogicalDomainRevisit
		}
		intent := TrafficIntent{
			Logical: LogicalSend{Sender: sender, Target: target}, Kind: TrafficPerson,
			Direction: direction, ChannelID: edge.PersonChannelID, Domain: domain,
		}
		if err := e.addActivity(&engineWork{due: start.Add(offset), kind: engineWorkSend, intent: intent}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) retargetPersonGrant(grant, activity TrafficIntent) (TrafficIntent, error) {
	logicalOrdinal := grant.Logical.LogicalSend
	domain := activity.Domain
	if domain == 0 {
		domain = LogicalDomainPrimary
	}
	if domain != LogicalDomainPrimary {
		var err error
		logicalOrdinal, err = scopedLogicalOrdinal(e.generation, domain, grant.Logical.LogicalSend&maxLogicalOrdinal)
		if err != nil {
			return TrafficIntent{}, err
		}
	}
	logical, err := e.traffic.NewLogicalSend(
		uint64(grant.Logical.WorkerID), logicalOrdinal, TrafficPerson,
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
	activity.Domain = domain
	return activity, nil
}

func (e *Engine) grantShouldCorrelate(grant TrafficIntent, domain LogicalDomain) (bool, error) {
	logicalOrdinal := grant.Logical.LogicalSend
	if domain != LogicalDomainPrimary {
		var err error
		logicalOrdinal, err = scopedLogicalOrdinal(e.generation, domain, grant.Logical.LogicalSend&maxLogicalOrdinal)
		if err != nil {
			return false, err
		}
	}
	return e.verifier.ShouldCorrelate(LogicalSend{LogicalSend: logicalOrdinal, WorkerID: grant.Logical.WorkerID})
}

func (e *Engine) routePersonGrant(grant TrafficIntent, now time.Time) (TrafficIntent, error) {
	for scans := 0; scans < maxActivityRouteScans && len(e.activity) > 0 && !e.activity[0].due.After(now); scans++ {
		activity := heap.Pop(&e.activity).(*engineWork)
		activity.offered = true
		if !activity.eligibilityDeadline.After(now) {
			return TrafficIntent{}, e.expireActivity(activity)
		}
		if !e.sessions.IsOnline(activity.intent.Logical.Sender) {
			if err := e.deferActivity(activity, now); err != nil {
				return TrafficIntent{}, err
			}
			continue
		}
		correlate, err := e.grantShouldCorrelate(grant, activity.intent.Domain)
		if err != nil {
			return TrafficIntent{}, err
		}
		if correlate && !e.sessions.IsOnline(activity.intent.Logical.Target) {
			if err := e.deferActivity(activity, now); err != nil {
				return TrafficIntent{}, err
			}
			continue
		}
		return e.retargetPersonGrant(grant, activity.intent)
	}
	correlate, err := e.grantShouldCorrelate(grant, LogicalDomainPrimary)
	if err != nil {
		return TrafficIntent{}, err
	}
	for scan := 0; scan < len(e.activeChannels); scan++ {
		position := int((e.activeCursor + uint64(scan)) % uint64(len(e.activeChannels)))
		active := e.activeChannels[position]
		sender, err := SenderFor(active.direction, grant.Logical.LogicalSend, active.edge.OwnerUID, active.edge.PeerUID)
		if err != nil {
			return TrafficIntent{}, err
		}
		if !e.sessions.IsOnline(sender) {
			continue
		}
		target := active.edge.OwnerUID
		if sender == active.edge.OwnerUID {
			target = active.edge.PeerUID
		}
		if correlate && !e.sessions.IsOnline(target) {
			continue
		}
		e.activeCursor = uint64(position + 1)
		template := TrafficIntent{
			Logical: LogicalSend{Sender: sender, Target: target}, Kind: TrafficPerson,
			Direction: active.direction, ChannelID: active.edge.PersonChannelID, Domain: LogicalDomainPrimary,
		}
		return e.retargetPersonGrant(grant, template)
	}
	return TrafficIntent{}, e.recordRuntimeFailure(RuntimeFailureUnderDelivery, uint64(len(e.activeChannels)))
}

func (e *Engine) deferActivity(activity *engineWork, now time.Time) error {
	if activity == nil || activity.eligibilityDeadline.IsZero() {
		return errEngineConfig
	}
	if !activity.eligibilityDeadline.After(now) {
		return e.expireActivity(activity)
	}
	deferred := now.Add(activityRouteDeferral)
	if !deferred.After(now) {
		return errEngineConfig
	}
	if !deferred.Before(activity.eligibilityDeadline) {
		return e.expireActivity(activity)
	}
	activity.due = deferred
	return e.addActivity(activity)
}

func (e *Engine) expireActivity(_ *engineWork) error {
	e.activityUnderDelivered++
	return e.recordRuntimeFailure(RuntimeFailureUnderDelivery, 1)
}

func (e *Engine) addActiveChannel(channel engineActiveChannel) {
	if _, exists := e.activePosition[channel.edge.PersonChannelID]; exists {
		return
	}
	e.activePosition[channel.edge.PersonChannelID] = len(e.activeChannels)
	e.activeChannels = append(e.activeChannels, channel)
}

func (e *Engine) removeActiveChannel(channelID string) bool {
	position, ok := e.activePosition[channelID]
	if !ok {
		return false
	}
	last := len(e.activeChannels) - 1
	if position != last {
		moved := e.activeChannels[last]
		e.activeChannels[position] = moved
		e.activePosition[moved.edge.PersonChannelID] = position
	}
	e.activeChannels[last] = engineActiveChannel{}
	e.activeChannels = e.activeChannels[:last]
	delete(e.activePosition, channelID)
	if len(e.activeChannels) == 0 {
		e.activeCursor = 0
	} else {
		e.activeCursor %= uint64(len(e.activeChannels))
	}
	return true
}

func (e *Engine) addPendingChannel(active engineActiveChannel, lifecycle *engineWork) bool {
	channelID := active.edge.PersonChannelID
	if channelID == "" || lifecycle == nil || len(e.pendingChannels) >= e.workCapacity {
		return false
	}
	if _, exists := e.pendingPosition[channelID]; exists {
		return false
	}
	e.pendingPosition[channelID] = len(e.pendingChannels)
	e.pendingChannels = append(e.pendingChannels, enginePendingChannel{active: active, lifecycle: lifecycle})
	return true
}

func (e *Engine) removePendingChannel(channelID string) bool {
	position, ok := e.pendingPosition[channelID]
	if !ok {
		return false
	}
	last := len(e.pendingChannels) - 1
	if position != last {
		moved := e.pendingChannels[last]
		e.pendingChannels[position] = moved
		e.pendingPosition[moved.active.edge.PersonChannelID] = position
	}
	e.pendingChannels[last] = enginePendingChannel{}
	e.pendingChannels = e.pendingChannels[:last]
	delete(e.pendingPosition, channelID)
	return true
}

func (e *Engine) promotePendingChannels(now time.Time) {
	for len(e.activeChannels) < e.generator.hotSet.PersonChannels && len(e.pendingChannels) > 0 {
		pending := e.pendingChannels[len(e.pendingChannels)-1]
		e.removePendingChannel(pending.active.edge.PersonChannelID)
		if pending.lifecycle == nil || !pending.lifecycle.due.After(now) {
			continue
		}
		e.addActiveChannel(pending.active)
	}
}

func (e *Engine) routeGroupGrant(grant TrafficIntent) (TrafficIntent, error) {
	groupIndex, ok := e.generator.catalog.IndexFromGroupID(grant.ChannelID)
	if !ok {
		return TrafficIntent{}, errEngineConfig
	}
	group, err := e.generator.catalog.Group(groupIndex)
	if err != nil {
		return TrafficIntent{}, err
	}
	groupOwner, err := e.generator.catalog.GroupOwner(groupIndex)
	if err != nil || groupOwner != e.workerID || grant.Logical.WorkerID != uint32(e.workerID) {
		return TrafficIntent{}, errEngineConfig
	}
	correlate, err := e.verifier.ShouldCorrelate(grant.Logical)
	if err != nil {
		return TrafficIntent{}, err
	}
	sender, ok := e.sessions.onlineGroupMember(group, grant.Logical.LogicalSend, correlate)
	if !ok && group.Category != GroupVeryLarge {
		var routedIndex uint64
		sender, routedIndex, ok = e.sessions.onlineGroupMemberInCategory(group.Category, grant.Logical.LogicalSend, correlate, e.workerID)
		if ok {
			group, err = e.generator.catalog.Group(routedIndex)
			if err != nil {
				return TrafficIntent{}, err
			}
		}
	}
	if !ok {
		return TrafficIntent{}, e.recordRuntimeFailure(RuntimeFailureUnderDelivery, uint64(group.MemberCount))
	}
	logical, err := e.traffic.NewLogicalSend(
		uint64(grant.Logical.WorkerID), grant.Logical.LogicalSend, TrafficGroup, sender.UID, group.ID,
	)
	if err != nil {
		return TrafficIntent{}, err
	}
	payload, err := e.traffic.BuildPayload(logical, grant.PayloadBytes)
	if err != nil {
		return TrafficIntent{}, err
	}
	grant.Logical = logical
	grant.Packet = packetForTrafficIntent(logical, payload)
	grant.ChannelID = group.ID
	return grant, nil
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

func (e *Engine) cleanupPendingActivities() {
	if len(e.activity) == 0 {
		return
	}
	var underDelivered, futureCanceled uint64
	for _, activity := range e.activity {
		if activity.offered || !activity.due.After(e.now) {
			underDelivered++
			continue
		}
		futureCanceled++
	}
	e.activityFutureCanceled += futureCanceled
	if underDelivered > 0 {
		e.activityUnderDelivered += underDelivered
		_ = e.recordRuntimeFailure(RuntimeFailureUnderDelivery, underDelivered)
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
	case RuntimeFailureUnderDelivery:
		failureCode = FailureCodeOfferedLoadUnderDelivery
	case RuntimeFailureSchedulerCPUSaturated:
		failureCode = FailureCodeSessionSchedulerCPUSaturated
	}
	_ = e.evidence.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: failureCode, Value: value})
	return &RuntimeError{code: code}
}

func (e *Engine) recordRuntimeFailureSync(code RuntimeFailureCode, value uint64) error {
	response := make(chan error, 1)
	if err := e.enqueue(engineCommand{run: func() { response <- e.recordRuntimeFailure(code, value) }}); err != nil {
		return err
	}
	return <-response
}

func (e *Engine) buildSnapshot(running bool) EngineSnapshot {
	sessions := e.sessions.Snapshot()
	retries := RetrySchedulerSnapshot{}
	if e.retries != nil {
		retries = e.retries.Snapshot()
	}
	snapshot := EngineSnapshot{
		Running: running, Generation: e.generation, WorkerID: e.workerID, WorkerCount: e.workers,
		OnlineTarget: e.onlineTarget, ActiveLoops: int(e.activeLoops.Load()), ActiveSteps: int(e.activeSteps.Load()),
		Online: sessions.Online, LoginStarting: sessions.Starting, TrafficReady: sessions.TrafficReady,
		GatewayConnectLatency: sessions.GatewayConnectLatency, ConversationSyncLatency: sessions.ConversationSyncLatency,
		QueueCurrent: e.queuedSends, FutureCurrent: e.futureCount(), ActivityCurrent: len(e.activity),
		ActivityUnderDelivered: e.activityUnderDelivered,
		ActivityFutureCanceled: e.activityFutureCanceled,
		QueuePeak:              e.workPeak, QueueCapacity: e.workCapacity,
		RetryQueueDepth: retries.Depth, RetryQueuePeak: retries.Peak, RetryQueueCapacity: e.retryCapacity,
		InflightCurrent: len(e.inflight), InflightPeak: e.inflightPeak, InflightCapacity: e.inflightCapacity,
		TransportQueueDepth: sessions.QueueDepth, TransportQueueCapacity: sessions.QueueCapacity,
		TransportInflight: sessions.TransportInflight, RelationshipLookback: MaxForwardRelationships,
		ActiveLifecycleTimers: e.activeLifecycleTimers, ColdEvidencePending: len(e.lifecycleByChannel),
		ActiveHotChannels: len(e.activeChannels), PendingHotChannels: len(e.pendingChannels),
		LoginPlannedNew: e.schedulerMetrics.plannedNew.Load(), LoginPlannedReturning: e.schedulerMetrics.plannedReturning.Load(),
		LoginAdmittedNew: e.schedulerMetrics.admittedNew.Load(), LoginAdmittedReturning: e.schedulerMetrics.admittedReturning.Load(),
		LoginCompletedNew: e.schedulerMetrics.completedNew.Load(), LoginCompletedReturning: e.schedulerMetrics.completedReturning.Load(),
		LoginSkipped: e.schedulerMetrics.skipped.Load(), LoginReplacements: e.schedulerMetrics.replacements.Load(),
		SessionsExpired: e.schedulerMetrics.expired.Load(),
		RetryAttempts:   e.retryAttempts,
		FinalFailures:   e.finalFailures, HarnessInvalid: e.harnessInvalid + e.commandSaturation.Load(),
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
		Running: running, Generation: e.generation, WorkerID: e.workerID, WorkerCount: e.workers,
		OnlineTarget: e.onlineTarget, ActiveLoops: int(e.activeLoops.Load()), ActiveSteps: int(e.activeSteps.Load()),
		QueueCapacity: e.workCapacity, RetryQueueCapacity: e.retryCapacity,
		InflightCapacity: e.inflightCapacity, CompletionQueueCapacity: e.commandCapacity,
		RelationshipLookback:  MaxForwardRelationships,
		Classification:        e.evidence.Snapshot().Classification,
		GatewayConnectLatency: newWorkerHistogramSnapshot(), ConversationSyncLatency: newWorkerHistogramSnapshot(),
	}
}
