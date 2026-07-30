package delivery

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func TestRuntimePlanAdmissionTransfersSharedImmutableStorage(t *testing.T) {
	resolver := &capturingPlanPresenceResolver{
		result: []TargetPresenceResult{{Routes: []onlinedelivery.Route{runtimeRouteForTest()}}},
		called: make(chan struct{}, 1),
	}
	writer := &recordingLocalSessionWriter{written: make(chan LocalSessionWrite, 1)}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:   1,
		Presence:      resolver,
		SessionWriter: writer,
		QueueSize:     1,
		Workers:       1,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})

	payload := []byte("shared")
	recipients := []channelappendcontract.Recipient{{UID: "u1"}}
	plan := onlinedelivery.RecipientDeliveryPlan{
		Mode:  onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{MessageID: 1, MessageSeq: 2, Payload: payload},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     authority.Target{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1},
			Recipients: recipients,
		}},
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), plan); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}

	select {
	case write := <-writer.written:
		if &write.Event.Payload[0] != &payload[0] {
			t.Fatalf("admitted payload was cloned, want shared immutable ownership transfer")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for local session write")
	}
}

func TestRuntimeRejectsInvalidPlanModeWithoutRetention(t *testing.T) {
	resolver := &capturingPlanPresenceResolver{called: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{LocalNodeID: 1, Presence: resolver, QueueSize: 1, Workers: 1})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})

	err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), onlinedelivery.RecipientDeliveryPlan{
		Event: channelappendcontract.CommittedEnvelope{MessageID: 1},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     authority.Target{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1},
			Recipients: []channelappendcontract.Recipient{{UID: "u1"}},
		}},
	})
	if err != ErrInvalidPlan {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v, want ErrInvalidPlan", err)
	}
	select {
	case <-resolver.called:
		t.Fatal("invalid plan was retained and processed")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRuntimeAdmissionRejectsClosedInvalidTargetAndOversizedPlansWithoutRetention(t *testing.T) {
	closed := NewRuntime(RuntimeOptions{})
	if err := closed.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(24)); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("closed admission error = %v, want ErrRuntimeClosed", err)
	}

	resolver := &capturingPlanPresenceResolver{called: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:       1,
		Presence:          resolver,
		MaxPlanRecipients: 1,
	})
	startRuntimeForTest(t, runtime)

	invalidTarget := runtimePlanForTest(25)
	invalidTarget.Targets[0].Target.LeaderNodeID = 0
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), invalidTarget); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("invalid target admission error = %v, want ErrInvalidPlan", err)
	}
	oversized := runtimePlanForTest(26)
	oversized.Targets[0].Recipients = append(oversized.Targets[0].Recipients, channelappendcontract.Recipient{UID: "u2"})
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), oversized); !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("oversized admission error = %v, want ErrPlanTooLarge", err)
	}
	select {
	case <-resolver.called:
		t.Fatal("rejected plan was retained and processed")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRuntimeFullQueueWaitsAndHonorsCallerCancellation(t *testing.T) {
	processingStarted := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		QueueSize:   1,
		Workers:     1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			startedOnce.Do(func() { close(processingStarted) })
			<-release
			return []TargetPresenceResult{{}}
		}),
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("idempotent Start() error = %v", err)
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(27)); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	select {
	case <-processingStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first plan processing")
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(28)); err != nil {
		t.Fatalf("queue-filling enqueue error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.EnqueueRecipientDeliveryPlan(canceled, runtimePlanForTest(29)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled full-queue admission error = %v, want context.Canceled", err)
	}

	waiting := make(chan error, 1)
	go func() {
		waiting <- runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(30))
	}()
	select {
	case err := <-waiting:
		t.Fatalf("full-queue admission returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatalf("waiting admission error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("full-queue admission did not resume after capacity became available")
	}
}

func TestRuntimeOwnerPushOwnsPendingAckTransaction(t *testing.T) {
	writer := &recordingLocalSessionWriter{written: make(chan LocalSessionWrite, 1)}
	runtime := NewRuntime(RuntimeOptions{LocalNodeID: 1, SessionWriter: writer})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})

	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 9, MessageSeq: 4},
		Routes:      []onlinedelivery.Route{runtimeRouteForTest()},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if len(result.Accepted) != 1 || runtime.PendingAckCount() != 1 {
		t.Fatalf("push result/pending = %#v/%d, want one accepted pending ack", result, runtime.PendingAckCount())
	}
	if err := runtime.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 9, MessageSeq: 4}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if runtime.PendingAckCount() != 0 {
		t.Fatalf("pending acks = %d, want 0 after feedback", runtime.PendingAckCount())
	}
}

func TestRuntimeOwnerPushUsesOneAlignedAckBatchTransaction(t *testing.T) {
	observer := &recordingRuntimeAckBatchObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			if write.Route.UID == "retry" {
				return SessionWriteResult{Disposition: SessionWriteRetryable}
			}
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		AckBatchObserver: observer,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})

	accepted := runtimeRouteForTest()
	retryable := runtimeRouteForTest()
	retryable.UID = "retry"
	retryable.SessionID = 20
	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 9, MessageSeq: 4},
		Routes:      []onlinedelivery.Route{accepted, retryable},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 1 || runtime.PendingAckCount() != 1 {
		t.Fatalf("push result/pending = %#v/%d, want one accepted, one retryable, one pending", result, runtime.PendingAckCount())
	}
	events := observer.snapshot()
	if len(events) != 2 {
		t.Fatalf("ack batch events = %#v, want bind and finish", events)
	}
	if events[0].Phase != DeliveryAckBatchPhaseBind || events[0].Items != 2 || events[0].Outcome != DeliveryAckBatchOutcomeOK {
		t.Fatalf("bind event = %#v, want complete two-item batch", events[0])
	}
	if events[1].Phase != DeliveryAckBatchPhaseFinish || events[1].Items != 2 || events[1].Rollback != 1 || events[1].Outcome != DeliveryAckBatchOutcomePartial {
		t.Fatalf("finish event = %#v, want one finish and one rollback", events[1])
	}
}

func TestRuntimeStopClearsTransientAckStateAndAllowsRestart(t *testing.T) {
	writer := &recordingLocalSessionWriter{written: make(chan LocalSessionWrite, 2)}
	ackObserver := &recordingRuntimeAckObserver{}
	runtime := NewRuntime(RuntimeOptions{LocalNodeID: 1, SessionWriter: writer, AckObserver: ackObserver})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 9},
		Routes:      []onlinedelivery.Route{runtimeRouteForTest()},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if runtime.PendingAckCount() != 1 {
		t.Fatalf("pending acks = %d, want 1 before stop", runtime.PendingAckCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if runtime.PendingAckCount() != 0 {
		t.Fatalf("pending acks = %d, want cleared after stop", runtime.PendingAckCount())
	}
	if event := ackObserver.last(); event.Action != DeliveryAckActionReset || event.Changed != 1 || event.PendingCount != 0 {
		t.Fatalf("last ack event = %#v, want one-item lifecycle reset", event)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("restart error = %v", err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestRuntimePlanKeepsSiblingTargetProgressAndDurableOfflineBatch(t *testing.T) {
	targetErr := errors.New("target authority unavailable")
	offline := &recordingOfflineRecipientsObserver{}
	var writesMu sync.Mutex
	var written []string
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{
				{Err: targetErr},
				{Routes: []onlinedelivery.Route{{UID: "u2", OwnerNodeID: 1, SessionID: 20}}},
			}
		}),
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			writesMu.Lock()
			written = append(written, write.Route.UID)
			writesMu.Unlock()
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		OfflineRecipientsObserver: offline,
	})

	err := runtime.processPlan(context.Background(), onlinedelivery.RecipientDeliveryPlan{
		Mode:  onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{MessageID: 11, MessageSeq: 7},
		Targets: []onlinedelivery.RecipientTargetBatch{
			{Target: runtimeTargetForTest(1), Recipients: []channelappendcontract.Recipient{{UID: "u1"}}},
			{Target: runtimeTargetForTest(2), Recipients: []channelappendcontract.Recipient{{UID: "u2"}, {UID: "u3"}}},
		},
	})
	if !errors.Is(err, targetErr) {
		t.Fatalf("processPlan() error = %v, want target error", err)
	}
	writesMu.Lock()
	gotWritten := append([]string(nil), written...)
	writesMu.Unlock()
	if !reflect.DeepEqual(gotWritten, []string{"u2"}) {
		t.Fatalf("written UIDs = %#v, want sibling target delivery to continue", gotWritten)
	}
	if got := offline.snapshot(); !reflect.DeepEqual(got, []string{"u3"}) {
		t.Fatalf("offline UIDs = %#v, want only resolved target's offline recipient", got)
	}
}

func TestRuntimeTransientPlanNeverPublishesOfflineRecipients(t *testing.T) {
	offline := &recordingOfflineRecipientsObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{}}
		}),
		OfflineRecipientsObserver: offline,
	})

	err := runtime.processPlan(context.Background(), onlinedelivery.RecipientDeliveryPlan{
		Mode:  onlinedelivery.ModeTransient,
		Event: channelappendcontract.CommittedEnvelope{MessageID: 12},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target: runtimeTargetForTest(1), Recipients: []channelappendcontract.Recipient{{UID: "u1"}},
		}},
	})
	if err != nil {
		t.Fatalf("processPlan() error = %v", err)
	}
	if got := offline.callCount(); got != 0 {
		t.Fatalf("offline observer calls = %d, want none for transient delivery", got)
	}
}

func TestRuntimeSuppressesOnlyExactSenderSession(t *testing.T) {
	routes := []onlinedelivery.Route{
		{UID: "sender", OwnerNodeID: 1, SessionID: 10, DeviceID: "exact"},
		{UID: "sender", OwnerNodeID: 1, SessionID: 11, DeviceID: "same-owner-other-session"},
		{UID: "sender", OwnerNodeID: 2, SessionID: 10, DeviceID: "other-owner-same-session"},
		{UID: "recipient", OwnerNodeID: 1, SessionID: 20, DeviceID: "recipient"},
	}
	var pushedMu sync.Mutex
	var pushed []string
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 99,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: routes}}
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			pushedMu.Lock()
			for _, route := range push.Routes {
				pushed = append(pushed, route.DeviceID)
			}
			pushedMu.Unlock()
			return onlinedelivery.OwnerPushResult{Accepted: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
		}),
	})

	err := runtime.processPlan(context.Background(), onlinedelivery.RecipientDeliveryPlan{
		Mode: onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID: 13, MessageSeq: 8, FromUID: "sender", SenderNodeID: 1, SenderSessionID: 10,
		},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target: runtimeTargetForTest(1),
			Recipients: []channelappendcontract.Recipient{
				{UID: "sender"}, {UID: "recipient"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("processPlan() error = %v", err)
	}
	pushedMu.Lock()
	got := append([]string(nil), pushed...)
	pushedMu.Unlock()
	want := []string{"same-owner-other-session", "recipient", "other-owner-same-session"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed devices = %#v, want %#v", got, want)
	}
}

func TestRuntimeRetriesOnlyRetryableRoutes(t *testing.T) {
	first := onlinedelivery.Route{UID: "u1", OwnerNodeID: 2, SessionID: 10}
	second := onlinedelivery.Route{UID: "u2", OwnerNodeID: 2, SessionID: 20}
	var callsMu sync.Mutex
	var calls [][]onlinedelivery.Route
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{first, second}}}
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			callsMu.Lock()
			calls = append(calls, append([]onlinedelivery.Route(nil), push.Routes...))
			call := len(calls)
			callsMu.Unlock()
			if call == 1 {
				return onlinedelivery.OwnerPushResult{
					Accepted:  []onlinedelivery.Route{first},
					Retryable: []onlinedelivery.Route{second},
				}, nil
			}
			return onlinedelivery.OwnerPushResult{Accepted: []onlinedelivery.Route{second}}, nil
		}),
		RetryMaxAttempts:    2,
		RetryInitialBackoff: time.Nanosecond,
		RetryMaxBackoff:     time.Nanosecond,
	})

	err := runtime.processPlan(context.Background(), onlinedelivery.RecipientDeliveryPlan{
		Mode:  onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{MessageID: 14, MessageSeq: 9},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     runtimeTargetForTest(1),
			Recipients: []channelappendcontract.Recipient{{UID: "u1"}, {UID: "u2"}},
		}},
	})
	if err != nil {
		t.Fatalf("processPlan() error = %v", err)
	}
	callsMu.Lock()
	got := append([][]onlinedelivery.Route(nil), calls...)
	callsMu.Unlock()
	if len(got) != 2 || !reflect.DeepEqual(got[0], []onlinedelivery.Route{first, second}) || !reflect.DeepEqual(got[1], []onlinedelivery.Route{second}) {
		t.Fatalf("owner push calls = %#v, want full batch then narrowed retry", got)
	}
}

func TestRuntimeOwnerGroupingKeepsBatchOrderAndBoundsConcurrency(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 2)
	var localOnce sync.Once
	var remoteOnce sync.Once
	var localMu sync.Mutex
	var localOrder []string
	var remoteMu sync.Mutex
	var remoteOrder []string
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{
				{UID: "l1", OwnerNodeID: 1, SessionID: 11},
				{UID: "r1", OwnerNodeID: 2, SessionID: 21},
				{UID: "l2", OwnerNodeID: 1, SessionID: 12},
				{UID: "r2", OwnerNodeID: 2, SessionID: 22},
				{UID: "l3", OwnerNodeID: 1, SessionID: 13},
				{UID: "r3", OwnerNodeID: 2, SessionID: 23},
			}}}
		}),
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			localOnce.Do(func() {
				started <- "local"
				<-release
			})
			localMu.Lock()
			localOrder = append(localOrder, write.Route.UID)
			localMu.Unlock()
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			remoteOnce.Do(func() {
				started <- "remote"
				<-release
			})
			remoteMu.Lock()
			for _, route := range push.Routes {
				remoteOrder = append(remoteOrder, route.UID)
			}
			remoteMu.Unlock()
			return onlinedelivery.OwnerPushResult{Accepted: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
		}),
		OwnerPushBatchSize: 2,
		OwnerConcurrency:   2,
	})
	plan := runtimePlanForTest(31)
	plan.Targets[0].Recipients = []channelappendcontract.Recipient{
		{UID: "l1"}, {UID: "r1"}, {UID: "l2"}, {UID: "r2"}, {UID: "l3"}, {UID: "r3"},
	}
	done := make(chan error, 1)
	go func() {
		done <- runtime.processPlan(context.Background(), plan)
	}()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case owner := <-started:
			seen[owner] = true
		case <-time.After(time.Second):
			t.Fatalf("owner concurrency starts = %#v, want local and remote overlap", seen)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("processPlan() error = %v", err)
	}
	localMu.Lock()
	gotLocal := append([]string(nil), localOrder...)
	localMu.Unlock()
	remoteMu.Lock()
	gotRemote := append([]string(nil), remoteOrder...)
	remoteMu.Unlock()
	if !reflect.DeepEqual(gotLocal, []string{"l1", "l2", "l3"}) {
		t.Fatalf("local order = %#v, want stable owner order", gotLocal)
	}
	if !reflect.DeepEqual(gotRemote, []string{"r1", "r2", "r3"}) {
		t.Fatalf("remote order = %#v, want stable owner batch order", gotRemote)
	}
}

func TestRuntimeRetryExhaustionReportsBoundedTargetSample(t *testing.T) {
	observer := &recordingRuntimeObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			route := runtimeRouteForTest()
			route.OwnerNodeID = 2
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{route}}}
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			return onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
		}),
		RetryMaxAttempts:    2,
		RetryInitialBackoff: time.Nanosecond,
		RetryMaxBackoff:     time.Nanosecond,
		Observer:            observer,
	})
	plan := runtimePlanForTest(23)

	runtime.runPlan(context.Background(), plan)

	event := observer.lastTerminal()
	if event.Result != ObservationResultRetryExhausted {
		t.Fatalf("terminal result = %q, want retry_exhausted", event.Result)
	}
	if event.Failure.Phase != PlanFailurePhaseOwnerPush ||
		event.Failure.RecipientUID != "u1" ||
		event.Failure.Target != plan.Targets[0].Target ||
		event.Failure.OwnerNodeID != 2 ||
		!errors.Is(event.Failure.Err, ErrOwnerPushRetryExhausted) {
		t.Fatalf("failure sample = %#v, want exact target/recipient retry exhaustion", event.Failure)
	}
}

func TestRuntimeRetryExhaustionReportsTargetFromFailedOwnerBatch(t *testing.T) {
	observer := &recordingRuntimeObserver{}
	var pushes atomic.Int64
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			first := runtimeRouteForTest()
			first.OwnerNodeID = 2
			second := first
			second.UID = "u2"
			second.SessionID = 12
			return []TargetPresenceResult{
				{Routes: []onlinedelivery.Route{first}},
				{Routes: []onlinedelivery.Route{second}},
			}
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			if pushes.Add(1) == 1 {
				return onlinedelivery.OwnerPushResult{Accepted: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
			}
			return onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
		}),
		OwnerPushBatchSize: 1,
		RetryMaxAttempts:   1,
		Observer:           observer,
	})
	plan := runtimePlanForTest(24)
	secondTarget := plan.Targets[0].Clone()
	secondTarget.Target.HashSlot = 2
	secondTarget.Target.SlotID = 2
	secondTarget.Target.RouteRevision = 2
	secondTarget.Recipients = []channelappendcontract.Recipient{{UID: "u2"}}
	plan.Targets = append(plan.Targets, secondTarget)

	runtime.runPlan(context.Background(), plan)

	event := observer.lastTerminal()
	if event.Failure.RecipientUID != "u2" ||
		event.Failure.Target != secondTarget.Target ||
		event.Failure.OwnerNodeID != 2 ||
		!errors.Is(event.Failure.Err, ErrOwnerPushRetryExhausted) {
		t.Fatalf("failure sample = %#v, want exact route and authority target from failed second batch", event.Failure)
	}
}

func TestRuntimeOwnerPushHandlesFastFeedbackAndDuplicateRebind(t *testing.T) {
	var runtime *Runtime
	var writes atomic.Int64
	runtime = NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			if writes.Add(1) == 1 {
				_ = runtime.Recvack(context.Background(), Recvack{
					UID: write.Route.UID, SessionID: write.Route.SessionID, MessageID: write.Event.MessageID,
				})
			}
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
	})
	startRuntimeForTest(t, runtime)

	route := runtimeRouteForTest()
	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 15, MessageSeq: 10},
		Routes:      []onlinedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if len(result.Accepted) != 2 || runtime.PendingAckCount() != 1 {
		t.Fatalf("result/pending = %#v/%d, want two accepted and duplicate rebound", result, runtime.PendingAckCount())
	}
}

func TestRuntimeOwnerPushSessionCloseDuringWriteLeavesNoPendingState(t *testing.T) {
	var runtime *Runtime
	runtime = NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			_ = runtime.SessionClosed(context.Background(), SessionClosed{
				UID: write.Route.UID, SessionID: write.Route.SessionID,
			})
			return SessionWriteResult{Disposition: SessionWriteDropped}
		}),
	})
	startRuntimeForTest(t, runtime)

	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 16},
		Routes:      []onlinedelivery.Route{runtimeRouteForTest()},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if len(result.Dropped) != 1 || runtime.PendingAckCount() != 0 {
		t.Fatalf("result/pending = %#v/%d, want terminal drop with no stale ack", result, runtime.PendingAckCount())
	}
}

func TestRuntimeOwnerPushIsolatesSessionWriterPanicAndRollsBackAck(t *testing.T) {
	panicRoute := runtimeRouteForTest()
	panicRoute.UID = "panic"
	okRoute := runtimeRouteForTest()
	okRoute.UID = "ok"
	okRoute.SessionID = 20
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			if write.Route.UID == "panic" {
				panic("session adapter failed")
			}
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
	})
	startRuntimeForTest(t, runtime)

	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 19},
		Routes:      []onlinedelivery.Route{panicRoute, okRoute},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if !reflect.DeepEqual(result.Retryable, []onlinedelivery.Route{panicRoute}) ||
		!reflect.DeepEqual(result.Accepted, []onlinedelivery.Route{okRoute}) {
		t.Fatalf("result = %#v, want isolated retry and sibling acceptance", result)
	}
	if runtime.PendingAckCount() != 1 {
		t.Fatalf("pending acks = %d, want only accepted sibling", runtime.PendingAckCount())
	}
}

func TestRuntimeObserverPanicsDoNotChangeDeliveryOutcomes(t *testing.T) {
	written := make(chan LocalSessionWrite, 1)
	observer := panickingRuntimeObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{runtimeRouteForTest()}}}
		}),
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			written <- write
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		Observer:         observer,
		AckObserver:      observer,
		AckBatchObserver: observer,
	})
	startRuntimeForTest(t, runtime)

	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(20)); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}
	select {
	case write := <-written:
		if write.Event.MessageID != 20 {
			t.Fatalf("written message = %d, want 20", write.Event.MessageID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery after observer panics")
	}
	if runtime.PendingAckCount() != 1 {
		t.Fatalf("pending acks = %d, want successful delivery state", runtime.PendingAckCount())
	}
	if err := runtime.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 20}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if runtime.PendingAckCount() != 0 {
		t.Fatalf("pending acks = %d, want cleanup despite observer panic", runtime.PendingAckCount())
	}
}

func TestRuntimeWorkerRecoversPlanPanicAndContinues(t *testing.T) {
	var presenceCalls atomic.Int64
	written := make(chan LocalSessionWrite, 1)
	observer := &recordingRuntimeObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			if presenceCalls.Add(1) == 1 {
				panic("presence failed")
			}
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{runtimeRouteForTest()}}}
		}),
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			written <- write
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		Observer: observer,
	})
	startRuntimeForTest(t, runtime)

	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(21)); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(22)); err != nil {
		t.Fatalf("second enqueue error = %v", err)
	}
	select {
	case write := <-written:
		if write.Event.MessageID != 22 {
			t.Fatalf("written message = %d, want second plan 22", write.Event.MessageID)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not continue after plan panic")
	}
	deadline := time.Now().Add(time.Second)
	for {
		results := observer.terminalResults()
		if len(results) >= 2 {
			if !reflect.DeepEqual(results, []string{"panic", "ok"}) {
				t.Fatalf("terminal results = %#v, want panic then ok", results)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal results = %#v, want two results", results)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRuntimeOwnerPushKeepsAckLimitRejectionsItemAligned(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 2, MaxPendingPerSession: 1})
	if !tracker.Bind(PendingRecvAck{UID: "full", SessionID: 10, MessageID: 1}) {
		t.Fatal("preload Bind() = false")
	}
	var writtenMu sync.Mutex
	var written []string
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Acks:        tracker,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			writtenMu.Lock()
			written = append(written, write.Route.UID)
			writtenMu.Unlock()
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
	})
	startRuntimeForTest(t, runtime)

	full := runtimeRouteForTest()
	full.UID = "full"
	open := runtimeRouteForTest()
	open.UID = "open"
	open.SessionID = 20
	invalid := runtimeRouteForTest()
	invalid.UID = ""
	result, err := runtime.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 2},
		Routes:      []onlinedelivery.Route{full, invalid, open},
	})
	if err != nil {
		t.Fatalf("PushOwner() error = %v", err)
	}
	if len(result.Dropped) != 2 || result.Dropped[0].UID != "full" || result.Dropped[1].UID != "" ||
		len(result.Accepted) != 1 || result.Accepted[0].UID != "open" {
		t.Fatalf("result = %#v, want aligned full/invalid drops and open acceptance", result)
	}
	writtenMu.Lock()
	gotWritten := append([]string(nil), written...)
	writtenMu.Unlock()
	if !reflect.DeepEqual(gotWritten, []string{"open"}) || runtime.PendingAckCount() != 2 {
		t.Fatalf("written/pending = %#v/%d, want open and two total pending keys", gotWritten, runtime.PendingAckCount())
	}
}

func TestRuntimeStopTimeoutStaysClosingUntilAcceptedWorkExits(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{runtimeRouteForTest()}}}
		}),
		SessionWriter: localSessionWriterFunc(func(_ context.Context, _ LocalSessionWrite) SessionWriteResult {
			startedOnce.Do(func() { close(started) })
			<-release
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
		PlanTimeout: time.Second,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), runtimePlanForTest(17)); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for accepted work")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopCancel()
	if err := runtime.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("Start() while closing error = %v, want ErrRuntimeClosed", err)
	}
	releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("Stop() after work exit error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("restart error = %v", err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("final Stop() error = %v", err)
	}
}

func TestRuntimeRoutesAcrossSingleAndMultiNodeThroughSameOwnerSeam(t *testing.T) {
	localWrites := make(chan LocalSessionWrite, 1)
	remoteWrites := make(chan LocalSessionWrite, 1)
	target := NewRuntime(RuntimeOptions{
		LocalNodeID: 2,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			remoteWrites <- write
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
	})
	mesh := runtimeMeshPusher{2: target}
	source := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, _ []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{Routes: []onlinedelivery.Route{
				{UID: "local", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 8, SessionID: 255},
				{UID: "remote", OwnerNodeID: 2, OwnerBootID: 7, OwnerSeq: 9, SessionID: 256},
			}}}
		}),
		RemoteOwnerPusher: mesh,
		SessionWriter: localSessionWriterFunc(func(_ context.Context, write LocalSessionWrite) SessionWriteResult {
			localWrites <- write
			return SessionWriteResult{Disposition: SessionWriteAccepted}
		}),
	})
	startRuntimeForTest(t, target)
	startRuntimeForTest(t, source)

	plan := runtimePlanForTest(18)
	plan.Targets[0].Target = runtimeTargetForTest(255)
	plan.Targets[0].Recipients = []channelappendcontract.Recipient{{UID: "local"}, {UID: "remote"}}
	if err := source.EnqueueRecipientDeliveryPlan(context.Background(), plan); err != nil {
		t.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
	}
	for name, writes := range map[string]<-chan LocalSessionWrite{"local": localWrites, "remote": remoteWrites} {
		select {
		case write := <-writes:
			if write.Route.UID != name || write.Event.MessageID != 18 || write.Event.MessageSeq != 18 {
				t.Fatalf("%s owner write = %#v", name, write)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s owner write", name)
		}
	}
	if source.PendingAckCount() != 1 || target.PendingAckCount() != 1 {
		t.Fatalf("local/remote pending = %d/%d, want identical owner-local ACK state", source.PendingAckCount(), target.PendingAckCount())
	}
	if err := source.Recvack(context.Background(), Recvack{UID: "local", SessionID: 255, MessageID: 18}); err != nil {
		t.Fatalf("local Recvack() error = %v", err)
	}
	if err := target.Recvack(context.Background(), Recvack{UID: "remote", SessionID: 256, MessageID: 18}); err != nil {
		t.Fatalf("remote Recvack() error = %v", err)
	}
	if source.PendingAckCount() != 0 || target.PendingAckCount() != 0 {
		t.Fatalf("local/remote pending after feedback = %d/%d, want 0/0", source.PendingAckCount(), target.PendingAckCount())
	}
}

type capturingPlanPresenceResolver struct {
	result []TargetPresenceResult
	called chan struct{}
}

func (r *capturingPlanPresenceResolver) EndpointsByTargets(context.Context, []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
	select {
	case r.called <- struct{}{}:
	default:
	}
	return r.result
}

type recordingLocalSessionWriter struct {
	written chan LocalSessionWrite
	result  SessionWriteResult
}

type localSessionWriterFunc func(context.Context, LocalSessionWrite) SessionWriteResult

func (f localSessionWriterFunc) WriteSession(ctx context.Context, write LocalSessionWrite) SessionWriteResult {
	return f(ctx, write)
}

type planPresenceResolverFunc func(context.Context, []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult

func (f planPresenceResolverFunc) EndpointsByTargets(ctx context.Context, targets []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
	return f(ctx, targets)
}

type remoteOwnerPusherFunc func(context.Context, onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error)

func (f remoteOwnerPusherFunc) PushOwner(ctx context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	return f(ctx, push)
}

type runtimeMeshPusher map[uint64]*Runtime

func (m runtimeMeshPusher) PushOwner(ctx context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	target := m[push.OwnerNodeID]
	if target == nil {
		return onlinedelivery.OwnerPushResult{}, errors.New("runtime mesh owner missing")
	}
	return target.PushOwner(ctx, push)
}

type recordingOfflineRecipientsObserver struct {
	mu    sync.Mutex
	calls int
	uids  []string
}

func (o *recordingOfflineRecipientsObserver) ObserveOfflineRecipients(_ context.Context, event OfflineRecipientsEvent) {
	o.mu.Lock()
	o.calls++
	o.uids = append(o.uids, event.UIDs...)
	o.mu.Unlock()
}

func (o *recordingOfflineRecipientsObserver) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.uids...)
}

func (o *recordingOfflineRecipientsObserver) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

type recordingRuntimeAckBatchObserver struct {
	mu     sync.Mutex
	events []AckBatchEvent
}

type recordingRuntimeAckObserver struct {
	mu     sync.Mutex
	events []AckEvent
}

func (o *recordingRuntimeAckObserver) ObserveAck(event AckEvent) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *recordingRuntimeAckObserver) last() AckEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.events) == 0 {
		return AckEvent{}
	}
	return o.events[len(o.events)-1]
}

type panickingRuntimeObserver struct{}

func (panickingRuntimeObserver) ObservePlanAdmission(PlanAdmissionEvent) {
	panic("plan admission observer")
}
func (panickingRuntimeObserver) ObservePlanTerminal(PlanTerminalEvent) {
	panic("plan terminal observer")
}
func (panickingRuntimeObserver) SetRuntimePressure(RuntimePressureEvent) {
	panic("runtime pressure observer")
}
func (panickingRuntimeObserver) ObserveOwnerPush(OwnerPushEvent) { panic("owner push observer") }
func (panickingRuntimeObserver) ObserveAck(AckEvent)             { panic("ack observer") }
func (panickingRuntimeObserver) ObserveAckBatch(AckBatchEvent)   { panic("ack batch observer") }

type recordingRuntimeObserver struct {
	mu       sync.Mutex
	terminal []PlanTerminalEvent
}

func (*recordingRuntimeObserver) ObservePlanAdmission(PlanAdmissionEvent) {}
func (*recordingRuntimeObserver) SetRuntimePressure(RuntimePressureEvent) {}
func (*recordingRuntimeObserver) ObserveOwnerPush(OwnerPushEvent)         {}

func (o *recordingRuntimeObserver) ObservePlanTerminal(event PlanTerminalEvent) {
	o.mu.Lock()
	o.terminal = append(o.terminal, event)
	o.mu.Unlock()
}

func (o *recordingRuntimeObserver) terminalResults() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	results := make([]string, len(o.terminal))
	for i := range o.terminal {
		results[i] = string(o.terminal[i].Result)
	}
	return results
}

func (o *recordingRuntimeObserver) lastTerminal() PlanTerminalEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.terminal) == 0 {
		return PlanTerminalEvent{}
	}
	return o.terminal[len(o.terminal)-1]
}

func (o *recordingRuntimeAckBatchObserver) ObserveAckBatch(event AckBatchEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingRuntimeAckBatchObserver) snapshot() []AckBatchEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]AckBatchEvent(nil), o.events...)
}

func (w *recordingLocalSessionWriter) WriteSession(_ context.Context, write LocalSessionWrite) SessionWriteResult {
	if w.written != nil {
		w.written <- write
	}
	if w.result.Disposition == 0 {
		return SessionWriteResult{Disposition: SessionWriteAccepted}
	}
	return w.result
}

func runtimeRouteForTest() onlinedelivery.Route {
	return onlinedelivery.Route{
		UID:         "u1",
		OwnerNodeID: 1,
		OwnerBootID: 2,
		OwnerSeq:    3,
		SessionID:   10,
	}
}

func runtimeTargetForTest(slot uint16) authority.Target {
	return authority.Target{
		HashSlot: slot, SlotID: uint32(slot) + 1, LeaderNodeID: 1,
		LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1,
	}
}

func runtimePlanForTest(messageID uint64) onlinedelivery.RecipientDeliveryPlan {
	return onlinedelivery.RecipientDeliveryPlan{
		Mode:  onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{MessageID: messageID, MessageSeq: messageID},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     runtimeTargetForTest(1),
			Recipients: []channelappendcontract.Recipient{{UID: "u1"}},
		}},
	}
}

func startRuntimeForTest(t *testing.T, runtime *Runtime) {
	t.Helper()
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})
}
