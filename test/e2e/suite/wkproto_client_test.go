//go:build e2e

package suite

import (
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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
