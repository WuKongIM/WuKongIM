package delivery

import (
	"context"
	"sync"
	"testing"
	"time"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestLocalOwnerPusherBuildsRecvPacketAndClonesPayload(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &ownerRecordingSession{}
	route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{Online: registry, AckManager: manager})
	envelope := runtimedelivery.Envelope{
		MessageID:   9001,
		MessageSeq:  42,
		ChannelID:   "ch1",
		ChannelType: 2,
		FromUID:     "sender",
		ClientMsgNo: "client-1",
		RedDot:      true,
		Payload:     []byte("hello"),
	}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    envelope,
		Routes:      []runtimedelivery.Route{route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want one accepted", result)
	}
	if len(session.writes) != 1 {
		t.Fatalf("delivery writes = %d, want 1", len(session.writes))
	}
	recv, ok := session.writes[0].(*frame.RecvPacket)
	if !ok {
		t.Fatalf("delivery write = %T, want *frame.RecvPacket", session.writes[0])
	}
	if recv.MessageID != int64(envelope.MessageID) || recv.MessageSeq != envelope.MessageSeq || recv.ChannelID != envelope.ChannelID ||
		recv.ChannelType != envelope.ChannelType || recv.FromUID != envelope.FromUID || recv.ClientMsgNo != envelope.ClientMsgNo ||
		string(recv.Payload) != "hello" || !recv.RedDot {
		t.Fatalf("recv packet = %#v", recv)
	}
	envelope.Payload[0] = 'H'
	if string(recv.Payload) != "hello" {
		t.Fatalf("recv payload = %q, want cloned hello", string(recv.Payload))
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() = %d, want 1", got)
	}
}

func TestLocalOwnerPusherClassifiesTerminalWriteErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "session closed", err: gatewaysession.ErrSessionClosed},
		{name: "outbound overflow", err: gatewaytransport.ErrOutboundBytesExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
			session := &ownerRecordingSession{writeErr: tt.err}
			route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
			manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
			pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{Online: registry, AckManager: manager})

			result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
				OwnerNodeID: 1,
				Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
				Routes:      []runtimedelivery.Route{route},
			})
			if err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
				t.Fatalf("push result = %#v, want one dropped", result)
			}
			if got := manager.PendingAckCount(); got != 0 {
				t.Fatalf("PendingAckCount() = %d, want 0 after terminal write error", got)
			}
		})
	}
}

func TestLocalOwnerPusherExpiresPendingAcksDuringPushActivity(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	route := registerAckBatchSession(t, registry, 1, "u1", 101, &ackBatchSession{})
	now := time.Unix(200, 0)
	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 1,
		Now:        func() int64 { return now.Unix() },
	})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{Acks: tracker})
	manager.BindPendingAck(runtimedelivery.PendingRecvAck{
		UID: "u1", SessionID: 101, MessageID: 1, MessageSeq: 1, DeliveredAt: 100,
	})
	pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{
		Online:        registry,
		AckManager:    manager,
		PendingAckTTL: 50 * time.Second,
		Now:           func() time.Time { return now },
	})

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2},
		Routes:      []runtimedelivery.Route{route},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("accepted routes = %d, want 1", len(result.Accepted))
	}
	if _, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 1}); ok {
		t.Fatal("old pending ACK still exists after delivery-activity expiration")
	}
	if pending, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 2}); !ok || pending.MessageID != 2 {
		t.Fatalf("new pending ACK = %#v, %v, want message 2 true", pending, ok)
	}
}

func TestLocalOwnerPusherThrottlesPendingAckExpiryPerWindow(t *testing.T) {
	clock := &ownerPusherTestClock{now: time.Unix(200, 0)}
	observer := &localOwnerPusherAckObserver{}
	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 4,
		Now:        func() int64 { return clock.Now().Unix() },
	})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{Acks: tracker, AckObserver: observer})
	manager.BindPendingAck(runtimedelivery.PendingRecvAck{
		UID: "expired-first", SessionID: 101, MessageID: 1, DeliveredAt: 100,
	})
	pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{
		AckManager: manager, PendingAckTTL: 50 * time.Second, Now: clock.Now,
	})

	const concurrentPushes = 64
	start := make(chan struct{})
	errC := make(chan error, concurrentPushes)
	var wg sync.WaitGroup
	for i := 0; i < concurrentPushes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{})
			errC <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errC)
	for err := range errC {
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
	}

	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionExpire); got != 1 {
		t.Fatalf("expire ACK events in one window = %d, want 1", got)
	}
	manager.BindPendingAck(runtimedelivery.PendingRecvAck{
		UID: "expired-second", SessionID: 102, MessageID: 2, DeliveredAt: 100,
	})
	if _, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{}); err != nil {
		t.Fatalf("Push(same window) error = %v", err)
	}
	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionExpire); got != 1 {
		t.Fatalf("expire ACK events before next window = %d, want 1", got)
	}
	if got := manager.PendingAckCount(); got != 1 {
		t.Fatalf("PendingAckCount() before next window = %d, want 1", got)
	}

	clock.Advance(time.Second)
	if _, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{}); err != nil {
		t.Fatalf("Push(next window) error = %v", err)
	}
	if got := observer.ActionCount(runtimedelivery.DeliveryAckActionExpire); got != 2 {
		t.Fatalf("expire ACK events after next window = %d, want 2", got)
	}
	if got := manager.PendingAckCount(); got != 0 {
		t.Fatalf("PendingAckCount() after next-window expiry = %d, want 0", got)
	}
}

func TestLocalOwnerPusherDropsInvalidRouteOrPacket(t *testing.T) {
	t.Run("overflow message ID", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		session := &ownerRecordingSession{}
		route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{Online: registry, AckManager: manager})

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: uint64(1 << 63), MessageSeq: 1},
			Routes:      []runtimedelivery.Route{route},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || len(session.writes) != 0 || manager.PendingAckCount() != 0 {
			t.Fatalf("result/writes/pending = %#v/%d/%d, want dropped/0/0", result, len(session.writes), manager.PendingAckCount())
		}
	})

	t.Run("incomplete exact identity", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		session := &ownerRecordingSession{}
		route := registerAckBatchSession(t, registry, 1, "u1", 101, session)
		route.OwnerBootID = 0
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{Online: registry, AckManager: manager})

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
			Routes:      []runtimedelivery.Route{route},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || len(session.writes) != 0 || manager.PendingAckCount() != 0 {
			t.Fatalf("result/writes/pending = %#v/%d/%d, want dropped/0/0", result, len(session.writes), manager.PendingAckCount())
		}
	})

	t.Run("inactive session", func(t *testing.T) {
		registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
		session := &ownerRecordingSession{}
		ownerRoute := online.OwnerRoute{
			UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 100,
		}
		if err := registry.RegisterPending(online.LocalSession{Route: ownerRoute, Session: session}); err != nil {
			t.Fatalf("RegisterPending() error = %v", err)
		}
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
		pusher := NewLocalOwnerPusher(LocalOwnerPusherOptions{Online: registry, AckManager: manager})

		result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
			OwnerNodeID: 1,
			Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
			Routes: []runtimedelivery.Route{{
				UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101,
			}},
		})
		if err != nil {
			t.Fatalf("Push() error = %v", err)
		}
		if len(result.Dropped) != 1 || len(session.writes) != 0 || manager.PendingAckCount() != 0 {
			t.Fatalf("result/writes/pending = %#v/%d/%d, want dropped/0/0", result, len(session.writes), manager.PendingAckCount())
		}
	})
}

type ownerRecordingSession struct {
	writeErr error
	writes   []any
}

func (s *ownerRecordingSession) WriteDelivery(payload any) error {
	s.writes = append(s.writes, payload)
	return s.writeErr
}

func (*ownerRecordingSession) CloseSession(string) error { return nil }

type ownerPusherTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *ownerPusherTestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *ownerPusherTestClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}
