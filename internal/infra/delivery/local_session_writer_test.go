package delivery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestLocalSessionWriterClassifiesExactSessionWritesWithoutAckState(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &localSessionWriterTestSession{}
	route := registerLocalSessionWriterTestSession(t, registry, 1, "u1", 10, session)
	writer := NewLocalSessionWriter(LocalSessionWriterOptions{Online: registry})
	write := runtimedelivery.LocalSessionWrite{
		Event: channelappendcontract.CommittedEnvelope{
			MessageID: 1, MessageSeq: 2, ChannelID: "room", ChannelType: 2, Payload: []byte("payload"),
		},
		Route: route,
	}

	if result := writer.WriteSession(context.Background(), write); result.Disposition != runtimedelivery.SessionWriteAccepted {
		t.Fatalf("accepted write result = %#v", result)
	}
	if session.writes.Load() != 1 {
		t.Fatalf("session writes = %d, want 1", session.writes.Load())
	}
	packet := session.last.Load()
	if packet == nil || packet.MessageID != 1 || packet.MessageSeq != 2 || string(packet.Payload) != "payload" {
		t.Fatalf("written packet = %#v", packet)
	}

	stale := write
	stale.Route.OwnerSeq++
	if result := writer.WriteSession(context.Background(), stale); result.Disposition != runtimedelivery.SessionWriteDropped {
		t.Fatalf("stale write result = %#v, want dropped", result)
	}
	if session.writes.Load() != 1 {
		t.Fatalf("stale route reached physical session; writes = %d", session.writes.Load())
	}

	session.writeErr = errors.New("temporary")
	if result := writer.WriteSession(context.Background(), write); result.Disposition != runtimedelivery.SessionWriteRetryable {
		t.Fatalf("temporary write result = %#v, want retryable", result)
	}
}

func TestLocalSessionWriterClassifiesMissingRegistryAsRetryable(t *testing.T) {
	result := NewLocalSessionWriter(LocalSessionWriterOptions{}).WriteSession(
		context.Background(),
		runtimedelivery.LocalSessionWrite{},
	)

	if result.Disposition != runtimedelivery.SessionWriteRetryable ||
		!errors.Is(result.Err, runtimedelivery.ErrSessionWriterUnavailable) {
		t.Fatalf("missing registry result = %#v, want retryable unavailable", result)
	}
}

func TestLocalSessionWriterClassifiesPacketBuildAndTerminalWriteFailures(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &localSessionWriterTestSession{}
	route := registerLocalSessionWriterTestSession(t, registry, 1, "u1", 10, session)
	writer := NewLocalSessionWriter(LocalSessionWriterOptions{Online: registry})
	write := runtimedelivery.LocalSessionWrite{
		Event: channelappendcontract.CommittedEnvelope{MessageID: uint64(1 << 63)},
		Route: route,
	}

	result := writer.WriteSession(context.Background(), write)
	if result.Disposition != runtimedelivery.SessionWriteDropped || !errors.Is(result.Err, errOnlineDeliveryMessageIDOverflow) {
		t.Fatalf("overflow result = %#v, want terminal packet-build drop", result)
	}
	if session.writes.Load() != 0 {
		t.Fatalf("overflow physical writes = %d, want 0", session.writes.Load())
	}

	write.Event.MessageID = 1
	for _, terminalErr := range []error{gatewaysession.ErrSessionClosed, gatewaytransport.ErrOutboundBytesExceeded} {
		session.writeErr = terminalErr
		result = writer.WriteSession(context.Background(), write)
		if result.Disposition != runtimedelivery.SessionWriteDropped || !errors.Is(result.Err, terminalErr) {
			t.Fatalf("terminal error %v result = %#v, want dropped", terminalErr, result)
		}
	}
}

func TestBuildOnlineDeliveryRecvPacketUsesRecipientPersonView(t *testing.T) {
	payload := []byte("shared")
	event := channelappendcontract.CommittedEnvelope{
		MessageID: 1, MessageSeq: 2, ClientMsgNo: "c1", ChannelType: frame.ChannelTypePerson,
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), FromUID: "u1", RedDot: true, Payload: payload,
	}

	packet, err := buildOnlineDeliveryRecvPacket(event, "u2", 123)
	if err != nil {
		t.Fatalf("buildOnlineDeliveryRecvPacket() error = %v", err)
	}
	if packet.ChannelID != "u1" || packet.Timestamp != 123 || packet.FromUID != "u1" || !packet.RedDot {
		t.Fatalf("recipient packet = %#v", packet)
	}
	if &packet.Payload[0] != &payload[0] {
		t.Fatal("packet construction cloned immutable payload before serialization")
	}
}

type localSessionWriterTestSession struct {
	writeErr error
	writes   atomic.Int64
	last     atomic.Pointer[frame.RecvPacket]
}

func (s *localSessionWriterTestSession) WriteDelivery(value any) error {
	s.writes.Add(1)
	if packet, ok := value.(*frame.RecvPacket); ok {
		retained := *packet
		retained.Payload = append([]byte(nil), packet.Payload...)
		s.last.Store(&retained)
	}
	return s.writeErr
}

func (*localSessionWriterTestSession) CloseSession(string) error { return nil }

func registerLocalSessionWriterTestSession(
	t *testing.T,
	registry *online.Registry,
	ownerNodeID uint64,
	uid string,
	sessionID uint64,
	session online.SessionHandle,
) onlinedelivery.Route {
	t.Helper()
	route := online.OwnerRoute{
		UID: uid, OwnerNodeID: ownerNodeID, OwnerBootID: 7,
		OwnerSeq: sessionID + 100, SessionID: sessionID, ConnectedUnix: 100,
	}
	if err := registry.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending(%d) error = %v", sessionID, err)
	}
	if err := registry.MarkActive(sessionID); err != nil {
		t.Fatalf("MarkActive(%d) error = %v", sessionID, err)
	}
	return onlinedelivery.Route{
		UID: uid, OwnerNodeID: ownerNodeID, OwnerBootID: route.OwnerBootID,
		OwnerSeq: route.OwnerSeq, SessionID: sessionID,
	}
}
