//go:build e2e && legacy_e2e

package suite

import (
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
	"github.com/stretchr/testify/require"
)

func TestWKProtoClientDecryptsRecvPacketAfterConnack(t *testing.T) {
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

		sealed, err := wkprotoenc.SealRecvPacket(&frame.RecvPacket{
			MessageID:   42,
			MessageSeq:  7,
			ChannelID:   "u1",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u1",
			Payload:     []byte("hello encrypted"),
		}, serverKeys)
		require.NoError(t, err)
		writeFrame(t, conn, sealed)
	})
	defer func() { _ = ln.Close() }()

	client, err := NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Connect(ln.Addr().String(), "u2", "u2-device"))
	recv, err := client.ReadRecv()
	require.NoError(t, err)
	require.Equal(t, []byte("hello encrypted"), recv.Payload)
	require.Equal(t, int64(42), recv.MessageID)
	require.Equal(t, uint64(7), recv.MessageSeq)
}

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
		ClientMsgNo: "e2e-send-ack",
		ChannelID:   "recipient",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello sendack"),
	}))

	ack, err := client.ReadSendAck()
	require.NoError(t, err)
	require.Equal(t, uint64(3), ack.ClientSeq)
	require.Equal(t, "e2e-send-ack", ack.ClientMsgNo)
	require.Equal(t, int64(99), ack.MessageID)
	require.Equal(t, uint64(11), ack.MessageSeq)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)

	recv, err := client.ReadRecv()
	require.NoError(t, err)
	require.Equal(t, []byte("interleaved recv"), recv.Payload)
	require.Equal(t, int64(42), recv.MessageID)
	require.Equal(t, uint64(7), recv.MessageSeq)
}
