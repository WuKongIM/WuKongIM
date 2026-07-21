package channelappend

import (
	"context"
	"errors"
	"reflect"
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

func TestRecipientDeliveryWorkerPreservesTargetsAndContinuesPartialPlan(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	presenceErr := errors.New("first target unavailable")
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
		{Err: presenceErr},
		{Routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
	}}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: resolver,
			OwnerPusher:      pusher,
		}),
		QueueSize: 1,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	plan := RecipientDeliveryPlan{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Targets: []RecipientTargetBatch{
			{Target: first, Recipients: []Recipient{{UID: "u1"}}},
			{Target: second, Recipients: []Recipient{{UID: "u2"}}},
		},
	}
	if err := worker.EnqueueRecipientDeliveryPlan(context.Background(), plan); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}

	observer.waitFailures(t, 1)
	waitDeliveryWorkerCondition(t, func() bool { return pusher.callCount() == 1 })
	if resolver.legacyCalls != 0 || resolver.targetCalls != 1 {
		t.Fatalf("presence calls legacy/target = %d/%d, want 0/1", resolver.legacyCalls, resolver.targetCalls)
	}
	if !reflect.DeepEqual(resolver.targets, []RecipientAuthorityTarget{first, second}) {
		t.Fatalf("resolved targets = %#v, want exact queued targets", resolver.targets)
	}
	got := observer.failures[0]
	if got.TargetHashSlot != first.HashSlot || got.TargetLeaderNodeID != first.LeaderNodeID || !errors.Is(got.Err, presenceErr) {
		t.Fatalf("first target failure = %#v, want exact first target and error", got)
	}
}

func TestRecipientDeliveryWorkerIsolatesTargetPanicAndContinuesSiblingGroups(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	third := recipientAuthorityTargetForTest(3, 30, 300)
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
		{Routes: []Route{{UID: "u1", OwnerNodeID: 1, SessionID: 10}}},
		{Routes: []Route{{UID: "u2", OwnerNodeID: 2, SessionID: 20}}},
		{Routes: []Route{{UID: "u3", OwnerNodeID: 3, SessionID: 30}}},
	}}
	pusher := &selectivePanicOwnerPusherForDeliveryWorkerTest{panicOwnerNodeID: 2}
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: resolver,
			OwnerPusher:      pusher,
		}),
		QueueSize: 1,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	plan := RecipientDeliveryPlan{
		Event: CommittedEnvelope{MessageID: 20, MessageSeq: 5, ChannelID: "g2", ChannelType: 2},
		Targets: []RecipientTargetBatch{
			{Target: first, Recipients: []Recipient{{UID: "u1"}}},
			{Target: second, Recipients: []Recipient{{UID: "u2"}}},
			{Target: third, Recipients: []Recipient{{UID: "u3"}}},
		},
	}
	if err := worker.EnqueueRecipientDeliveryPlan(context.Background(), plan); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}

	observer.waitFailures(t, 1)
	failure := observer.failures[0]
	if failure.TargetHashSlot != second.HashSlot || failure.TargetLeaderNodeID != second.LeaderNodeID || !errors.Is(failure.Err, ErrEffectPanic) {
		t.Fatalf("panic failure = %#v, want exact second target", failure)
	}
	waitDeliveryWorkerCondition(t, func() bool {
		owners := pusher.ownerNodeIDs()
		if len(owners) != 3 {
			return false
		}
		seen := make(map[uint64]int, len(owners))
		for _, ownerNodeID := range owners {
			seen[ownerNodeID]++
		}
		return seen[1] == 1 && seen[2] == 1 && seen[3] == 1
	})
	observer.waitProcesses(t, 1)
	if got := observer.processes[0]; got.Result != recipientDeliveryResultPanic || got.Recipients != 3 {
		t.Fatalf("panic process observation = %+v, want panic recipients=3", got)
	}
}

func TestRecipientDeliveryWorkerSharesImmutableEnvelopeWithOwnerPush(t *testing.T) {
	payload := []byte("payload")
	pusher := &payloadAliasOwnerPusherForDeliveryTest{payload: payload}
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

	batch := RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 1, Payload: payload},
		Recipients: []Recipient{{UID: "u1"}},
	}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("EnqueueRecipientBatch() error = %v", err)
	}

	waitDeliveryWorkerCondition(t, func() bool { return pusher.callCount() == 1 })
	if !pusher.sawAlias() {
		t.Fatalf("owner push envelope payload did not share immutable committed envelope payload")
	}
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

func TestRecipientDeliveryWorkerObservesConcurrentInflightAndReturnsToZero(t *testing.T) {
	blocker := newConcurrentBlockingPresenceResolverForDeliveryWorkerTest(2)
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      &recordingOwnerPusherForDeliveryTest{},
		}),
		QueueSize: 2,
		Workers:   2,
		Observer:  observer,
	})
	if got := worker.WorkerCapacity(); got != 2 {
		t.Fatalf("WorkerCapacity() = %d, want 2", got)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}
	target := recipientAuthorityTargetForTest(1, 1, 1)
	for i := 0; i < 2; i++ {
		if err := worker.EnqueueRecipientBatch(context.Background(), target, batch); err != nil {
			t.Fatalf("EnqueueRecipientBatch(%d) error = %v", i, err)
		}
	}
	blocker.waitStarted(t)
	observer.waitWorkerPressure(t, func(obs RecipientDeliveryWorkerPressureObservation) bool {
		return obs.Inflight == 2 && obs.Capacity == 2
	})

	blocker.release()
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := observer.lastWorkerPressure(t); got.Inflight != 0 || got.Capacity != 2 {
		t.Fatalf("final worker pressure = %+v, want inflight=0 capacity=2", got)
	}
	if got := observer.lastQueue(t); got.QueueDepth != 0 || got.QueueCapacity != 2 {
		t.Fatalf("final queue pressure = %+v, want depth=0 capacity=2", got)
	}
}

func TestRecipientDeliveryWorkerPlanDeadlineCancelsStalledProcessor(t *testing.T) {
	blocker := newBlockingPresenceResolverForDeliveryWorkerTest()
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      &recordingOwnerPusherForDeliveryTest{},
		}),
		QueueSize:   1,
		Workers:     1,
		PlanTimeout: 20 * time.Millisecond,
		Observer:    observer,
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
		t.Fatalf("EnqueueRecipientBatch() error = %v", err)
	}
	blocker.waitStarted(t)
	observer.waitFailures(t, 1)
	observer.waitProcesses(t, 1)

	if got := observer.failures[0]; !errors.Is(got.Err, context.DeadlineExceeded) {
		t.Fatalf("processing failure = %v, want context deadline exceeded", got.Err)
	}
	if got := observer.processes[0]; got.Result != recipientDeliveryResultTimeout {
		t.Fatalf("process result = %q, want %q", got.Result, recipientDeliveryResultTimeout)
	}
}

func TestRecipientDeliveryWorkerStopCancelsAcceptedBatches(t *testing.T) {
	blocker := newBlockingPresenceResolverForDeliveryWorkerTest()
	pusher := &recordingOwnerPusherForDeliveryTest{}
	observer := &recordingRecipientDeliveryWorkerObserverForTest{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		Processor: NewRecipientProcessor(RecipientProcessorOptions{
			PresenceResolver: blocker,
			OwnerPusher:      pusher,
		}),
		QueueSize: 2,
		Workers:   1,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(blocker.release)
	batch := RecipientBatch{Event: CommittedEnvelope{MessageID: 1}, Recipients: []Recipient{{UID: "u1"}}}
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("first EnqueueRecipientBatch() error = %v", err)
	}
	blocker.waitStarted(t)
	if err := worker.EnqueueRecipientBatch(context.Background(), recipientAuthorityTargetForTest(1, 1, 1), batch); err != nil {
		t.Fatalf("second EnqueueRecipientBatch() error = %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := pusher.callCount(); got != 0 {
		t.Fatalf("push calls = %d, want canceled plans to stop before owner push", got)
	}
	if len(observer.processes) != 2 {
		t.Fatalf("process observations = %d, want both accepted batches terminated", len(observer.processes))
	}
	for i, got := range observer.processes {
		if got.Result != recipientDeliveryResultCanceled {
			t.Fatalf("process[%d] result = %q, want %q", i, got.Result, recipientDeliveryResultCanceled)
		}
	}
	if len(observer.failures) != 2 {
		t.Fatalf("processing failures = %d, want both accepted batches observed", len(observer.failures))
	}
	for i, got := range observer.failures {
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("failure[%d] = %v, want context canceled", i, got.Err)
		}
	}
}

func TestRecipientDeliveryWorkerStopWaitsForSenderPastAdmissionGate(t *testing.T) {
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

	// Model the exact interleaving after Enqueue crosses the open-state gate but
	// before its channel send. Stop must not let workers exit during this window.
	worker.mu.Lock()
	worker.admissionSenders.Add(1)
	queue := worker.queue
	worker.mu.Unlock()
	senderReleased := false
	t.Cleanup(func() {
		if !senderReleased {
			worker.admissionSenders.Done()
		}
		blocker.release()
		_ = worker.Stop(context.Background())
	})

	stopResult := make(chan error, 1)
	go func() { stopResult <- worker.Stop(context.Background()) }()
	waitDeliveryWorkerCondition(t, func() bool {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return worker.state == recipientDeliveryWorkerClosing
	})
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned while an admitted sender was active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	queue <- recipientDeliveryCommand{plan: RecipientDeliveryPlan{
		Event: CommittedEnvelope{MessageID: 99},
		Targets: []RecipientTargetBatch{{
			Target:     recipientAuthorityTargetForTest(1, 1, 1),
			Recipients: []Recipient{{UID: "u1"}},
		}},
	}}
	observer.waitProcesses(t, 1)
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before admitted sender left Enqueue: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	worker.admissionSenders.Done()
	senderReleased = true
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not finish after admitted sender left Enqueue")
	}
	if len(worker.queue) != 0 {
		t.Fatalf("queue depth after Stop = %d, want 0", len(worker.queue))
	}
	if got := observer.failures[0]; got.MessageID != 99 || !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("admitted command failure = %#v, want canceled message 99", got)
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

func TestPostCommitFailureDetailBuildsObservationWithFallback(t *testing.T) {
	event := CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2}
	detail := PostCommitFailureDetail{
		Phase:               "owner_push",
		UID:                 "u1",
		RecipientCount:      2,
		DispatchBatchSize:   2,
		DispatchOwnerNodeID: 7,
	}
	fallback := PostCommitFailureDetail{
		TargetHashSlot:       8,
		TargetSlotID:         4,
		TargetLeaderNodeID:   3,
		TargetRouteRevision:  99,
		TargetAuthorityEpoch: 100,
	}
	err := errors.New("push failed")

	got := detail.withFallback(fallback).toObservation(event, 5, "error", err)

	if got.ChannelID != "g1" || got.ChannelType != 2 || got.MessageID != 10 || got.MessageSeq != 4 ||
		got.Attempt != 5 || got.Result != "error" || got.Phase != "owner_push" || got.UID != "u1" ||
		got.RecipientCount != 2 || got.DispatchBatchSize != 2 || got.DispatchOwnerNodeID != 7 ||
		got.TargetHashSlot != 8 || got.TargetSlotID != 4 || got.TargetLeaderNodeID != 3 ||
		got.TargetRouteRevision != 99 || got.TargetAuthorityEpoch != 100 || got.Err != err {
		t.Fatalf("observation = %#v, want detail mapped with fallback", got)
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
	workers    []RecipientDeliveryWorkerPressureObservation
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

func (o *recordingRecipientDeliveryWorkerObserverForTest) SetChannelAppendRecipientDeliveryWorkerPressure(obs RecipientDeliveryWorkerPressureObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workers = append(o.workers, obs)
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

func (o *recordingRecipientDeliveryWorkerObserverForTest) waitWorkerPressure(t *testing.T, match func(RecipientDeliveryWorkerPressureObservation) bool) {
	t.Helper()
	waitDeliveryWorkerCondition(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, obs := range o.workers {
			if match(obs) {
				return true
			}
		}
		return false
	})
}

func (o *recordingRecipientDeliveryWorkerObserverForTest) lastWorkerPressure(t *testing.T) RecipientDeliveryWorkerPressureObservation {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.workers) == 0 {
		t.Fatalf("no worker pressure observations")
	}
	return o.workers[len(o.workers)-1]
}

type recordingTargetPresenceResolverForDeliveryWorkerTest struct {
	legacyCalls int
	targetCalls int
	targets     []RecipientAuthorityTarget
	results     []RecipientTargetPresenceResult
}

type selectivePanicOwnerPusherForDeliveryWorkerTest struct {
	mu               sync.Mutex
	panicOwnerNodeID uint64
	owners           []uint64
}

func (p *selectivePanicOwnerPusherForDeliveryWorkerTest) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.mu.Lock()
	p.owners = append(p.owners, cmd.OwnerNodeID)
	panicOwnerNodeID := p.panicOwnerNodeID
	p.mu.Unlock()
	if cmd.OwnerNodeID == panicOwnerNodeID {
		panic("target owner push panic")
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

func (p *selectivePanicOwnerPusherForDeliveryWorkerTest) ownerNodeIDs() []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint64(nil), p.owners...)
}

func (r *recordingTargetPresenceResolverForDeliveryWorkerTest) EndpointsByUIDs(context.Context, []string) ([]Route, error) {
	r.legacyCalls++
	return nil, nil
}

func (r *recordingTargetPresenceResolverForDeliveryWorkerTest) EndpointsByTargets(_ context.Context, batches []RecipientTargetBatch) []RecipientTargetPresenceResult {
	r.targetCalls++
	r.targets = make([]RecipientAuthorityTarget, len(batches))
	for i, batch := range batches {
		r.targets[i] = batch.Target
	}
	results := make([]RecipientTargetPresenceResult, len(r.results))
	copy(results, r.results)
	for i := range results {
		results[i].Routes = append([]Route(nil), results[i].Routes...)
	}
	return results
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

type concurrentBlockingPresenceResolverForDeliveryWorkerTest struct {
	want     int
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
	mu       sync.Mutex
	count    int
}

func newConcurrentBlockingPresenceResolverForDeliveryWorkerTest(want int) *concurrentBlockingPresenceResolverForDeliveryWorkerTest {
	return &concurrentBlockingPresenceResolverForDeliveryWorkerTest{
		want:     want,
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (r *concurrentBlockingPresenceResolverForDeliveryWorkerTest) EndpointsByUIDs(ctx context.Context, uids []string) ([]Route, error) {
	r.mu.Lock()
	r.count++
	if r.count == r.want {
		close(r.started)
	}
	r.mu.Unlock()
	select {
	case <-r.releaseC:
		return []Route{{UID: uids[0], OwnerNodeID: 1, SessionID: 10}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *concurrentBlockingPresenceResolverForDeliveryWorkerTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("concurrent presence resolver did not reach expected calls")
	}
}

func (r *concurrentBlockingPresenceResolverForDeliveryWorkerTest) release() {
	r.once.Do(func() { close(r.releaseC) })
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
