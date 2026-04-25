//go:build e2e

package single_node_send_message

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestSingleNodeSendMessage(t *testing.T) {
	s := suite.New(t)
	node := s.StartSingleNodeCluster()

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	require.NoError(t, sender.Connect(node.GatewayAddr(), "u1", "u1-device"))
	require.NoError(t, recipient.Connect(node.GatewayAddr(), "u2", "u2-device"))

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   1,
		ClientMsgNo: "e2e-msg-1",
		Payload:     []byte("hello from e2e"),
	}))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err)
	require.Equal(t, "u1", recv.FromUID)
	require.Equal(t, "u1", recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, []byte("hello from e2e"), recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}
