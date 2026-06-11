package channelappend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRecipientDeliveryWorkerRejectsBeforeStartAndAfterStop(t *testing.T) {
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{}),
		QueueSize: 1,
		Workers:   1,
	})
	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}

	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); !errors.Is(err, ErrRecipientDeliveryWorkerClosed) {
		t.Fatalf("EnqueueRecipientBatch() before Start error = %v, want ErrRecipientDeliveryWorkerClosed", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); !errors.Is(err, ErrRecipientDeliveryWorkerClosed) {
		t.Fatalf("EnqueueRecipientBatch() after Stop error = %v, want ErrRecipientDeliveryWorkerClosed", err)
	}
}

func TestRecipientDeliveryWorkerProcessesAcceptedBatches(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u1", OwnerNodeID: 1, SessionID: 10}}},
			OwnerPusher:      pusher,
		}),
		QueueSize: 2,
		Workers:   1,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("EnqueueRecipientBatch() error = %v", err)
	}

	waitDeliveryWorkerCondition(t, func() bool { return pusher.callCount() == 1 })
}

func TestRecipientDeliveryWorkerFullQueueReturnsContextError(t *testing.T) {
	blocker := newBlockingPresenceResolverForDeliveryWorkerTest()
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      &recordingOwnerPusherForDeliveryTest{},
		}),
		QueueSize: 1,
		Workers:   1,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		blocker.release()
		_ = worker.Stop(context.Background())
	})

	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("first EnqueueRecipientBatch() error = %v", err)
	}
	blocker.waitStarted(t)
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("second EnqueueRecipientBatch() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := worker.EnqueueRecipientBatch(ctx, recipientAuthorityTargetForTest(1, 1, 1), batch)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third EnqueueRecipientBatch() error = %v, want context deadline", err)
	}
}

func TestRecipientDeliveryWorkerObservesQueueAdmissionAndProcessing(t *testing.T) {
	blocker := newBlockingPresenceResolverForDeliveryWorkerTest()
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      &recordingOwnerPusherForDeliveryTest{},
		}),
		QueueSize: 1,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		blocker.release()
		_ = worker.Stop(context.Background())
	})

	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}}}
	target := recipientAuthorityTargetForTest(1, 1, 1)
	if err := worker.EnqueueRecipientBatch(context.Background(), target, batch); err != nil {
		t.Fatalf("first EnqueueRecipientBatch() error = %v", err)
	}
	blocker.waitStarted(t)
	if err := worker.EnqueueRecipientBatch(context.Background(), target, batch); err != nil {
		t.Fatalf("second EnqueueRecipientBatch() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := worker.EnqueueRecipientBatch(ctx, target, batch)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third EnqueueRecipientBatch() error = %v, want context deadline", err)
	}

	observer.waitAdmissions(t, 3)
	if got := observer.admissions[0]; got.Result != "accepted" || got.QueueCapacity != 1 {
		t.Fatalf("first admission = %+v, want accepted capacity=1", got)
	}
	if got := observer.admissions[1]; got.Result != "accepted" || got.QueueDepth != 1 || got.QueueCapacity != 1 {
		t.Fatalf("second admission = %+v, want accepted depth=1 capacity=1", got)
	}
	if got := observer.admissions[2]; got.Result != "timeout" || got.Duration <= 0 {
		t.Fatalf("third admission = %+v, want timeout with duration", got)
	}
	if got := observer.lastQueue(t); got.QueueCapacity != 1 {
		t.Fatalf("queue observation = %+v, want capacity=1", got)
	}

	blocker.release()
	observer.waitProcesses(t, 2)
	if got := observer.processes[0]; got.Result != "ok" || got.Recipients != 2 || got.Duration <= 0 {
		t.Fatalf("first process observation = %+v, want ok recipients=2 duration", got)
	}
}

func TestRecipientDeliveryWorkerStopDrainsAcceptedBatches(t *testing.T) {
	blocker := newBlockingPresenceResolverForDeliveryWorkerTest()
	pusher := &recordingOwnerPusherForDeliveryTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      pusher,
		}),
		QueueSize: 2,
		Workers:   1,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("first EnqueueRecipientBatch() error = %v", err)
	}
	blocker.waitStarted(t)
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("second EnqueueRecipientBatch() error = %v", err)
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- worker.Stop(context.Background()) }()
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before blocked batch was released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	blocker.release()
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not drain accepted batches")
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want both accepted batches drained", got)
	}
}

func TestRecipientDeliveryWorkerObservesProcessingFailures(t *testing.T) {
	observer := &recordingPostCommitFailureObserverForTest{}
	presenceErr := errors.New("presence unavailable")
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: errorPresenceResolverForDeliveryWorkerTest{err: presenceErr},
			OwnerPusher:      &recordingOwnerPusherForDeliveryTest{},
		}),
		QueueSize: 1,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	batch := RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}},
	}
	target := recipientAuthorityTargetForTest(7, 3, 99)
	if err := worker.EnqueueRecipientBatch(context.Background(), target, batch); err != nil {
		t.Fatalf("EnqueueRecipientBatch() error = %v", err)
	}

	observer.waitFailures(t, 1)
	got := observer.failures[0]
	if got.MessageID != 10 || got.MessageSeq != 4 || got.Phase != "presence_resolve" || got.UID != "u1" ||
		got.TargetHashSlot != target.HashSlot || got.TargetLeaderNodeID != target.LeaderNodeID {
		t.Fatalf("post-commit failure observation = %#v, want presence failure with target detail", got)
	}
}

func TestRecipientDeliveryWorkerRecoversProcessorPanic(t *testing.T) {
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: &panicOncePresenceResolverForDeliveryWorkerTest{
				next: &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u3", OwnerNodeID: 1, SessionID: 10}}},
			},
			OwnerPusher: pusher,
		}),
		QueueSize: 2,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	batch := RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 11, MessageSeq: 5, ChannelID: "g2", ChannelType: 2},
		Recipients: []Recipient{{UID: "u2"}},
	}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(8, 4, 100), batch); err != nil {
		t.Fatalf("EnqueueRecipientBatch() error = %v", err)
	}

	observer.waitFailures(t, 1)
	got := observer.failures[0]
	if got.MessageID != 11 || got.Phase != "panic" || !errors.Is(got.Err, ErrEffectPanic) {
		t.Fatalf("post-commit panic observation = %#v, want panic failure", got)
	}
	if got.UID != "u2" || got.UIDCount != 1 || got.RecipientCount != 1 {
		t.Fatalf("post-commit panic recipient detail = %#v, want u2 batch detail", got)
	}
	observer.waitProcesses(t, 1)
	if got := observer.processes[0]; got.Result != "panic" || got.Recipients != 1 {
		t.Fatalf("panic process observation = %+v, want panic recipients=1", got)
	}

	next := RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 12, MessageSeq: 6, ChannelID: "g2", ChannelType: 2},
		Recipients: []Recipient{{UID: "u3"}},
	}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(8, 4, 100), next); err != nil {
		t.Fatalf("second EnqueueRecipientBatch() error = %v", err)
	}
	waitDeliveryWorkerCondition(t, func() bool { return pusher.callCount() == 1 })
}

type recordingRecipientDeliveryWorkerObserverForTest struct {
	mu         sync.Mutex
	failures   []PostCommitFailureObservation
	queues     []RecipientDeliveryQueueObservation
	admissions []RecipientDeliveryAdmissionObservation
	processes  []RecipientDeliveryProcessObservation
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) AppendFinished(string, error, time.Duration) {
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) ObserveChannelAppendPostCommitFailure(obs PostCommitFailureObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = append(o.failures, obs)
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) SetChannelAppendRecipientDeliveryQueue(obs RecipientDeliveryQueueObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queues = append(o.queues, obs)
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) ObserveChannelAppendRecipientDeliveryAdmission(obs RecipientDeliveryAdmissionObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.admissions = append(o.admissions, obs)
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) ObserveChannelAppendRecipientDeliveryProcess(obs RecipientDeliveryProcessObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.processes = append(o.processes, obs)
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) waitFailures(t *testing.T, want int) {
	t.Helper()
	waitDeliveryWorkerCondition(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		return len(o.failures) >= want
	})
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) waitAdmissions(t *testing.T, want int) {
	t.Helper()
	waitDeliveryWorkerCondition(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		return len(o.admissions) >= want
	})
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) waitProcesses(t *testing.T, want int) {
	t.Helper()
	waitDeliveryWorkerCondition(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		return len(o.processes) >= want
	})
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) lastQueue(t *testing.T) RecipientDeliveryQueueObservation {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.queues) == 0 {
		t.Fatalf("no queue observations")
	}
	return o.queues[len(o.queues)-1]
}

type errorPresenceResolverForDeliveryWorkerTest struct {
	err error
}

func (r errorPresenceResolverForDeliveryWorkerTest) EndpointsByUIDs(context.Context, []string) ([]Route, error) {
	return nil, r.err
}

type panicOncePresenceResolverForDeliveryWorkerTest struct {
	once sync.Once
	next PresenceResolver
}

func (r *panicOncePresenceResolverForDeliveryWorkerTest) EndpointsByUIDs(ctx context.Context, uids []string) ([]Route, error) {
	panicked := false
	r.once.Do(func() {
		panicked = true
		panic("delivery worker panic")
	})
	if panicked {
		return nil, nil
	}
	return r.next.EndpointsByUIDs(ctx, uids)
}

type blockingPresenceResolverForDeliveryWorkerTest struct {
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
}

func newBlockingPresenceResolverForDeliveryWorkerTest() *blockingPresenceResolverForDeliveryWorkerTest {
	return &blockingPresenceResolverForDeliveryWorkerTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *blockingPresenceResolverForDeliveryWorkerTest) EndpointsByUIDs(ctx context.Context, uids []string) ([]Route, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.releaseC:
		routes := make([]Route, 0, len(uids))
		for _, uid := range uids {
			routes = append(routes, Route{UID: uid, OwnerNodeID: 1, SessionID: 10})
		}
		return routes, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *blockingPresenceResolverForDeliveryWorkerTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("presence resolver did not start")
	}
}

func (r *blockingPresenceResolverForDeliveryWorkerTest) release() {
	select {
	case <-r.releaseC:
	default:
		close(r.releaseC)
	}
}

func waitDeliveryWorkerCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before deadline")
}
