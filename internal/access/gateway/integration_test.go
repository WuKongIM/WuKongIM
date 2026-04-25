package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	codec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

const (
	gatewayTestListenerName = "tcp-wkproto-access-gateway"
	gatewayTestFromUID      = "u1"
	gatewayTestRecipientUID = "u2"
	gatewayTestClientMsgNo  = "m1"
	gatewayTestPayload      = "hi"
	gatewayReadTimeout      = 2 * time.Second
)

func TestGatewayWKProtoHandlerAcknowledgesDurablePersonSend(t *testing.T) {
	registry := online.NewRegistry()
	handler := newGatewayIntegrationHandler(
		newClusterBackedMessageAppWithOnline(registry, channel.AppendResult{
			MessageID:  88,
			MessageSeq: 9,
		}),
		registry,
	)
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())
	t.Cleanup(func() { _ = gw.Stop() })

	senderConn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = senderConn.Close() })

	recipientConn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = recipientConn.Close() })

	senderConnack := connectWKProtoClient(t, senderConn, gatewayTestFromUID)
	require.Equal(t, frame.ReasonSuccess, senderConnack.ReasonCode)

	recipientConnack := connectWKProtoClient(t, recipientConn, gatewayTestRecipientUID)
	require.Equal(t, frame.ReasonSuccess, recipientConnack.ReasonCode)

	const clientSeq uint64 = 1
	sendWKProtoFrame(t, senderConn, &frame.SendPacket{
		ChannelID:   gatewayTestRecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte(gatewayTestPayload),
		ClientSeq:   clientSeq,
		ClientMsgNo: gatewayTestClientMsgNo,
	})

	ack := readSendackPacket(t, senderConn)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Equal(t, clientSeq, ack.ClientSeq)
	require.Equal(t, gatewayTestClientMsgNo, ack.ClientMsgNo)
	require.Equal(t, int64(88), ack.MessageID)
	require.Equal(t, uint64(9), ack.MessageSeq)
}

func TestGatewayWKProtoHandlerRepliesPongToPing(t *testing.T) {
	handler := newGatewayIntegrationHandler(&fakeMessageUsecase{}, nil)
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = conn.Close() })

	connack := connectWKProtoClient(t, conn, gatewayTestFromUID)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	sendWKProtoFrame(t, conn, &frame.PingPacket{})

	reply := readWKProtoFrame(t, conn)
	_, ok := reply.(*frame.PongPacket)
	require.True(t, ok, "expected *frame.PongPacket, got %T", reply)
}

func TestGatewayVersion5ClientGetsUpgradeRequiredOnSend(t *testing.T) {
	handler := newGatewayIntegrationHandler(&fakeMessageUsecase{sendErr: channel.ErrProtocolUpgradeRequired}, nil)
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = conn.Close() })

	connack := connectWKProtoClientVersion(t, conn, gatewayTestFromUID, frame.LegacyMessageSeqVersion)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)
	require.Equal(t, uint8(frame.LegacyMessageSeqVersion), connack.ServerVersion)

	sendWKProtoFrameVersion(t, conn, &frame.SendPacket{
		ChannelID:   gatewayTestRecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte(gatewayTestPayload),
		ClientSeq:   1,
		ClientMsgNo: gatewayTestClientMsgNo,
	}, frame.LegacyMessageSeqVersion)

	ack := readSendackPacketVersion(t, conn, frame.LegacyMessageSeqVersion)
	require.Equal(t, frame.ReasonProtocolUpgradeRequired, ack.ReasonCode)
	require.Zero(t, ack.MessageID)
	require.Zero(t, ack.MessageSeq)
	require.Equal(t, uint64(1), ack.ClientSeq)
	require.Equal(t, gatewayTestClientMsgNo, ack.ClientMsgNo)
}

func TestGatewayWKProtoHandlerPropagatesRequestContextToUsecase(t *testing.T) {
	msgs := &fakeMessageUsecase{
		sendResult: message.SendResult{Reason: frame.ReasonSuccess},
	}
	handler := newGatewayIntegrationHandler(msgs, nil)
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = conn.Close() })

	connack := connectWKProtoClient(t, conn, gatewayTestFromUID)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	sendWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   gatewayTestRecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte(gatewayTestPayload),
		ClientSeq:   1,
		ClientMsgNo: gatewayTestClientMsgNo,
	})

	ack := readSendackPacket(t, conn)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Len(t, msgs.sendContexts, 1)
	require.NotNil(t, msgs.sendContexts[0])
	require.ErrorIs(t, msgs.sendContexts[0].Err(), context.Canceled)
}

func TestGatewayWKProtoHandlerCancelsInFlightSendOnTimeout(t *testing.T) {
	started := make(chan struct{})
	done := make(chan error, 1)
	msgs := &fakeMessageUsecase{
		sendFn: func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
			close(started)
			<-ctx.Done()
			done <- ctx.Err()
			return message.SendResult{}, ctx.Err()
		},
	}
	registry := online.NewRegistry()
	handler := New(Options{
		Messages:    msgs,
		Presence:    newGatewayIntegrationPresence(registry),
		Online:      registry,
		SendTimeout: 50 * time.Millisecond,
		Now:         func() time.Time { return fixedGatewayNow },
	})
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialGateway(t, gw, gatewayTestListenerName)
	connack := connectWKProtoClient(t, conn, gatewayTestFromUID)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	sendWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   gatewayTestRecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte(gatewayTestPayload),
		ClientSeq:   2,
		ClientMsgNo: "m2",
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight send to start")
	}

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight send timeout")
	}

	ack := readSendackPacket(t, conn)
	require.Equal(t, frame.ReasonSystemError, ack.ReasonCode)
	require.Equal(t, uint64(2), ack.ClientSeq)
	require.Equal(t, "m2", ack.ClientMsgNo)
}

func newGatewayIntegrationHandler(msgs MessageUsecase, registry online.Registry) *Handler {
	if registry == nil {
		registry = online.NewRegistry()
	}
	return New(Options{
		Online:   registry,
		Messages: msgs,
		Presence: newGatewayIntegrationPresence(registry),
		Now:      func() time.Time { return fixedGatewayNow },
	})
}

func newGatewayIntegrationPresence(registry online.Registry) PresenceUsecase {
	if registry == nil {
		registry = online.NewRegistry()
	}
	return presence.New(presence.Options{
		LocalNodeID:   1,
		GatewayBootID: 1,
		Online:        registry,
		Router:        fixedGatewayRouter{groupID: 1},
		Now:           func() time.Time { return fixedGatewayNow },
	})
}

type fixedGatewayRouter struct {
	groupID uint64
}

type wkprotoTestConn struct {
	net.Conn
	client *testkit.WKProtoClient
}

func (r fixedGatewayRouter) SlotForKey(string) uint64 {
	if r.groupID == 0 {
		return 1
	}
	return r.groupID
}

func TestGatewayWKProtoHandlerCancelsInFlightSendOnGatewayStop(t *testing.T) {
	started := make(chan struct{})
	done := make(chan error, 1)
	msgs := &fakeMessageUsecase{
		sendFn: func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
			close(started)
			<-ctx.Done()
			done <- ctx.Err()
			return message.SendResult{}, ctx.Err()
		},
	}
	handler := newGatewayIntegrationHandler(msgs, nil)
	handler.sendTimeout = time.Second
	gw, err := coregateway.New(coregateway.Options{
		Handler: handler,
		Authenticator: coregateway.NewWKProtoAuthenticator(coregateway.WKProtoAuthOptions{
			TokenAuthOn: false,
		}),
		Listeners: []coregateway.ListenerOptions{
			binding.TCPWKProto(gatewayTestListenerName, "127.0.0.1:0"),
		},
	})
	require.NoError(t, err)
	require.NoError(t, gw.Start())

	conn := dialGateway(t, gw, gatewayTestListenerName)
	t.Cleanup(func() { _ = conn.Close() })

	connack := connectWKProtoClient(t, conn, gatewayTestFromUID)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	sendWKProtoFrame(t, conn, &frame.SendPacket{
		ChannelID:   gatewayTestRecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte(gatewayTestPayload),
		ClientSeq:   3,
		ClientMsgNo: "m3",
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight send to start")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- gw.Stop()
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gateway stop cancellation")
	}

	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gateway stop")
	}
}

func dialGateway(t *testing.T, gw *coregateway.Gateway, listener string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", gw.ListenerAddr(listener))
	require.NoError(t, err)
	client, err := testkit.NewWKProtoClient()
	require.NoError(t, err)
	return &wkprotoTestConn{Conn: conn, client: client}
}

func connectWKProtoClient(t *testing.T, conn net.Conn, uid string) *frame.ConnackPacket {
	t.Helper()
	return connectWKProtoClientVersion(t, conn, uid, frame.LatestVersion)
}

func connectWKProtoClientVersion(t *testing.T, conn net.Conn, uid string, version uint8) *frame.ConnackPacket {
	t.Helper()

	sendWKProtoFrameVersion(t, conn, &frame.ConnectPacket{
		Version:         version,
		UID:             uid,
		DeviceID:        uid + "-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	}, frame.LatestVersion)

	f := readWKProtoFrameVersion(t, conn, frame.LatestVersion)
	connack, ok := f.(*frame.ConnackPacket)
	require.True(t, ok, "expected *frame.ConnackPacket, got %T", f)
	return connack
}

func sendWKProtoFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()
	sendWKProtoFrameVersion(t, conn, f, frame.LatestVersion)
}

func sendWKProtoFrameVersion(t *testing.T, conn net.Conn, f frame.Frame, version uint8) {
	t.Helper()

	if wrapped, ok := conn.(*wkprotoTestConn); ok {
		switch pkt := f.(type) {
		case *frame.ConnectPacket:
			var err error
			f, err = wrapped.client.UseClientKey(pkt)
			require.NoError(t, err)
		case *frame.SendPacket:
			cloned := *pkt
			require.NoError(t, wrapped.client.EncryptSendPacket(&cloned))
			f = &cloned
		}
	}

	payload, err := codec.New().EncodeFrame(f, version)
	require.NoError(t, err)

	_, err = conn.Write(payload)
	require.NoError(t, err)
}

func readWKProtoFrame(t *testing.T, conn net.Conn) frame.Frame {
	t.Helper()
	return readWKProtoFrameVersion(t, conn, frame.LatestVersion)
}

func readWKProtoFrameVersion(t *testing.T, conn net.Conn, version uint8) frame.Frame {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(gatewayReadTimeout)))
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	f, err := codec.New().DecodePacketWithConn(conn, version)
	require.NoError(t, err)
	if wrapped, ok := conn.(*wkprotoTestConn); ok {
		switch pkt := f.(type) {
		case *frame.ConnackPacket:
			require.NoError(t, wrapped.client.ApplyConnack(pkt))
		case *frame.RecvPacket:
			require.NoError(t, wrapped.client.DecryptRecvPacket(pkt))
		}
	}
	return f
}

func readSendackPacket(t *testing.T, conn net.Conn) *frame.SendackPacket {
	t.Helper()
	return readSendackPacketVersion(t, conn, frame.LatestVersion)
}

func readSendackPacketVersion(t *testing.T, conn net.Conn, version uint8) *frame.SendackPacket {
	t.Helper()

	f := readWKProtoFrameVersion(t, conn, version)
	ack, ok := f.(*frame.SendackPacket)
	require.True(t, ok, "expected *frame.SendackPacket, got %T", f)
	return ack
}

func readRecvPacket(t *testing.T, conn net.Conn) *frame.RecvPacket {
	t.Helper()

	f := readWKProtoFrame(t, conn)
	recv, ok := f.(*frame.RecvPacket)
	require.True(t, ok, "expected *frame.RecvPacket, got %T", f)
	return recv
}
