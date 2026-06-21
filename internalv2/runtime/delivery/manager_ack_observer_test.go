package delivery

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerAckObserverReportsPendingTransitions(t *testing.T) {
	observer := &recordingAckObserver{}
	manager := NewManager(ManagerOptions{
		Acks:        newAckTrackerWithClock(100),
		AckObserver: observer,
	})

	if ok := manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
		t.Fatalf("BindPendingAck() ok = false, want true")
	}
	if err := manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if err := manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 404}); err != nil {
		t.Fatalf("Recvack(miss) error = %v", err)
	}

	want := []AckEvent{
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionAck, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 0},
		{Action: DeliveryAckActionAck, Result: DeliveryAckResultMiss, Changed: 0, PendingCount: 0},
	}
	if !reflect.DeepEqual(observer.Events(), want) {
		t.Fatalf("ack events = %#v, want %#v", observer.Events(), want)
	}
}

func TestManagerAckObserverReportsCloseExpireAndRejectedBind(t *testing.T) {
	now := int64(200)
	observer := &recordingAckObserver{}
	manager := NewManager(ManagerOptions{
		Acks: NewAckTracker(AckTrackerOptions{
			ShardCount:           4,
			MaxPendingPerSession: 1,
			Now: func() int64 {
				return now
			},
		}),
		AckObserver: observer,
	})

	manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100})
	manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002, DeliveredAt: 100})
	manager.BindPendingAck(PendingRecvAck{UID: "u2", SessionID: 20, MessageID: 2001, DeliveredAt: 100})
	if err := manager.SessionClosed(context.Background(), SessionClosed{UID: "u2", SessionID: 20}); err != nil {
		t.Fatalf("SessionClosed() error = %v", err)
	}
	manager.ExpirePendingAcks(50 * time.Second)

	want := []AckEvent{
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultRejected, Changed: 0, PendingCount: 1},
		{Action: DeliveryAckActionBind, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 2},
		{Action: DeliveryAckActionSessionClosed, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 1},
		{Action: DeliveryAckActionExpire, Result: DeliveryAckResultOK, Changed: 1, PendingCount: 0},
	}
	if !reflect.DeepEqual(observer.Events(), want) {
		t.Fatalf("ack events = %#v, want %#v", observer.Events(), want)
	}
}

func TestManagerAckObserverSerializesMutationAndObservation(t *testing.T) {
	observer := newDelayedBindAckObserver()
	manager := NewManager(ManagerOptions{
		Acks:        newAckTrackerWithClock(100),
		AckObserver: observer,
	})

	bindDone := make(chan bool, 1)
	go func() {
		bindDone <- manager.BindPendingAck(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001})
	}()

	select {
	case <-observer.bindBlocked:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bind observation")
	}

	ackDone := make(chan error, 1)
	go func() {
		ackDone <- manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
	}()
	select {
	case <-observer.ackObserved:
	case <-time.After(50 * time.Millisecond):
	}
	close(observer.releaseBind)

	select {
	case ok := <-bindDone:
		if !ok {
			t.Fatalf("BindPendingAck() ok = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bind to finish")
	}
	select {
	case err := <-ackDone:
		if err != nil {
			t.Fatalf("Recvack() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recvack to finish")
	}

	events := observer.Events()
	if len(events) == 0 {
		t.Fatalf("ack events = nil, want final event")
	}
	last := events[len(events)-1]
	if got := manager.PendingAckCount(); last.PendingCount != got {
		t.Fatalf("last ack event PendingCount = %d, tracker count = %d, events = %#v", last.PendingCount, got, events)
	}
}

func newAckTrackerWithClock(now int64) *AckTracker {
	return NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})
}

type recordingAckObserver struct {
	events []AckEvent
}

func (o *recordingAckObserver) ObserveAck(event AckEvent) {
	o.events = append(o.events, event)
}

func (o *recordingAckObserver) Events() []AckEvent {
	return append([]AckEvent(nil), o.events...)
}

type delayedBindAckObserver struct {
	bindBlocked chan struct{}
	ackObserved chan struct{}
	releaseBind chan struct{}
	blocked     atomic.Bool
	acked       atomic.Bool

	mu     sync.Mutex
	events []AckEvent
}

func newDelayedBindAckObserver() *delayedBindAckObserver {
	return &delayedBindAckObserver{
		bindBlocked: make(chan struct{}),
		ackObserved: make(chan struct{}),
		releaseBind: make(chan struct{}),
	}
}

func (o *delayedBindAckObserver) ObserveAck(event AckEvent) {
	if event.Action == DeliveryAckActionBind && event.PendingCount == 1 && o.blocked.CompareAndSwap(false, true) {
		close(o.bindBlocked)
		<-o.releaseBind
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
	if event.Action == DeliveryAckActionAck && o.acked.CompareAndSwap(false, true) {
		close(o.ackObserved)
	}
}

func (o *delayedBindAckObserver) Events() []AckEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]AckEvent(nil), o.events...)
}
