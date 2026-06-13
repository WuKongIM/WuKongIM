//go:build e2e

package suite

import (
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
	"github.com/stretchr/testify/require"
)

func TestWKProtoClientReadSendAckSkipsInterleavedRecv(t *testing.T) {
	ln := newWKProtoTestServer(t, func(conn net.Conn) {
		connect := readConnectPacket(t, conn)
		serverKeys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
		require.NoError(t, err)

		writeFrame(t, conn, &frame.ConnackPacket{
			ServerVersion: frame.LatestVersion,
			ServerKey:     serverKey,
			Salt:          string(serverKeys.AESIV),
			ReasonCode:    frame.ReasonSuccess,
		})

		raw, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		require.NoError(t, err)
		send, ok := raw.(*frame.SendPacket)
		require.Truef(t, ok, "expected *frame.SendPacket, got %T", raw)
		require.NoError(t, wkprotoenc.ValidateSendPacket(send, serverKeys))

		sealed, err := wkprotoenc.SealRecvPacket(&frame.RecvPacket{
			MessageID:   42,
			MessageSeq:  7,
			ChannelID:   "sender",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "sender",
			Payload:     []byte("interleaved recv"),
		}, serverKeys)
		require.NoError(t, err)
		writeFrame(t, conn, sealed)

		writeFrame(t, conn, &frame.SendackPacket{
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   99,
			MessageSeq:  11,
			ReasonCode:  frame.ReasonSuccess,
		})
	})
	defer func() { _ = ln.Close() }()

	client, err := NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Connect(ln.Addr().String(), "sender", "sender-device"))
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ClientSeq:   3,
		ClientMsgNo: "e2ev2-send-ack",
		ChannelID:   "recipient",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello sendack"),
	}))

	ack, err := client.ReadSendAck()
	require.NoError(t, err)
	require.Equal(t, uint64(3), ack.ClientSeq)
	require.Equal(t, "e2ev2-send-ack", ack.ClientMsgNo)
	require.Equal(t, int64(99), ack.MessageID)
	require.Equal(t, uint64(11), ack.MessageSeq)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)

	recv, err := client.ReadRecv()
	require.NoError(t, err)
	require.Equal(t, []byte("interleaved recv"), recv.Payload)
	require.Equal(t, int64(42), recv.MessageID)
	require.Equal(t, uint64(7), recv.MessageSeq)
}

func newWKProtoTestServer(t *testing.T, handler func(net.Conn)) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(conn)
	}()
	return ln
}

func readConnectPacket(t *testing.T, conn net.Conn) *frame.ConnectPacket {
	t.Helper()

	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	require.NoError(t, err)
	connect, ok := f.(*frame.ConnectPacket)
	require.Truef(t, ok, "expected *frame.ConnectPacket, got %T", f)
	return connect
}

func writeFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()

	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	require.NoError(t, err)
	_, err = conn.Write(payload)
	require.NoError(t, err)
}
