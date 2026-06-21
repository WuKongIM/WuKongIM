package delivery

import (
	"context"
	"reflect"
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
