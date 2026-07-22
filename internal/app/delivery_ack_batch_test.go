package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestLocalOwnerPusherBatchesAckObservationAndCleansOnlyFailedWrites(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 4})
	goodFirst := &ackBatchSession{}
	retryableFailure := &ackBatchSession{writeErr: errors.New("temporary write failure")}
	goodLast := &ackBatchSession{}
	routes := []runtimedelivery.Route{
		registerAckBatchSession(t, registry, 1, "u1", 101, goodFirst),
		registerAckBatchSession(t, registry, 1, "u2", 102, retryableFailure),
		registerAckBatchSession(t, registry, 1, "u3", 103, goodLast),
	}
	observer := &localOwnerPusherAckObserver{}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Acks:             runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 4}),
		AckObserver:      observer,
		AckBatchObserver: observer,
	})
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42, ChannelID: "g1", ChannelType: 2},
		Routes:      routes,
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 2 || result.Accepted[0].UID != "u1" || result.Accepted[1].UID != "u3" {
		t.Fatalf("accepted routes = %#v, want u1 and u3", result.Accepted)
	}
	if len(result.Retryable) != 1 || result.Retryable[0].UID != "u2" || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want only u2 retryable", result)
	}
	if got := manager.PendingAckCount(); got != 2 {
		t.Fatalf("PendingAckCount() = %d, want successful writes only", got)
	}
	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionBind); got != 1 {
		t.Fatalf("bind observations = %d, want one batch observation", got)
	}
	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionRollback); got != 1 {
		t.Fatalf("rollback observations = %d, want one failed-write token cleanup", got)
	}
	batchEvents := observer.BatchEvents()
	if len(batchEvents) != 2 {
		t.Fatalf("batch events = %#v, want bind and finish", batchEvents)
	}
	finish := batchEvents[1]
	if finish.Phase != runtimedelivery.DeliveryAckBatchPhaseFinish || finish.Rollback != 1 || finish.Outcome != runtimedelivery.DeliveryAckBatchOutcomePartial {
		t.Fatalf("finish batch event = %#v, want one actual rollback and partial outcome", finish)
	}
}

func TestLocalOwnerPusherBatchKeepsMixedLimitRejectionsItemAligned(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 4})
	fullSession := &ackBatchSession{}
	openSession := &ackBatchSession{}
	routes := []runtimedelivery.Route{
		registerAckBatchSession(t, registry, 1, "full", 101, fullSession),
		{UID: "missing", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 202, SessionID: 202},
		registerAckBatchSession(t, registry, 1, "open", 102, openSession),
	}
	observer := &localOwnerPusherAckObserver{}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
			ShardCount:           4,
			MaxPendingPerSession: 1,
		}),
		AckObserver: observer,
	})
	if !manager.BindPendingAck(runtimedelivery.PendingRecvAck{UID: "full", SessionID: 101, MessageID: 1}) {
		t.Fatal("preload BindPendingAck() = false")
	}
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2, ChannelID: "g1", ChannelType: 2},
		Routes:      routes,
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 2 || result.Dropped[0].UID != "full" || result.Dropped[1].UID != "missing" {
		t.Fatalf("dropped routes = %#v, want full then missing in input order", result.Dropped)
	}
	if len(result.Accepted) != 1 || result.Accepted[0].UID != "open" {
		t.Fatalf("accepted routes = %#v, want open", result.Accepted)
	}
	if fullSession.writes.Load() != 0 || openSession.writes.Load() != 1 {
		t.Fatalf("writes full/open = %d/%d, want 0/1", fullSession.writes.Load(), openSession.writes.Load())
	}
	if got := manager.PendingAckCount(); got != 2 {
		t.Fatalf("PendingAckCount() = %d, want preload plus open delivery", got)
	}
	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionBind); got != 2 {
		t.Fatalf("bind observations = %d, want preload plus one mixed batch observation", got)
	}
}

func TestLocalOwnerPusherBatchHandlesAckAndSessionCloseDuringWrite(t *testing.T) {
	t.Run("close after batch bind is revalidated before write", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		closedSession := &ackBatchSession{}
		openSession := &ackBatchSession{}
		closedRoute := registerAckBatchSession(t, registry, 1, "u1", 101, closedSession)
		openRoute := registerAckBatchSession(t, registry, 1, "u2", 102, openSession)
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			AckObserver: ackBatchObserverFunc(func(event runtimedelivery.AckEvent) {
				if event.Action == runtimedelivery.DeliveryAckActionBind {
					registry.MarkClosingAndUnregister(101)
				}
			}),
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
			Routes:      []runtimedelivery.Route{closedRoute, openRoute},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || len(result.Accepted) != 1 || closedSession.writes.Load() != 0 || openSession.writes.Load() != 1 || manager.PendingAckCount() != 1 {
			t.Fatalf("result = %#v writes = %d/%d pending = %d, want closed route dropped and open route retained", result, closedSession.writes.Load(), openSession.writes.Load(), manager.PendingAckCount())
		}
	})

	t.Run("recvack consumes binding after packet handoff", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		session := &ackBatchSession{}
		route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
		session.onWrite = func() error {
			return manager.Recvack(context.Background(), runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 9001})
		}
		pusher := &localOwnerPusher{online: registry, delivery: manager}

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
			Routes:      []runtimedelivery.Route{route},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Accepted) != 1 || manager.PendingAckCount() != 0 {
			t.Fatalf("result = %#v pending = %d, want accepted and already acknowledged", result, manager.PendingAckCount())
		}
	})

	t.Run("session close removes binding and terminal write is dropped", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		session := &ackBatchSession{}
		route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
		session.onWrite = func() error {
			registry.MarkClosingAndUnregister(101)
			if err := manager.SessionClosed(context.Background(), runtimedelivery.SessionClosed{UID: "u1", SessionID: 101}); err != nil {
				return err
			}
			return gatewaysession.ErrSessionClosed
		}
		pusher := &localOwnerPusher{online: registry, delivery: manager}

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
			Routes:      []runtimedelivery.Route{route},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || manager.PendingAckCount() != 0 {
			t.Fatalf("result = %#v pending = %d, want terminal drop and closed-session cleanup", result, manager.PendingAckCount())
		}
	})
}

func TestLocalOwnerPusherBatchRebindsDuplicateAckKeyAfterEarlierWriteFailure(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	session := &ackBatchSequenceSession{writeErrs: []error{errors.New("temporary write failure"), nil}}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Retryable) != 1 || len(result.Accepted) != 1 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want first retryable and second accepted", result)
	}
	if got := session.writes.Load(); got != 2 {
		t.Fatalf("writes = %d, want 2 duplicate delivery attempts", got)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want second successful duplicate rebound", got)
	}
}

func TestLocalOwnerPusherBatchDuplicateFailuresRollbackEveryReservation(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	observer := &localOwnerPusherAckObserver{}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		AckBatchObserver: observer,
	})
	session := &ackBatchSequenceSession{writeErrs: []error{
		errors.New("temporary first duplicate failure"),
		errors.New("temporary second duplicate failure"),
	}}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Retryable) != 2 || len(result.Accepted) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want both duplicate writes retryable", result)
	}
	if got := manager.PendingAckCount(); got != 0 {
		t.Fatalf("PendingAckCount() = %d, want every failed duplicate reservation rolled back", got)
	}
	batchEvents := observer.BatchEvents()
	if len(batchEvents) != 2 {
		t.Fatalf("batch events = %#v, want bind and finish", batchEvents)
	}
	finish := batchEvents[1]
	if finish.Phase != runtimedelivery.DeliveryAckBatchPhaseFinish || finish.Rollback != 3 || finish.Outcome != runtimedelivery.DeliveryAckBatchOutcomeRolledBack {
		t.Fatalf("finish batch event = %#v, want all three actual cancellations including the duplicate rebind", finish)
	}
}

func TestLocalOwnerPusherBatchRebindsDuplicateAckKeyAfterFastRecvack(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	session := &ackBatchSequenceSession{}
	session.onWrite = func(call int64) error {
		if call != 1 {
			return nil
		}
		return manager.Recvack(context.Background(), runtimedelivery.Recvack{
			UID:       "u1",
			SessionID: 101,
			MessageID: 9001,
		})
	}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 2 || len(result.Retryable) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want both duplicate writes accepted", result)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want second duplicate rebound after first fast recvack", got)
	}
}

func TestLocalOwnerPusherBatchRevalidatesSessionAfterDuplicateRebind(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &ackBatchSequenceSession{}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	var bindObservations atomic.Int64
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		AckObserver: ackBatchObserverFunc(func(event runtimedelivery.AckEvent) {
			if event.Action == runtimedelivery.DeliveryAckActionBind && bindObservations.Add(1) == 2 {
				registry.MarkClosingAndUnregister(101)
			}
		}),
	})
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Dropped) != 1 || len(result.Retryable) != 0 {
		t.Fatalf("result = %#v, want first duplicate accepted and second dropped after rebind-time close", result)
	}
	if got := session.writes.Load(); got != 1 {
		t.Fatalf("writes = %d, want no stale-handle write after duplicate rebind", got)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want only the first successful duplicate retained", got)
	}
}

func TestLocalOwnerPusherBatchKeepsEarlierDuplicateBindingWhenLaterWriteFails(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	session := &ackBatchSequenceSession{writeErrs: []error{nil, errors.New("temporary duplicate write failure")}}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 1 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want first accepted and second retryable", result)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want earlier successful duplicate binding preserved", got)
	}
}

func TestLocalOwnerPusherBatchCleansNewDuplicateBindingAfterFastRecvackAndWriteFailure(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	session := &ackBatchSequenceSession{writeErrs: []error{nil, errors.New("temporary duplicate write failure")}}
	session.onWrite = func(call int64) error {
		if call != 1 {
			return nil
		}
		return manager.Recvack(context.Background(), runtimedelivery.Recvack{
			UID:       "u1",
			SessionID: 101,
			MessageID: 9001,
		})
	}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route, route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 1 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want first accepted and second retryable", result)
	}
	if got := manager.PendingAckCount(); got != 0 {
		t.Fatalf("PendingAckCount() = %d, want failed duplicate's newly added binding cleaned", got)
	}
}

func TestLocalOwnerPusherSingleRouteKeepsExistingBindingWhenRefreshWriteFails(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	preload := runtimedelivery.PendingRecvAck{UID: "u1", SessionID: 101, MessageID: 9001}
	if bind := manager.BindPendingAckResult(preload); !bind.Bound || !bind.Added || !manager.FinishPendingAck(preload, bind.Token) {
		t.Fatalf("preload BindPendingAckResult() = %#v, want committed added binding", bind)
	}
	session := &ackBatchSession{writeErr: errors.New("temporary retry write failure")}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Retryable) != 1 || len(result.Accepted) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("result = %#v, want one retryable route", result)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want pre-existing successful binding preserved", got)
	}
}

func TestLocalOwnerPusherSingleRouteFailedWriteDoesNotRollbackConcurrentRefresh(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	session := newAckInterleavingSession()
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	pusher := &localOwnerPusher{online: registry, delivery: manager}
	cmd := runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route},
	}

	failedPush := make(chan runtimedelivery.PushResult, 1)
	go func() {
		result, _ := pusher.Push(context.Background(), cmd)
		failedPush <- result
	}()
	session.waitForWrite(t, 1)

	successfulPush := make(chan runtimedelivery.PushResult, 1)
	go func() {
		result, _ := pusher.Push(context.Background(), cmd)
		successfulPush <- result
	}()
	session.waitForWrite(t, 2)

	session.releaseWrite(1, errors.New("temporary first write failure"))
	failed := waitAckInterleavingPush(t, failedPush)
	if len(failed.Retryable) != 1 {
		t.Fatalf("first Push() result = %#v, want one retryable route", failed)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() after first rollback = %d, want concurrent refresh retained", got)
	}

	session.releaseWrite(2, nil)
	succeeded := waitAckInterleavingPush(t, successfulPush)
	if len(succeeded.Accepted) != 1 {
		t.Fatalf("second Push() result = %#v, want one accepted route", succeeded)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() after second success = %d, want one pending key", got)
	}
	if err := manager.Recvack(context.Background(), runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 9001}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if got := manager.PendingAckCount(); got != 0 {
		t.Fatalf("PendingAckCount() after Recvack = %d, want zero", got)
	}
}

func TestLocalOwnerPusherSingleRouteRevalidatesSessionAfterBind(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &ackBatchSession{}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		AckObserver: ackBatchObserverFunc(func(event runtimedelivery.AckEvent) {
			if event.Action == runtimedelivery.DeliveryAckActionBind {
				registry.MarkClosingAndUnregister(101)
			}
		}),
	})
	pusher := &localOwnerPusher{online: registry, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("result = %#v, want one dropped route after bind-time close", result)
	}
	if got := session.writes.Load(); got != 0 {
		t.Fatalf("writes = %d, want no write after exact-session revalidation fails", got)
	}
	if got := manager.PendingAckCount(); got != 0 {
		t.Fatalf("PendingAckCount() = %d, want failed reservation rolled back", got)
	}
}

func TestLocalOwnerPusherBatchFailedWriteDoesNotRollbackConcurrentRefresh(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 2})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	sharedSession := newAckInterleavingSession()
	sharedRoute := registerAckBatchSession(t, registry, 1, "shared", 101, sharedSession)
	otherRoute := registerAckBatchSession(t, registry, 1, "other", 102, &ackBatchSession{})
	pusher := &localOwnerPusher{online: registry, delivery: manager}
	cmd := runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
		Routes:      []runtimedelivery.Route{sharedRoute, otherRoute},
	}

	failedPush := make(chan runtimedelivery.PushResult, 1)
	go func() {
		result, _ := pusher.Push(context.Background(), cmd)
		failedPush <- result
	}()
	sharedSession.waitForWrite(t, 1)

	successfulPush := make(chan runtimedelivery.PushResult, 1)
	go func() {
		result, _ := pusher.Push(context.Background(), cmd)
		successfulPush <- result
	}()
	sharedSession.waitForWrite(t, 2)

	sharedSession.releaseWrite(1, errors.New("temporary first batch write failure"))
	failed := waitAckInterleavingPush(t, failedPush)
	if len(failed.Retryable) != 1 || len(failed.Accepted) != 1 {
		t.Fatalf("first Push() result = %#v, want shared retryable and other accepted", failed)
	}
	if got := manager.PendingAckCount(); got != 2 {
		t.Fatalf("PendingAckCount() after first rollback = %d, want both concurrent batch identities retained", got)
	}

	sharedSession.releaseWrite(2, nil)
	succeeded := waitAckInterleavingPush(t, successfulPush)
	if len(succeeded.Accepted) != 2 {
		t.Fatalf("second Push() result = %#v, want both routes accepted", succeeded)
	}
	assertAckBatchKeysRemain(t, manager, []runtimedelivery.Recvack{
		{UID: "shared", SessionID: 101, MessageID: 9001},
		{UID: "other", SessionID: 102, MessageID: 9001},
	})
}

func TestLocalOwnerPusherBatchKeepsExistingBindingWhenRefreshedRouteFails(t *testing.T) {
	t.Run("write failure", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		preload := runtimedelivery.PendingRecvAck{UID: "old", SessionID: 101, MessageID: 9001}
		if bind := manager.BindPendingAckResult(preload); !bind.Bound || !bind.Added || !manager.FinishPendingAck(preload, bind.Token) {
			t.Fatalf("preload BindPendingAckResult() = %#v, want committed added binding", bind)
		}
		failedSession := &ackBatchSession{writeErr: errors.New("temporary retry write failure")}
		goodSession := &ackBatchSession{}
		routes := []runtimedelivery.Route{
			registerAckBatchSession(t, registry, 1, "old", 101, failedSession),
			registerAckBatchSession(t, registry, 1, "new", 102, goodSession),
		}
		pusher := &localOwnerPusher{online: registry, delivery: manager}

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
			Routes:      routes,
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Retryable) != 1 || result.Retryable[0].UID != "old" || len(result.Accepted) != 1 || result.Accepted[0].UID != "new" {
			t.Fatalf("result = %#v, want old retryable and new accepted", result)
		}
		assertAckBatchKeysRemain(t, manager, []runtimedelivery.Recvack{
			{UID: "old", SessionID: 101, MessageID: 9001},
			{UID: "new", SessionID: 102, MessageID: 9001},
		})
	})

	t.Run("session revalidation failure", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		var bindObservations atomic.Int64
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			AckObserver: ackBatchObserverFunc(func(event runtimedelivery.AckEvent) {
				if event.Action == runtimedelivery.DeliveryAckActionBind && bindObservations.Add(1) == 2 {
					registry.MarkClosingAndUnregister(101)
				}
			}),
		})
		preload := runtimedelivery.PendingRecvAck{UID: "old", SessionID: 101, MessageID: 9001}
		if bind := manager.BindPendingAckResult(preload); !bind.Bound || !bind.Added || !manager.FinishPendingAck(preload, bind.Token) {
			t.Fatalf("preload BindPendingAckResult() = %#v, want committed added binding", bind)
		}
		failedSession := &ackBatchSession{}
		goodSession := &ackBatchSession{}
		routes := []runtimedelivery.Route{
			registerAckBatchSession(t, registry, 1, "old", 101, failedSession),
			registerAckBatchSession(t, registry, 1, "new", 102, goodSession),
		}
		pusher := &localOwnerPusher{online: registry, delivery: manager}

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 9001, MessageSeq: 42},
			Routes:      routes,
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || result.Dropped[0].UID != "old" || len(result.Accepted) != 1 || result.Accepted[0].UID != "new" {
			t.Fatalf("result = %#v, want old dropped and new accepted", result)
		}
		if failedSession.writes.Load() != 0 || goodSession.writes.Load() != 1 {
			t.Fatalf("writes old/new = %d/%d, want 0/1", failedSession.writes.Load(), goodSession.writes.Load())
		}
		assertAckBatchKeysRemain(t, manager, []runtimedelivery.Recvack{
			{UID: "old", SessionID: 101, MessageID: 9001},
			{UID: "new", SessionID: 102, MessageID: 9001},
		})
	})
}

func assertAckBatchKeysRemain(t *testing.T, manager *runtimedelivery.Manager, acks []runtimedelivery.Recvack) {
	t.Helper()
	if got := manager.PendingAckCount(); got != len(acks) {
		t.Fatalf("PendingAckCount() = %d, want %d retained successful bindings", got, len(acks))
	}
	for i, ack := range acks {
		if err := manager.Recvack(context.Background(), ack); err != nil {
			t.Fatalf("Recvack(%#v) error = %v", ack, err)
		}
		if got := manager.PendingAckCount(); got != len(acks)-i-1 {
			t.Fatalf("PendingAckCount() after Recvack(%#v) = %d, want %d", ack, got, len(acks)-i-1)
		}
	}
}

func BenchmarkLocalOwnerPusherAckBinding55Routes(b *testing.B) {
	registry, cmd := ackBatchPushBenchmarkFixture(b, 55)

	b.Run("single_bind_reference", func(b *testing.B) {
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
			AckObserver: &ackBatchBenchmarkObserver{},
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}
		benchmarkLocalOwnerPushSingleBind(b, pusher, cmd)
	})
	b.Run("batch_bind", func(b *testing.B) {
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
			AckObserver: &ackBatchBenchmarkObserver{},
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}
		if _, err := pusher.Push(context.Background(), cmd); err != nil {
			b.Fatalf("warm Push() error = %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := pusher.Push(context.Background(), cmd)
			if err != nil || len(result.Accepted) != len(cmd.Routes) {
				b.Fatalf("Push() result = %#v error = %v", result, err)
			}
		}
	})
	b.Run("batch_bind_metrics_enabled", func(b *testing.B) {
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Acks:             runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
			AckObserver:      &ackBatchBenchmarkObserver{},
			AckBatchObserver: deliveryMetricsObserver{metrics: obsmetrics.New(1, "benchmark")},
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}
		if _, err := pusher.Push(context.Background(), cmd); err != nil {
			b.Fatalf("warm Push() error = %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := pusher.Push(context.Background(), cmd)
			if err != nil || len(result.Accepted) != len(cmd.Routes) {
				b.Fatalf("Push() result = %#v error = %v", result, err)
			}
		}
	})
}

func BenchmarkLocalOwnerPusherAckBinding55RoutesParallel(b *testing.B) {
	registry, cmd := ackBatchPushBenchmarkFixture(b, 55)

	b.Run("single_bind_reference", func(b *testing.B) {
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
			AckObserver: &ackBatchBenchmarkObserver{},
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}
		if result, err := localOwnerPushSingleBindReference(pusher, cmd); err != nil || len(result.Accepted) != len(cmd.Routes) {
			b.Fatalf("warm reference result = %#v error = %v", result, err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				result, err := localOwnerPushSingleBindReference(pusher, cmd)
				if err != nil || len(result.Accepted) != len(cmd.Routes) {
					panic("single-bind reference push failed")
				}
			}
		})
	})
	b.Run("batch_bind", func(b *testing.B) {
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
			AckObserver: &ackBatchBenchmarkObserver{},
		})
		pusher := &localOwnerPusher{online: registry, delivery: manager}
		if result, err := pusher.Push(context.Background(), cmd); err != nil || len(result.Accepted) != len(cmd.Routes) {
			b.Fatalf("warm batch result = %#v error = %v", result, err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				result, err := pusher.Push(context.Background(), cmd)
				if err != nil || len(result.Accepted) != len(cmd.Routes) {
					panic("batch push failed")
				}
			}
		})
	})
}

func BenchmarkLocalOwnerPusherAckBindingByRouteCountParallel(b *testing.B) {
	for _, routeCount := range []int{1, 3, 20, 55, 256, 512} {
		b.Run(fmt.Sprintf("routes-%d", routeCount), func(b *testing.B) {
			registry, cmd := ackBatchPushBenchmarkFixture(b, routeCount)
			b.Run("single_bind_reference", func(b *testing.B) {
				manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
					Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
					AckObserver: &ackBatchBenchmarkObserver{},
				})
				pusher := &localOwnerPusher{online: registry, delivery: manager}
				if result, err := localOwnerPushSingleBindReference(pusher, cmd); err != nil || len(result.Accepted) != len(cmd.Routes) {
					b.Fatalf("warm reference result = %#v error = %v", result, err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						result, err := localOwnerPushSingleBindReference(pusher, cmd)
						if err != nil || len(result.Accepted) != len(cmd.Routes) {
							panic("single-bind reference push failed")
						}
					}
				})
			})
			b.Run("batch_bind", func(b *testing.B) {
				manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
					Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
					AckObserver: &ackBatchBenchmarkObserver{},
				})
				pusher := &localOwnerPusher{online: registry, delivery: manager}
				if result, err := pusher.Push(context.Background(), cmd); err != nil || len(result.Accepted) != len(cmd.Routes) {
					b.Fatalf("warm batch result = %#v error = %v", result, err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						result, err := pusher.Push(context.Background(), cmd)
						if err != nil || len(result.Accepted) != len(cmd.Routes) {
							panic("batch push failed")
						}
					}
				})
			})
		})
	}
}

func BenchmarkLocalOwnerPusherAckLifecycleByRouteCountParallel(b *testing.B) {
	for _, routeCount := range []int{1, 3, 20, 55, 256} {
		b.Run(fmt.Sprintf("routes-%d", routeCount), func(b *testing.B) {
			registry, cmd := ackBatchPushBenchmarkFixture(b, routeCount)
			for _, variant := range []struct {
				name  string
				batch bool
			}{
				{name: "single_bind_reference"},
				{name: "batch_bind", batch: true},
			} {
				b.Run(variant.name, func(b *testing.B) {
					manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
						Acks:        runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 32}),
						AckObserver: &ackBatchBenchmarkObserver{},
					})
					pusher := &localOwnerPusher{online: registry, delivery: manager}
					var messageIDs atomic.Uint64
					b.ReportAllocs()
					b.ResetTimer()
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							localCmd := cmd
							localCmd.Envelope.MessageID = messageIDs.Add(1)
							var result runtimedelivery.PushResult
							var err error
							if variant.batch {
								result, err = pusher.Push(context.Background(), localCmd)
							} else {
								result, err = localOwnerPushSingleBindReference(pusher, localCmd)
							}
							if err != nil || len(result.Accepted) != len(localCmd.Routes) {
								panic("ack lifecycle push failed")
							}
							for _, route := range localCmd.Routes {
								if err := manager.Recvack(context.Background(), runtimedelivery.Recvack{
									UID:       route.UID,
									SessionID: route.SessionID,
									MessageID: localCmd.Envelope.MessageID,
								}); err != nil {
									panic("ack lifecycle cleanup failed")
								}
							}
						}
					})
					b.StopTimer()
					if got := manager.PendingAckCount(); got != 0 {
						b.Fatalf("PendingAckCount() = %d, want zero after lifecycle benchmark", got)
					}
				})
			}
		})
	}
}

func benchmarkLocalOwnerPushSingleBind(b *testing.B, pusher *localOwnerPusher, cmd runtimedelivery.PushCommand) {
	if result, err := localOwnerPushSingleBindReference(pusher, cmd); err != nil || len(result.Accepted) != len(cmd.Routes) {
		b.Fatalf("warm reference result = %#v error = %v", result, err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := localOwnerPushSingleBindReference(pusher, cmd)
		if err != nil || len(result.Accepted) != len(cmd.Routes) {
			b.Fatalf("reference result = %#v error = %v", result, err)
		}
	}
}

func localOwnerPushSingleBindReference(pusher *localOwnerPusher, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	pusher.expirePendingAcksIfDue()
	payload := append([]byte(nil), cmd.Envelope.Payload...)
	timestamp := int32(time.Now().Unix())
	var result runtimedelivery.PushResult
	for _, route := range cmd.Routes {
		session, ok := pusher.localSession(route)
		if !ok {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		packet, err := buildRecvPacket(cmd.Envelope, route.UID, payload, timestamp)
		if err != nil {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if !pusher.delivery.BindPendingAck(runtimedelivery.PendingRecvAck{
			UID:         route.UID,
			SessionID:   route.SessionID,
			MessageID:   cmd.Envelope.MessageID,
			MessageSeq:  cmd.Envelope.MessageSeq,
			ChannelID:   cmd.Envelope.ChannelID,
			ChannelType: cmd.Envelope.ChannelType,
		}) {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if err := session.Session.WriteDelivery(packet); err != nil {
			return result, err
		}
		result.Accepted = append(result.Accepted, route)
	}
	return result, nil
}

func ackBatchPushBenchmarkFixture(tb testing.TB, routeCount int) (*online.Registry, runtimedelivery.PushCommand) {
	tb.Helper()
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 32})
	routes := make([]runtimedelivery.Route, 0, routeCount)
	for i := 0; i < routeCount; i++ {
		routes = append(routes, registerAckBatchSession(tb, registry, 1, fmt.Sprintf("u-%d", i), uint64(i+1), &ackBatchSession{}))
	}
	return registry, runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope: runtimedelivery.Envelope{
			MessageID:   9001,
			MessageSeq:  42,
			ChannelID:   "g1",
			ChannelType: 2,
			Payload:     []byte("benchmark-payload"),
		},
		Routes: routes,
	}
}

func registerAckBatchSession(tb testing.TB, registry *online.Registry, ownerNodeID uint64, uid string, sessionID uint64, session online.SessionHandle) runtimedelivery.Route {
	tb.Helper()
	route := online.OwnerRoute{
		UID:           uid,
		OwnerNodeID:   ownerNodeID,
		OwnerBootID:   7,
		OwnerSeq:      sessionID + 100,
		SessionID:     sessionID,
		ConnectedUnix: 100,
	}
	if err := registry.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		tb.Fatalf("RegisterPending(%d) error = %v", sessionID, err)
	}
	if err := registry.MarkActive(sessionID); err != nil {
		tb.Fatalf("MarkActive(%d) error = %v", sessionID, err)
	}
	return runtimedelivery.Route{
		UID:         uid,
		OwnerNodeID: ownerNodeID,
		OwnerBootID: route.OwnerBootID,
		OwnerSeq:    route.OwnerSeq,
		SessionID:   sessionID,
	}
}

type ackBatchSession struct {
	writeErr error
	onWrite  func() error
	writes   atomic.Int64
}

func (s *ackBatchSession) WriteDelivery(any) error {
	s.writes.Add(1)
	if s.onWrite != nil {
		return s.onWrite()
	}
	return s.writeErr
}

func (*ackBatchSession) CloseSession(string) error { return nil }

type ackBatchSequenceSession struct {
	writeErrs []error
	onWrite   func(int64) error
	writes    atomic.Int64
}

func (s *ackBatchSequenceSession) WriteDelivery(any) error {
	call := s.writes.Add(1)
	if s.onWrite != nil {
		if err := s.onWrite(call); err != nil {
			return err
		}
	}
	index := int(call - 1)
	if index >= 0 && index < len(s.writeErrs) {
		return s.writeErrs[index]
	}
	return nil
}

func (*ackBatchSequenceSession) CloseSession(string) error { return nil }

type ackInterleavingSession struct {
	writes   atomic.Int64
	started  [2]chan struct{}
	releases [2]chan error
}

func newAckInterleavingSession() *ackInterleavingSession {
	return &ackInterleavingSession{
		started:  [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		releases: [2]chan error{make(chan error, 1), make(chan error, 1)},
	}
}

func (s *ackInterleavingSession) WriteDelivery(any) error {
	call := int(s.writes.Add(1))
	if call < 1 || call > len(s.started) {
		return fmt.Errorf("unexpected write call %d", call)
	}
	close(s.started[call-1])
	return <-s.releases[call-1]
}

func (*ackInterleavingSession) CloseSession(string) error { return nil }

func (s *ackInterleavingSession) waitForWrite(t *testing.T, call int) {
	t.Helper()
	select {
	case <-s.started[call-1]:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for write call %d", call)
	}
}

func (s *ackInterleavingSession) releaseWrite(call int, err error) {
	s.releases[call-1] <- err
}

func waitAckInterleavingPush(t *testing.T, result <-chan runtimedelivery.PushResult) runtimedelivery.PushResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owner push")
		return runtimedelivery.PushResult{}
	}
}

type ackBatchBenchmarkObserver struct {
	pending atomic.Int64
}

func (o *ackBatchBenchmarkObserver) ObserveAck(event runtimedelivery.AckEvent) {
	o.pending.Store(int64(event.PendingCount))
}

type ackBatchObserverFunc func(runtimedelivery.AckEvent)

func (f ackBatchObserverFunc) ObserveAck(event runtimedelivery.AckEvent) {
	f(event)
}
