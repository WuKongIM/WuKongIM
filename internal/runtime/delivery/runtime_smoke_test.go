package delivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func TestRuntimePlanAdmissionTransfersSharedImmutableStorage(t *testing.T) {
	resolver := &smokePlanPresenceResolver{
		result: []TargetPresenceResult{{
			Routes: []onlinedelivery.Route{smokeRuntimeRoute()},
		}},
		called: make(chan struct{}, 1),
	}
	writer := &smokeSessionWriter{written: make(chan LocalSessionWrite, 1)}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:   1,
		Presence:      resolver,
		SessionWriter: writer,
		QueueSize:     1,
		Workers:       1,
	})
	startSmokeRuntime(t, runtime)

	payload := []byte("shared")
	plan := onlinedelivery.RecipientDeliveryPlan{
		Mode: onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID:  1,
			MessageSeq: 2,
			Payload:    payload,
		},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     smokeRuntimeTarget(),
			Recipients: []channelappendcontract.Recipient{{UID: "u1"}},
		}},
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(
		context.Background(),
		plan,
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case write := <-writer.written:
		if &write.Event.Payload[0] != &payload[0] {
			t.Fatal("admission cloned shared immutable payload")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session write")
	}
}

func TestRuntimeAdmissionRejectsClosedInvalidAndOversizedPlans(t *testing.T) {
	closed := NewRuntime(RuntimeOptions{})
	if err := closed.EnqueueRecipientDeliveryPlan(
		context.Background(),
		smokeRuntimePlan(24),
	); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("closed admission error = %v", err)
	}

	resolver := &smokePlanPresenceResolver{called: make(chan struct{}, 1)}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:       1,
		Presence:          resolver,
		MaxPlanRecipients: 1,
	})
	startSmokeRuntime(t, runtime)

	invalid := smokeRuntimePlan(25)
	invalid.Targets[0].Target.LeaderNodeID = 0
	if err := runtime.EnqueueRecipientDeliveryPlan(
		context.Background(),
		invalid,
	); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("invalid-target admission error = %v", err)
	}
	oversized := smokeRuntimePlan(26)
	oversized.Targets[0].Recipients = append(
		oversized.Targets[0].Recipients,
		channelappendcontract.Recipient{UID: "u2"},
	)
	if err := runtime.EnqueueRecipientDeliveryPlan(
		context.Background(),
		oversized,
	); !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("oversized admission error = %v", err)
	}
	select {
	case <-resolver.called:
		t.Fatal("rejected plan was retained")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRuntimeOwnerPushOwnsPendingAckTransaction(t *testing.T) {
	writer := &smokeSessionWriter{written: make(chan LocalSessionWrite, 1)}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:   1,
		SessionWriter: writer,
	})
	startSmokeRuntime(t, runtime)

	result, err := runtime.PushOwner(
		context.Background(),
		onlinedelivery.OwnerPush{
			OwnerNodeID: 1,
			Event: channelappendcontract.CommittedEnvelope{
				MessageID:  9,
				MessageSeq: 4,
			},
			Routes: []onlinedelivery.Route{smokeRuntimeRoute()},
		},
	)
	if err != nil {
		t.Fatalf("push owner: %v", err)
	}
	if len(result.Accepted) != 1 || runtime.PendingAckCount() != 1 {
		t.Fatalf(
			"push result/pending = %#v/%d, want one accepted pending ack",
			result,
			runtime.PendingAckCount(),
		)
	}
	if err := runtime.Recvack(context.Background(), Recvack{
		UID:        "u1",
		SessionID:  10,
		MessageID:  9,
		MessageSeq: 4,
	}); err != nil {
		t.Fatalf("recvack: %v", err)
	}
	if got := runtime.PendingAckCount(); got != 0 {
		t.Fatalf("pending acks = %d, want 0", got)
	}
}

func TestRuntimeStopClearsTransientAckStateAndAllowsRestart(t *testing.T) {
	observer := &smokeAckObserver{}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID:   1,
		SessionWriter: &smokeSessionWriter{},
		AckObserver:   observer,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err := runtime.PushOwner(
		context.Background(),
		onlinedelivery.OwnerPush{
			OwnerNodeID: 1,
			Event:       channelappendcontract.CommittedEnvelope{MessageID: 9},
			Routes:      []onlinedelivery.Route{smokeRuntimeRoute()},
		},
	)
	if err != nil {
		t.Fatalf("push owner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := runtime.PendingAckCount(); got != 0 {
		t.Fatalf("pending acks after stop = %d, want 0", got)
	}
	if observer.last.Action != DeliveryAckActionReset ||
		observer.last.Changed != 1 ||
		observer.last.PendingCount != 0 {
		t.Fatalf("last ack event = %#v", observer.last)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

func TestRuntimeStopDrainsAcceptedPlanBeforeCancel(t *testing.T) {
	started := make(chan struct{})
	completed := make(chan bool, 1)
	var startedOnce sync.Once
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: &smokePlanPresenceResolver{result: []TargetPresenceResult{{
			Routes: []onlinedelivery.Route{smokeRuntimeRoute()},
		}}},
		SessionWriter: localSessionWriterFunc(func(ctx context.Context, _ LocalSessionWrite) SessionWriteResult {
			startedOnce.Do(func() { close(started) })
			select {
			case <-ctx.Done():
				completed <- false
				return SessionWriteResult{Disposition: SessionWriteRetryable, Err: ctx.Err()}
			case <-time.After(20 * time.Millisecond):
				completed <- true
				return SessionWriteResult{Disposition: SessionWriteAccepted}
			}
		}),
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), smokeRuntimePlan(27)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for accepted plan")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if finished := <-completed; !finished {
		t.Fatal("successful Stop canceled ownership-transferred plan")
	}
}

func TestRuntimeContinuesLaterOwnerBatchAfterRetryExhaustion(t *testing.T) {
	first := smokeRuntimeRoute()
	first.OwnerNodeID = 2
	second := first
	second.UID = "u2"
	second.SessionID = 20
	var pushes atomic.Int64
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: &smokePlanPresenceResolver{result: []TargetPresenceResult{{
			Routes: []onlinedelivery.Route{first, second},
		}}},
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			if pushes.Add(1) == 1 {
				return onlinedelivery.OwnerPushResult{Retryable: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
			}
			return onlinedelivery.OwnerPushResult{Accepted: append([]onlinedelivery.Route(nil), push.Routes...)}, nil
		}),
		OwnerPushBatchSize: 1,
		RetryMaxAttempts:   1,
	})
	plan := smokeRuntimePlan(28)
	plan.Targets[0].Recipients = append(plan.Targets[0].Recipients, channelappendcontract.Recipient{UID: "u2"})

	err := runtime.processPlan(context.Background(), plan)
	if !errors.Is(err, ErrOwnerPushRetryExhausted) {
		t.Fatalf("process plan error = %v, want retry exhaustion", err)
	}
	if got := pushes.Load(); got != 2 {
		t.Fatalf("owner pushes = %d, want failed batch and later sibling batch", got)
	}
}

func TestRuntimeRejectsUnknownOwnerAndKeepsMissingWriterRetryable(t *testing.T) {
	planOnly := NewRuntime(RuntimeOptions{LocalNodeID: 1})
	if err := planOnly.processPlan(context.Background(), smokeRuntimePlan(29)); !errors.Is(err, ErrPresenceResolverUnavailable) {
		t.Fatalf("missing presence error = %v", err)
	}

	unknown := NewRuntime(RuntimeOptions{})
	if err := unknown.Start(context.Background()); err != nil {
		t.Fatalf("start unknown owner runtime: %v", err)
	}
	if _, err := unknown.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 30},
		Routes:      []onlinedelivery.Route{smokeRuntimeRoute()},
	}); !errors.Is(err, ErrOwnerPushNotLocal) {
		t.Fatalf("unknown owner push error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := unknown.Stop(ctx); err != nil {
		t.Fatalf("stop unknown owner runtime: %v", err)
	}

	local := NewRuntime(RuntimeOptions{LocalNodeID: 1})
	if err := local.Start(context.Background()); err != nil {
		t.Fatalf("start local runtime: %v", err)
	}
	result, err := local.PushOwner(context.Background(), onlinedelivery.OwnerPush{
		OwnerNodeID: 1,
		Event:       channelappendcontract.CommittedEnvelope{MessageID: 31},
		Routes:      []onlinedelivery.Route{smokeRuntimeRoute()},
	})
	if err != nil {
		t.Fatalf("missing writer push: %v", err)
	}
	if len(result.Retryable) != 1 || len(result.Dropped) != 0 {
		t.Fatalf("missing writer result = %#v, want one retryable route", result)
	}
	if err := local.Stop(ctx); err != nil {
		t.Fatalf("stop local runtime: %v", err)
	}
}

type smokePlanPresenceResolver struct {
	result []TargetPresenceResult
	called chan struct{}
}

func (r *smokePlanPresenceResolver) EndpointsByTargets(
	context.Context,
	[]onlinedelivery.RecipientTargetBatch,
) []TargetPresenceResult {
	select {
	case r.called <- struct{}{}:
	default:
	}
	return r.result
}

type smokeSessionWriter struct {
	written chan LocalSessionWrite
}

func (w *smokeSessionWriter) WriteSession(
	_ context.Context,
	write LocalSessionWrite,
) SessionWriteResult {
	if w.written != nil {
		w.written <- write
	}
	return SessionWriteResult{Disposition: SessionWriteAccepted}
}

type localSessionWriterFunc func(context.Context, LocalSessionWrite) SessionWriteResult

func (f localSessionWriterFunc) WriteSession(ctx context.Context, write LocalSessionWrite) SessionWriteResult {
	return f(ctx, write)
}

type remoteOwnerPusherFunc func(context.Context, onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error)

func (f remoteOwnerPusherFunc) PushOwner(ctx context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	return f(ctx, push)
}

type smokeAckObserver struct {
	last AckEvent
}

func (o *smokeAckObserver) ObserveAck(event AckEvent) {
	o.last = event
}

func smokeRuntimeRoute() onlinedelivery.Route {
	return onlinedelivery.Route{
		UID:         "u1",
		OwnerNodeID: 1,
		OwnerBootID: 2,
		OwnerSeq:    3,
		SessionID:   10,
	}
}

func smokeRuntimeTarget() authority.Target {
	return authority.Target{
		HashSlot:       1,
		SlotID:         2,
		LeaderNodeID:   1,
		LeaderTerm:     1,
		ConfigEpoch:    1,
		RouteRevision:  1,
		AuthorityEpoch: 1,
	}
}

func smokeRuntimePlan(messageID uint64) onlinedelivery.RecipientDeliveryPlan {
	return onlinedelivery.RecipientDeliveryPlan{
		Mode: onlinedelivery.ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID:  messageID,
			MessageSeq: messageID,
		},
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     smokeRuntimeTarget(),
			Recipients: []channelappendcontract.Recipient{{UID: "u1"}},
		}},
	}
}

func startSmokeRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(ctx)
	})
}
