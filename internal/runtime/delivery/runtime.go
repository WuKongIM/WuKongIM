package delivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	defaultRuntimeQueueSize       = 1024
	defaultRuntimeWorkers         = 1
	defaultRuntimePlanTimeout     = 5 * time.Second
	defaultRuntimePlanRecipients  = 512
	defaultRuntimePushBatchSize   = 256
	defaultRuntimeOwnerWorkers    = 4
	defaultRuntimeRetryAttempts   = 3
	defaultRuntimeRetryBackoff    = 5 * time.Millisecond
	defaultRuntimeRetryMaxBackoff = 100 * time.Millisecond
	runtimeAckExpiryInterval      = time.Second
)

var (
	// ErrRuntimeClosed reports that Online Delivery is not accepting work.
	ErrRuntimeClosed = errors.New("internal/runtime/delivery: runtime closed")
	// ErrInvalidPlan reports an unusable Recipient Delivery Plan.
	ErrInvalidPlan = errors.New("internal/runtime/delivery: invalid recipient delivery plan")
	// ErrPlanTooLarge reports a plan exceeding the configured recipient bound.
	ErrPlanTooLarge = errors.New("internal/runtime/delivery: recipient delivery plan too large")
	// ErrPresenceResultMissing reports an unaligned exact-target presence result.
	ErrPresenceResultMissing = errors.New("internal/runtime/delivery: target presence result missing")
	// ErrPresenceResolverUnavailable reports plan execution without a presence adapter.
	ErrPresenceResolverUnavailable = errors.New("internal/runtime/delivery: presence resolver unavailable")
	// ErrOwnerPushNotLocal reports an owner-local push addressed to another node.
	ErrOwnerPushNotLocal = errors.New("internal/runtime/delivery: owner push is not local")
	// ErrOwnerPushRetryExhausted reports routes still retryable after the final attempt.
	ErrOwnerPushRetryExhausted = errors.New("internal/runtime/delivery: owner push retry exhausted")
	// ErrOwnerPushPanic reports a recovered owner adapter panic.
	ErrOwnerPushPanic = errors.New("internal/runtime/delivery: owner push panic")
	// ErrPlanPanic reports an isolated unexpected plan-processing panic.
	ErrPlanPanic = errors.New("internal/runtime/delivery: plan processing panic")
	// ErrSessionWriterUnavailable reports owner-local execution without a session adapter.
	ErrSessionWriterUnavailable = errors.New("internal/runtime/delivery: local session writer unavailable")
)

type runtimeState uint8

const (
	runtimeClosed runtimeState = iota
	runtimeOpen
	runtimeClosing
)

// RuntimeOptions configures the authoritative Online Delivery runtime.
type RuntimeOptions struct {
	// LocalNodeID identifies the owner node executed in process.
	LocalNodeID uint64
	// Presence resolves exact recipient-authority target groups.
	Presence PlanPresenceResolver
	// RemoteOwnerPusher forwards grouped routes to non-local owner nodes.
	RemoteOwnerPusher RemoteOwnerPusher
	// SessionWriter performs final exact owner-local session writes.
	SessionWriter LocalSessionWriter
	// OfflineRecipientsObserver receives durable-only offline batches.
	OfflineRecipientsObserver OfflineRecipientsObserver
	// QueueSize bounds accepted Recipient Delivery Plans.
	QueueSize int
	// Workers bounds concurrent plan processing.
	Workers int
	// PlanTimeout bounds one accepted plan's total processing time.
	PlanTimeout time.Duration
	// MaxPlanRecipients rejects plans above this recipient count.
	MaxPlanRecipients int
	// OwnerPushBatchSize bounds routes in one owner-node push.
	OwnerPushBatchSize int
	// OwnerConcurrency bounds concurrent distinct-owner processing per plan.
	OwnerConcurrency int
	// RetryMaxAttempts bounds attempts for retryable exact routes.
	RetryMaxAttempts int
	// RetryInitialBackoff is the first retry delay.
	RetryInitialBackoff time.Duration
	// RetryMaxBackoff caps exponential retry delay.
	RetryMaxBackoff time.Duration
	// PendingAckTTL expires stale owner-local feedback state during push activity.
	PendingAckTTL time.Duration
	// Acks optionally supplies the owner-local tracker.
	Acks *AckTracker
	// AckObserver receives exact pending-count changes.
	AckObserver AckObserver
	// AckBatchObserver receives aggregate reservation transaction stages.
	AckBatchObserver AckBatchObserver
	// Observer receives plan, pressure, and owner-push observations.
	Observer RuntimeObserver
	// Goroutines tracks long-lived runtime tasks.
	Goroutines *goruntimeregistry.Registry
}

// Runtime owns plan processing, owner-local writes, retry, and pending ACK state.
type Runtime struct {
	// localNodeID selects the owner-local execution path.
	localNodeID uint64
	// presence resolves already-fenced recipient target groups.
	presence PlanPresenceResolver
	// remoteOwnerPusher transports exact owner groups to peer runtimes.
	remoteOwnerPusher RemoteOwnerPusher
	// sessionWriter owns final exact-session validation and physical writes.
	sessionWriter LocalSessionWriter
	// offlineRecipientsObserver receives durable-only auxiliary effects.
	offlineRecipientsObserver OfflineRecipientsObserver
	// queue is the fixed-capacity ownership-transfer boundary for plans.
	queue chan onlinedelivery.RecipientDeliveryPlan
	// workers is the fixed plan-processing concurrency.
	workers int
	// planTimeout bounds the complete processing lifetime of one accepted plan.
	planTimeout time.Duration
	// maxPlanRecipients rejects plans larger than channel append's packing bound.
	maxPlanRecipients int
	// ownerPushBatchSize bounds one owner command.
	ownerPushBatchSize int
	// ownerConcurrency bounds distinct owners executing for one plan.
	ownerConcurrency int
	// retryMaxAttempts bounds exact-route owner push attempts.
	retryMaxAttempts int
	// retryInitialBackoff is the first inline narrowed-retry delay.
	retryInitialBackoff time.Duration
	// retryMaxBackoff caps inline narrowed-retry delay.
	retryMaxBackoff time.Duration
	// pendingAckTTL bounds committed pending feedback state.
	pendingAckTTL time.Duration
	// acks owns sharded owner-local pending feedback identities.
	acks *AckTracker
	// ackObserver receives serialized identity-count mutations.
	ackObserver AckObserver
	// ackBatchObserver receives aggregate reservation transaction stages.
	ackBatchObserver AckBatchObserver
	// observer receives bounded plan and owner-push outcomes.
	observer RuntimeObserver
	// goroutines accounts all runtime lifecycle and bounded burst tasks.
	goroutines *goruntimeregistry.Registry

	// mu protects lifecycle generation state and admission wait-group gates.
	mu sync.Mutex
	// state prevents overlap between runtime generations.
	state runtimeState
	// acceptDone closes before shutdown waits for in-flight admission senders.
	acceptDone chan struct{}
	// stopReady closes only after new work and external owner pushes quiesce.
	stopReady chan struct{}
	// runCancel cancels accepted plan processing for the current generation.
	runCancel context.CancelFunc
	// done closes after every current-generation plan worker exits.
	done chan struct{}
	// admissionSenders accounts calls that passed the lifecycle admission gate.
	admissionSenders sync.WaitGroup
	// ownerPushes accounts synchronous RPC owner pushes during shutdown.
	ownerPushes sync.WaitGroup
	// ackMu serializes tracker mutation observations with recipient feedback.
	ackMu sync.Mutex
	// inflight publishes the number of executing plan workers.
	inflight atomic.Int64
	// pressureMu serializes pressure callbacks from concurrent workers.
	pressureMu sync.Mutex
	// pendingAckExpiryNext rate-limits opportunistic TTL scans.
	pendingAckExpiryNext atomic.Int64
}

// NewRuntime creates one Online Delivery implementation.
func NewRuntime(opts RuntimeOptions) *Runtime {
	queueSize := boundedRuntimePositive(opts.QueueSize, defaultRuntimeQueueSize)
	workers := boundedRuntimePositive(opts.Workers, defaultRuntimeWorkers)
	planTimeout := opts.PlanTimeout
	if planTimeout <= 0 {
		planTimeout = defaultRuntimePlanTimeout
	}
	maxPlanRecipients := boundedRuntimePositive(opts.MaxPlanRecipients, defaultRuntimePlanRecipients)
	ownerPushBatchSize := boundedRuntimePositive(opts.OwnerPushBatchSize, defaultRuntimePushBatchSize)
	ownerConcurrency := boundedRuntimePositive(opts.OwnerConcurrency, defaultRuntimeOwnerWorkers)
	retryMaxAttempts := boundedRuntimePositive(opts.RetryMaxAttempts, defaultRuntimeRetryAttempts)
	retryInitialBackoff := opts.RetryInitialBackoff
	if retryInitialBackoff <= 0 {
		retryInitialBackoff = defaultRuntimeRetryBackoff
	}
	retryMaxBackoff := opts.RetryMaxBackoff
	if retryMaxBackoff <= 0 {
		retryMaxBackoff = defaultRuntimeRetryMaxBackoff
	}
	acks := opts.Acks
	if acks == nil {
		acks = NewAckTracker(AckTrackerOptions{})
	}
	return &Runtime{
		localNodeID:               opts.LocalNodeID,
		presence:                  opts.Presence,
		remoteOwnerPusher:         opts.RemoteOwnerPusher,
		sessionWriter:             opts.SessionWriter,
		offlineRecipientsObserver: opts.OfflineRecipientsObserver,
		queue:                     make(chan onlinedelivery.RecipientDeliveryPlan, queueSize),
		workers:                   workers,
		planTimeout:               planTimeout,
		maxPlanRecipients:         maxPlanRecipients,
		ownerPushBatchSize:        ownerPushBatchSize,
		ownerConcurrency:          ownerConcurrency,
		retryMaxAttempts:          retryMaxAttempts,
		retryInitialBackoff:       retryInitialBackoff,
		retryMaxBackoff:           retryMaxBackoff,
		pendingAckTTL:             opts.PendingAckTTL,
		acks:                      acks,
		ackObserver:               opts.AckObserver,
		ackBatchObserver:          opts.AckBatchObserver,
		observer:                  opts.Observer,
		goroutines:                opts.Goroutines,
		state:                     runtimeClosed,
	}
}

// WorkerCapacity returns the configured plan worker count.
func (r *Runtime) WorkerCapacity() int {
	if r == nil {
		return 0
	}
	return r.workers
}

// Start opens all caller-specific Online Delivery interfaces.
func (r *Runtime) Start(context.Context) error {
	if r == nil {
		return nil
	}
	resetRemoved := -1
	r.mu.Lock()
	switch r.state {
	case runtimeOpen:
		r.mu.Unlock()
		return nil
	case runtimeClosing:
		closed, removed := r.finishClosedIfDoneLocked()
		if !closed {
			r.mu.Unlock()
			return ErrRuntimeClosed
		}
		resetRemoved = removed
	}
	acceptDone := make(chan struct{})
	stopReady := make(chan struct{})
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(r.workers)
	for i := 0; i < r.workers; i++ {
		goruntimeregistry.SafeGo(r.goroutines, goruntimeregistry.TaskOnlineDeliveryWorker, func() {
			defer workers.Done()
			r.runWorker(runCtx, stopReady)
		})
	}
	goruntimeregistry.SafeGo(r.goroutines, goruntimeregistry.TaskOnlineDeliveryLifecycle, func() {
		workers.Wait()
		close(done)
	})
	r.acceptDone = acceptDone
	r.stopReady = stopReady
	r.runCancel = runCancel
	r.done = done
	r.state = runtimeOpen
	r.mu.Unlock()
	if resetRemoved >= 0 {
		r.observeAckReset(resetRemoved)
	}
	r.observePressure()
	return nil
}

// Stop quiesces accepted work, resets transient ACK state, and remains
// restartable only after the current generation exits completely.
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	switch r.state {
	case runtimeClosed:
		r.mu.Unlock()
		return nil
	case runtimeClosing:
		done := r.done
		r.mu.Unlock()
		return r.waitClosed(ctx, done)
	}
	acceptDone := r.acceptDone
	stopReady := r.stopReady
	runCancel := r.runCancel
	done := r.done
	r.state = runtimeClosing
	close(acceptDone)
	r.mu.Unlock()
	goruntimeregistry.SafeGo(r.goroutines, goruntimeregistry.TaskOnlineDeliveryLifecycle, func() {
		r.admissionSenders.Wait()
		r.ownerPushes.Wait()
		close(stopReady)
	})
	err := r.waitClosed(ctx, done)
	if err != nil && runCancel != nil {
		// A failed graceful drain releases cooperative adapters while the
		// generation remains closing until every handler actually exits.
		runCancel()
	}
	return err
}

// EnqueueRecipientDeliveryPlan transfers ownership of one valid bounded plan.
func (r *Runtime) EnqueueRecipientDeliveryPlan(ctx context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	started := time.Now()
	result := ObservationResultAccepted
	defer func() {
		if r != nil {
			r.observePlanAdmission(PlanAdmissionEvent{
				Result: result, QueueDepth: len(r.queue), QueueCapacity: cap(r.queue), Duration: positiveRuntimeDuration(time.Since(started)),
			})
		}
	}()
	if r == nil {
		result = ObservationResultClosed
		return ErrRuntimeClosed
	}
	if err := r.validatePlan(plan); err != nil {
		result = ObservationResultInvalid
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		result = runtimeResultForContext(err)
		return err
	}
	r.mu.Lock()
	if r.state != runtimeOpen {
		r.mu.Unlock()
		result = ObservationResultClosed
		return ErrRuntimeClosed
	}
	queue := r.queue
	acceptDone := r.acceptDone
	r.admissionSenders.Add(1)
	r.mu.Unlock()
	defer r.admissionSenders.Done()

	select {
	case queue <- plan:
		r.observePressure()
		return nil
	case <-acceptDone:
		result = ObservationResultClosed
		return ErrRuntimeClosed
	case <-ctx.Done():
		result = runtimeResultForContext(ctx.Err())
		return ctx.Err()
	}
}

func (r *Runtime) validatePlan(plan onlinedelivery.RecipientDeliveryPlan) error {
	if !plan.Mode.Valid() || plan.Event.MessageID == 0 || len(plan.Targets) == 0 {
		return ErrInvalidPlan
	}
	if plan.RecipientCount() > r.maxPlanRecipients {
		return ErrPlanTooLarge
	}
	for _, target := range plan.Targets {
		if target.Target.Validate() != nil || len(target.Recipients) == 0 {
			return ErrInvalidPlan
		}
		for _, recipient := range target.Recipients {
			if recipient.UID == "" {
				return ErrInvalidPlan
			}
		}
	}
	return nil
}

// runWorker drains ownership-transferred plans after admission closes. The
// generation context is canceled only when the caller's graceful-stop budget
// expires, so a successful Stop never discards accepted delivery work.
func (r *Runtime) runWorker(runCtx context.Context, stopReady <-chan struct{}) {
	for {
		select {
		case plan := <-r.queue:
			r.runPlan(runCtx, plan)
		case <-stopReady:
			for {
				select {
				case plan := <-r.queue:
					r.runPlan(runCtx, plan)
				default:
					return
				}
			}
		}
	}
}

// runPlan isolates one plan panic, applies the per-plan execution deadline,
// and emits exactly one terminal observation for every accepted plan.
func (r *Runtime) runPlan(runCtx context.Context, plan onlinedelivery.RecipientDeliveryPlan) {
	r.inflight.Add(1)
	r.observePressure()
	started := time.Now()
	result := ObservationResultOK
	var failure PlanFailureSample
	defer func() {
		if recovered := recover(); recovered != nil {
			result = ObservationResultPanic
			failure = planFailureSample(PlanFailurePhasePanic, plan, 0, ErrPlanPanic)
		}
		r.inflight.Add(-1)
		r.observePressure()
		r.observePlanTerminal(PlanTerminalEvent{
			Result: result, Mode: plan.Mode, Recipients: plan.RecipientCount(),
			Duration: positiveRuntimeDuration(time.Since(started)), Failure: failure,
		})
	}()
	ctx, cancel := context.WithTimeout(runCtx, r.planTimeout)
	defer cancel()
	if err := r.processPlan(ctx, plan); err != nil {
		result = runtimeResultForError(err)
		failure = failureSampleFromError(err, plan)
	}
}

// processPlan resolves every exact authority target, preserves sibling
// progress, and owns all bounded owner grouping and retry for one accepted plan.
func (r *Runtime) processPlan(ctx context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	if err := ctx.Err(); err != nil {
		return newPlanFailure(PlanFailurePhaseContext, plan, 0, 0, err)
	}
	if r.presence == nil {
		return newPlanFailure(PlanFailurePhasePresence, plan, 0, 0, ErrPresenceResolverUnavailable)
	}
	resolved := r.presence.EndpointsByTargets(ctx, plan.Targets)
	grouped := make(map[uint64][]onlinedelivery.Route)
	groupedSamples := make(map[uint64][]PlanFailureSample)
	ownerOrder := make([]uint64, 0)
	var firstErr error
	var offline []string
	var seenOffline map[string]struct{}
	if plan.Mode == onlinedelivery.ModeDurable && r.offlineRecipientsObserver != nil {
		seenOffline = make(map[string]struct{}, plan.RecipientCount())
	}
	for i, target := range plan.Targets {
		if i >= len(resolved) {
			if firstErr == nil {
				firstErr = newPlanFailure(PlanFailurePhasePresence, plan, i, 0, ErrPresenceResultMissing)
			}
			continue
		}
		if resolved[i].Err != nil {
			if firstErr == nil {
				firstErr = newPlanFailure(PlanFailurePhasePresence, plan, i, 0, resolved[i].Err)
			}
			continue
		}
		if seenOffline != nil {
			offline = appendOfflineUIDs(offline, seenOffline, target, resolved[i].Routes)
		}
		for _, route := range resolved[i].Routes {
			if route.OwnerNodeID == 0 || suppressSenderRoute(plan, route) {
				continue
			}
			if _, ok := grouped[route.OwnerNodeID]; !ok {
				ownerOrder = append(ownerOrder, route.OwnerNodeID)
			}
			grouped[route.OwnerNodeID] = append(grouped[route.OwnerNodeID], route)
			groupedSamples[route.OwnerNodeID] = append(groupedSamples[route.OwnerNodeID], PlanFailureSample{
				Phase:        PlanFailurePhaseOwnerPush,
				RecipientUID: route.UID,
				Target:       target.Target,
				OwnerNodeID:  route.OwnerNodeID,
			})
		}
	}
	r.notifyOfflineSafely(ctx, plan, offline)
	ownerFailures := make([]PlanFailureSample, len(ownerOrder))
	runOwner := func(index int) {
		ownerNodeID := ownerOrder[index]
		routes := grouped[ownerNodeID]
		samples := groupedSamples[ownerNodeID]
		for start := 0; start < len(routes); start += r.ownerPushBatchSize {
			end := start + r.ownerPushBatchSize
			if end > len(routes) {
				end = len(routes)
			}
			failedRoute, err := r.pushWithRetry(ctx, onlinedelivery.OwnerPush{
				OwnerNodeID: ownerNodeID,
				Event:       plan.Event,
				Routes:      routes[start:end],
			})
			if err != nil && ownerFailures[index].Err == nil {
				sample := samples[start]
				for offset, route := range routes[start:end] {
					if route == failedRoute {
						sample = samples[start+offset]
						break
					}
				}
				sample.Err = err
				ownerFailures[index] = sample
			}
			if err != nil && ctx.Err() != nil {
				return
			}
		}
	}
	runBoundedRuntime(r.goroutines, len(ownerOrder), r.ownerConcurrency, runOwner)
	for _, sample := range ownerFailures {
		if sample.Err != nil && firstErr == nil {
			firstErr = &planFailureError{sample: sample}
		}
	}
	return firstErr
}

type planFailureError struct {
	sample PlanFailureSample
}

func (e *planFailureError) Error() string {
	if e == nil || e.sample.Err == nil {
		return "internal/runtime/delivery: plan processing failed"
	}
	return e.sample.Err.Error()
}

func (e *planFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sample.Err
}

func newPlanFailure(
	phase PlanFailurePhase,
	plan onlinedelivery.RecipientDeliveryPlan,
	targetIndex int,
	ownerNodeID uint64,
	err error,
) error {
	sample := planFailureSample(phase, plan, targetIndex, err)
	sample.OwnerNodeID = ownerNodeID
	return &planFailureError{sample: sample}
}

func planFailureSample(
	phase PlanFailurePhase,
	plan onlinedelivery.RecipientDeliveryPlan,
	targetIndex int,
	err error,
) PlanFailureSample {
	sample := PlanFailureSample{Phase: phase, Err: err}
	if targetIndex < 0 || targetIndex >= len(plan.Targets) {
		return sample
	}
	target := plan.Targets[targetIndex]
	sample.Target = target.Target
	if len(target.Recipients) > 0 {
		sample.RecipientUID = target.Recipients[0].UID
	}
	return sample
}

func failureSampleFromError(err error, plan onlinedelivery.RecipientDeliveryPlan) PlanFailureSample {
	var failure *planFailureError
	if errors.As(err, &failure) {
		return failure.sample
	}
	return planFailureSample(PlanFailurePhaseOwnerPush, plan, 0, err)
}

// pushWithRetry retries only the exact routes classified as retryable. It
// returns one bounded failed-route sample after the configured attempt limit.
func (r *Runtime) pushWithRetry(ctx context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.Route, error) {
	backoff := r.retryInitialBackoff
	for attempt := 1; attempt <= r.retryMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return firstOwnerPushRoute(push), err
		}
		result, err := r.routeOwnerPush(ctx, push)
		if err == nil {
			if len(result.Retryable) == 0 {
				return onlinedelivery.Route{}, nil
			}
			push.Routes = result.Retryable
		}
		if attempt == r.retryMaxAttempts {
			if err != nil {
				return firstOwnerPushRoute(push), errors.Join(ErrOwnerPushRetryExhausted, err)
			}
			return firstOwnerPushRoute(push), ErrOwnerPushRetryExhausted
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return firstOwnerPushRoute(push), ctx.Err()
		}
		backoff *= 2
		if backoff > r.retryMaxBackoff {
			backoff = r.retryMaxBackoff
		}
	}
	return onlinedelivery.Route{}, nil
}

func firstOwnerPushRoute(push onlinedelivery.OwnerPush) onlinedelivery.Route {
	if len(push.Routes) == 0 {
		return onlinedelivery.Route{}
	}
	return push.Routes[0]
}

// routeOwnerPush keeps single-node cluster and multi-node delivery on the same
// owner seam while containing remote adapter panics as retryable failures.
func (r *Runtime) routeOwnerPush(ctx context.Context, push onlinedelivery.OwnerPush) (result onlinedelivery.OwnerPushResult, err error) {
	if push.OwnerNodeID == r.localNodeID {
		return r.pushOwnerLocal(ctx, push)
	}
	return r.pushOwnerRemote(ctx, push)
}

func (r *Runtime) pushOwnerRemote(ctx context.Context, push onlinedelivery.OwnerPush) (result onlinedelivery.OwnerPushResult, err error) {
	started := time.Now()
	var failure OwnerPushFailureSample
	defer func() {
		if recover() != nil {
			result = onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}
			err = ErrOwnerPushPanic
		}
		if err != nil && len(push.Routes) > 0 {
			failure = OwnerPushFailureSample{Err: err, Route: push.Routes[0]}
		}
		r.observeOwnerPushResult(push, result, err, failure, started)
	}()
	if r.remoteOwnerPusher == nil {
		return onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
	}
	return r.remoteOwnerPusher.PushOwner(ctx, push)
}

// PushOwner enters the same owner-local execution used by local plan processing.
func (r *Runtime) PushOwner(ctx context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	if r == nil {
		return onlinedelivery.OwnerPushResult{}, ErrRuntimeClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.state != runtimeOpen {
		r.mu.Unlock()
		return onlinedelivery.OwnerPushResult{}, ErrRuntimeClosed
	}
	r.ownerPushes.Add(1)
	r.mu.Unlock()
	defer r.ownerPushes.Done()
	return r.pushOwnerLocal(ctx, push)
}

// pushOwnerLocal owns the complete reserve-write-finish-or-rollback ACK
// transaction and never exposes reservation tokens to the session adapter.
func (r *Runtime) pushOwnerLocal(ctx context.Context, push onlinedelivery.OwnerPush) (result onlinedelivery.OwnerPushResult, err error) {
	started := time.Now()
	var recoveryPending []PendingRecvAck
	var recoveryTokens []AckBindToken
	var failure OwnerPushFailureSample
	defer func() {
		if recovered := recover(); recovered != nil {
			for i := range recoveryPending {
				if i < len(recoveryTokens) {
					r.rollbackPendingAck(recoveryPending[i], recoveryTokens[i])
				}
			}
			result = onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}
			err = ErrOwnerPushPanic
			if len(push.Routes) > 0 {
				failure = OwnerPushFailureSample{Err: err, Route: push.Routes[0]}
			}
		}
		r.observeOwnerPushResult(push, result, err, failure, started)
	}()
	if r.localNodeID == 0 || push.OwnerNodeID == 0 || push.OwnerNodeID != r.localNodeID {
		return onlinedelivery.OwnerPushResult{}, ErrOwnerPushNotLocal
	}
	r.expirePendingAcksIfDue()
	pendings := make([]PendingRecvAck, len(push.Routes))
	duplicates := make([]bool, len(push.Routes))
	seen := make(map[ackMessageKey]struct{}, len(push.Routes))
	for i, route := range push.Routes {
		if route.UID == "" || route.SessionID == 0 || route.OwnerNodeID != push.OwnerNodeID || push.Event.MessageID == 0 {
			continue
		}
		pendings[i] = PendingRecvAck{
			UID: route.UID, SessionID: route.SessionID,
			MessageID: push.Event.MessageID, MessageSeq: push.Event.MessageSeq,
			ChannelID: push.Event.ChannelID, ChannelType: push.Event.ChannelType,
		}
		key := ackMessageKey{uid: route.UID, sessionID: route.SessionID, messageID: push.Event.MessageID}
		_, duplicates[i] = seen[key]
		seen[key] = struct{}{}
	}
	bind := r.bindPendingAckBatch(pendings)
	recoveryPending = pendings
	recoveryTokens = bind.Tokens
	acceptedIndexes := make([]int, 0, bind.Bound)
	rollbackCount := 0
	for i, route := range push.Routes {
		if err := ctx.Err(); err != nil {
			if i < len(bind.Tokens) && r.rollbackPendingAck(pendings[i], bind.Tokens[i]) {
				rollbackCount++
			}
			result.Retryable = append(result.Retryable, route)
			setOwnerPushFailure(&failure, route, err)
			continue
		}
		pending := pendings[i]
		if pending.UID == "" || i >= len(bind.Tokens) || !bind.Tokens[i].Valid() {
			result.Dropped = append(result.Dropped, route)
			setOwnerPushFailure(&failure, route, ErrInvalidPlan)
			continue
		}
		token := bind.Tokens[i]
		// Duplicate rows intentionally produce duplicate writes, but they share
		// one protocol-visible recvack identity. Refresh later reservations just
		// before their write so a fast ACK from an earlier duplicate cannot
		// consume the later write's pending state.
		if duplicates[i] {
			if r.rollbackPendingAck(pending, token) {
				rollbackCount++
			}
			refreshed := r.bindPendingAckResult(pending)
			if !refreshed.Bound {
				result.Dropped = append(result.Dropped, route)
				setOwnerPushFailure(&failure, route, ErrInvalidPlan)
				continue
			}
			token = refreshed.Token
			bind.Tokens[i] = token
		}
		writeResult := SessionWriteResult{Disposition: SessionWriteRetryable, Err: ErrSessionWriterUnavailable}
		if r.sessionWriter != nil {
			writeResult = r.writeSessionSafely(ctx, LocalSessionWrite{Event: push.Event, Route: route})
		}
		switch writeResult.Disposition {
		case SessionWriteAccepted:
			acceptedIndexes = append(acceptedIndexes, i)
			result.Accepted = append(result.Accepted, route)
		case SessionWriteRetryable:
			if r.rollbackPendingAck(pending, token) {
				rollbackCount++
			}
			result.Retryable = append(result.Retryable, route)
			setOwnerPushFailure(&failure, route, writeResult.Err)
		default:
			if r.rollbackPendingAck(pending, token) {
				rollbackCount++
			}
			result.Dropped = append(result.Dropped, route)
			setOwnerPushFailure(&failure, route, writeResult.Err)
		}
	}
	r.finishPendingAckBatch(pendings, bind.Tokens, acceptedIndexes, rollbackCount)
	recoveryPending = nil
	recoveryTokens = nil
	return result, nil
}

func (r *Runtime) observeOwnerPushResult(
	push onlinedelivery.OwnerPush,
	result onlinedelivery.OwnerPushResult,
	err error,
	failure OwnerPushFailureSample,
	started time.Time,
) {
	label := ObservationResultOK
	retryable := len(result.Retryable)
	if err != nil {
		label = ObservationResultError
		retryable = len(push.Routes)
	} else if retryable > 0 {
		label = ObservationResultRetryable
	} else if len(result.Dropped) > 0 {
		label = ObservationResultDropped
	}
	r.observeOwnerPush(OwnerPushEvent{
		OwnerNodeID: push.OwnerNodeID,
		Result:      label,
		Routes:      len(push.Routes),
		Accepted:    len(result.Accepted),
		Retryable:   retryable,
		Dropped:     len(result.Dropped),
		Duration:    positiveRuntimeDuration(time.Since(started)),
		Failure:     failure,
	})
}

func setOwnerPushFailure(sample *OwnerPushFailureSample, route onlinedelivery.Route, err error) {
	if sample == nil || sample.Route.UID != "" || sample.Route.SessionID != 0 || sample.Err != nil {
		return
	}
	sample.Route = route
	sample.Err = err
}

func (r *Runtime) writeSessionSafely(ctx context.Context, write LocalSessionWrite) (result SessionWriteResult) {
	defer func() {
		if recover() != nil {
			result = SessionWriteResult{Disposition: SessionWriteRetryable, Err: ErrOwnerPushPanic}
		}
	}()
	return r.sessionWriter.WriteSession(ctx, write)
}

// Recvack clears one exact pending identity and ignores unknown feedback.
func (r *Runtime) Recvack(_ context.Context, cmd Recvack) error {
	if r == nil || r.acks == nil {
		return nil
	}
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	_, ok := r.acks.Ack(cmd)
	result := DeliveryAckResultMiss
	changed := 0
	if ok {
		result = DeliveryAckResultOK
		changed = 1
	}
	r.observeAck(DeliveryAckActionAck, result, changed)
	return nil
}

// SessionClosed clears all pending identities for one exact local session.
func (r *Runtime) SessionClosed(_ context.Context, cmd SessionClosed) error {
	if r == nil || r.acks == nil {
		return nil
	}
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	removed := r.acks.SessionClosed(cmd.UID, cmd.SessionID)
	result := DeliveryAckResultNoop
	if len(removed) > 0 {
		result = DeliveryAckResultOK
	}
	r.observeAck(DeliveryAckActionSessionClosed, result, len(removed))
	return nil
}

func (r *Runtime) bindPendingAckResult(pending PendingRecvAck) AckBindResult {
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	result := r.acks.BindResult(pending)
	label := DeliveryAckResultRejected
	changed := 0
	if result.Bound {
		label = DeliveryAckResultOK
		if result.Added {
			changed = 1
		}
	}
	r.observeAck(DeliveryAckActionBind, label, changed)
	return result
}

func (r *Runtime) bindPendingAckBatch(pending []PendingRecvAck) AckBindBatchResult {
	started := time.Now()
	r.ackMu.Lock()
	result := r.acks.BindBatch(pending)
	label := DeliveryAckResultRejected
	if result.Bound > 0 {
		label = DeliveryAckResultOK
	}
	r.observeAck(DeliveryAckActionBind, label, result.Added)
	r.ackMu.Unlock()
	rejected := len(pending) - result.Bound
	r.observeAckBatch(AckBatchEvent{
		Phase: DeliveryAckBatchPhaseBind, Outcome: bindAckBatchOutcome(result.Bound, rejected),
		Items: len(pending), Shards: result.Shards, Rejected: rejected,
		Duration: positiveRuntimeDuration(time.Since(started)),
	})
	return result
}

func (r *Runtime) rollbackPendingAck(pending PendingRecvAck, token AckBindToken) bool {
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	result := r.acks.CancelBind(pending, token)
	label := DeliveryAckResultMiss
	changed := 0
	if result.Canceled {
		label = DeliveryAckResultOK
		if result.Removed {
			changed = 1
		}
	}
	r.observeAck(DeliveryAckActionRollback, label, changed)
	return result.Canceled
}

func (r *Runtime) finishPendingAckBatch(pending []PendingRecvAck, tokens []AckBindToken, indexes []int, rollback int) int {
	started := time.Now()
	finished, shards, selected := r.acks.finishBindBatch(pending, tokens, indexes)
	bound := countBoundAckTokens(pending, tokens)
	rejected := len(pending) - bound
	r.observeAckBatch(AckBatchEvent{
		Phase: DeliveryAckBatchPhaseFinish, Outcome: finishAckBatchOutcome(finished, selected, rejected, rollback),
		Items: len(pending), Shards: shards, Rejected: rejected, Rollback: rollback,
		Duration: positiveRuntimeDuration(time.Since(started)),
	})
	return finished
}

func (r *Runtime) expirePendingAcks() []PendingRecvAck {
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	removed := r.acks.Expire(r.pendingAckTTL)
	label := DeliveryAckResultNoop
	if len(removed) > 0 {
		label = DeliveryAckResultOK
	}
	r.observeAck(DeliveryAckActionExpire, label, len(removed))
	return removed
}

func (r *Runtime) expirePendingAcksIfDue() {
	if r.pendingAckTTL <= 0 {
		return
	}
	now := time.Now()
	for {
		next := r.pendingAckExpiryNext.Load()
		if now.UnixNano() < next {
			return
		}
		if r.pendingAckExpiryNext.CompareAndSwap(next, now.Add(runtimeAckExpiryInterval).UnixNano()) {
			break
		}
	}
	r.expirePendingAcks()
}

// PendingAckCount returns owner-local pending feedback state for diagnostics.
func (r *Runtime) PendingAckCount() int {
	if r == nil || r.acks == nil {
		return 0
	}
	return r.acks.PendingCount()
}

func (r *Runtime) observeAck(action, result string, changed int) {
	r.observeAckEvent(AckEvent{
		Action: action, Result: result, Changed: changed, PendingCount: r.acks.PendingCount(),
	})
}

func (r *Runtime) observeAckEvent(event AckEvent) {
	if r.ackObserver == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.ackObserver.ObserveAck(event)
}

func (r *Runtime) observeAckBatch(event AckBatchEvent) {
	if r.ackBatchObserver == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.ackBatchObserver.ObserveAckBatch(event)
}

func (r *Runtime) notifyOfflineSafely(ctx context.Context, plan onlinedelivery.RecipientDeliveryPlan, uids []string) {
	if plan.Mode != onlinedelivery.ModeDurable || r.offlineRecipientsObserver == nil || len(uids) == 0 {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.offlineRecipientsObserver.ObserveOfflineRecipients(ctx, OfflineRecipientsEvent{
		Event: plan.Event,
		UIDs:  append([]string(nil), uids...),
	})
}

func appendOfflineUIDs(out []string, seen map[string]struct{}, target onlinedelivery.RecipientTargetBatch, routes []onlinedelivery.Route) []string {
	online := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		online[route.UID] = struct{}{}
	}
	for _, recipient := range target.Recipients {
		if _, ok := online[recipient.UID]; ok {
			continue
		}
		if _, ok := seen[recipient.UID]; ok {
			continue
		}
		seen[recipient.UID] = struct{}{}
		out = append(out, recipient.UID)
	}
	return out
}

func suppressSenderRoute(plan onlinedelivery.RecipientDeliveryPlan, route onlinedelivery.Route) bool {
	return plan.Event.FromUID != "" &&
		plan.Event.SenderNodeID != 0 &&
		plan.Event.SenderSessionID != 0 &&
		route.UID == plan.Event.FromUID &&
		route.OwnerNodeID == plan.Event.SenderNodeID &&
		route.SessionID == plan.Event.SenderSessionID
}

// runBoundedRuntime executes fixed indexed work with a caller-owned worker and
// at most concurrency-1 cataloged burst goroutines.
func runBoundedRuntime(registry *goruntimeregistry.Registry, count, concurrency int, run func(int)) {
	if count == 0 {
		return
	}
	if concurrency <= 1 || count == 1 {
		for i := 0; i < count; i++ {
			run(i)
		}
		return
	}
	if concurrency > count {
		concurrency = count
	}
	var next atomic.Int64
	var workers sync.WaitGroup
	worker := func() {
		for {
			index := int(next.Add(1) - 1)
			if index >= count {
				return
			}
			run(index)
		}
	}
	workers.Add(concurrency - 1)
	for i := 1; i < concurrency; i++ {
		goruntimeregistry.SafeGo(registry, goruntimeregistry.TaskOnlineDeliveryOwnerPush, func() {
			defer workers.Done()
			worker()
		})
	}
	worker()
	workers.Wait()
}

func (r *Runtime) observePressure() {
	if r == nil || r.observer == nil {
		return
	}
	r.pressureMu.Lock()
	defer r.pressureMu.Unlock()
	defer func() {
		_ = recover()
	}()
	r.observer.SetRuntimePressure(RuntimePressureEvent{
		QueueDepth: len(r.queue), QueueCapacity: cap(r.queue),
		Inflight: int(r.inflight.Load()), Workers: r.workers,
	})
}

func (r *Runtime) observePlanAdmission(event PlanAdmissionEvent) {
	if r == nil || r.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.observer.ObservePlanAdmission(event)
}

func (r *Runtime) observePlanTerminal(event PlanTerminalEvent) {
	if r == nil || r.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.observer.ObservePlanTerminal(event)
}

func (r *Runtime) observeOwnerPush(event OwnerPushEvent) {
	if r == nil || r.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.observer.ObserveOwnerPush(event)
}

// waitClosed completes the exact generation observed by the Stop caller.
func (r *Runtime) waitClosed(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		r.finishClosedForDone(done)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finishClosedForDone finalizes only the generation owning done, preventing a
// late lifecycle goroutine from resetting a subsequently restarted runtime.
func (r *Runtime) finishClosedForDone(done <-chan struct{}) {
	r.mu.Lock()
	resetRemoved := -1
	if r.state == runtimeClosing && r.done == done {
		resetRemoved = r.finishClosedLocked()
	}
	r.mu.Unlock()
	if resetRemoved >= 0 {
		r.observeAckReset(resetRemoved)
		r.observePressure()
	}
}

// finishClosedIfDoneLocked lets Start or Stop finalize a generation whose
// workers exited before that lifecycle call acquired the runtime lock.
func (r *Runtime) finishClosedIfDoneLocked() (bool, int) {
	if r.done == nil {
		return true, r.finishClosedLocked()
	}
	select {
	case <-r.done:
		return true, r.finishClosedLocked()
	default:
		return false, 0
	}
}

// finishClosedLocked clears generation handles and transient ACK state after
// all work that could mutate the current generation has exited.
func (r *Runtime) finishClosedLocked() int {
	r.state = runtimeClosed
	r.acceptDone = nil
	r.stopReady = nil
	if r.runCancel != nil {
		r.runCancel()
		r.runCancel = nil
	}
	r.done = nil
	r.pendingAckExpiryNext.Store(0)
	r.ackMu.Lock()
	removed := r.acks.PendingCount()
	r.acks.Reset()
	r.ackMu.Unlock()
	return removed
}

func (r *Runtime) observeAckReset(removed int) {
	result := DeliveryAckResultNoop
	if removed > 0 {
		result = DeliveryAckResultOK
	}
	r.observeAckEvent(AckEvent{
		Action: DeliveryAckActionReset, Result: result, Changed: removed, PendingCount: 0,
	})
}

func boundedRuntimePositive(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func positiveRuntimeDuration(value time.Duration) time.Duration {
	if value <= 0 {
		return time.Nanosecond
	}
	return value
}

func runtimeResultForContext(err error) ObservationResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return ObservationResultTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ObservationResultCanceled
	}
	return ObservationResultError
}

func runtimeResultForError(err error) ObservationResult {
	if err == nil {
		return ObservationResultOK
	}
	if errors.Is(err, ErrOwnerPushRetryExhausted) {
		return ObservationResultRetryExhausted
	}
	return runtimeResultForContext(err)
}
