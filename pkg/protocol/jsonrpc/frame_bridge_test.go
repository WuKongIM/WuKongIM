package jsonrpc

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectParamsToProtoDefaultsLatestVersion(t *testing.T) {
	params := ConnectParams{
		ClientKey:  "client-key",
		DeviceID:   "device-id",
		DeviceFlag: DeviceApp,
		UID:        "user-1",
		Token:      "token-1",
	}

	packet := params.ToProto()

	require.NotNil(t, packet)
	assert.Equal(t, uint8(frame.LatestVersion), packet.Version)
}

func TestToFrameReturnsWKPacketFrame(t *testing.T) {
	f, reqID, err := ToFrame(SendRequest{
		BaseRequest: BaseRequest{
			Jsonrpc: jsonRPCVersion,
			Method:  MethodSend,
			ID:      "req-send-1",
		},
		Params: SendParams{
			ChannelID:   "channel-1",
			ChannelType: 2,
			Payload:     []byte("payload"),
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "req-send-1", reqID)

	sendFrame, ok := f.(*frame.SendPacket)
	require.True(t, ok, "expected *frame.SendPacket, got %T", f)
	assert.Equal(t, frame.SEND, sendFrame.GetFrameType())
}

func TestToFrameSendRequestPreservesEmptyClientMsgNo(t *testing.T) {
	f, reqID, err := ToFrame(SendRequest{
		BaseRequest: BaseRequest{
			Jsonrpc: jsonRPCVersion,
			Method:  MethodSend,
			ID:      "req-send-1",
		},
		Params: SendParams{
			ChannelID:   "channel-1",
			ChannelType: 2,
			Payload:     []byte("payload"),
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "req-send-1", reqID)

	sendFrame, ok := f.(*frame.SendPacket)
	require.True(t, ok, "expected *frame.SendPacket, got %T", f)
	assert.Empty(t, sendFrame.ClientMsgNo)
}

func TestSendRequestToProtoPreservesEmptyClientMsgNo(t *testing.T) {
	packet, err := (SendRequest{
		BaseRequest: BaseRequest{
			Jsonrpc: jsonRPCVersion,
			Method:  MethodSend,
			ID:      "req-send-2",
		},
		Params: SendParams{
			ChannelID:   "channel-1",
			ChannelType: 2,
			Payload:     []byte("payload"),
		},
	}).ToProto()

	require.NoError(t, err)
	require.NotNil(t, packet)
	assert.Empty(t, packet.ClientMsgNo)
}

func TestSendParamsToProtoPreservesEmptyClientMsgNo(t *testing.T) {
	packet := SendParams{
		ChannelID:   "channel-1",
		ChannelType: 2,
		Payload:     []byte("payload"),
	}.ToProto()

	require.NotNil(t, packet)
	assert.Empty(t, packet.ClientMsgNo)
}

func TestFromFrameAcceptsWKPacketConnackPacket(t *testing.T) {
	msg, err := FromFrame("req-connect-1", &frame.ConnackPacket{
		Framer: frame.Framer{
			HasServerVersion: true,
		},
		ServerVersion: 4,
		ServerKey:     "server-key",
		Salt:          "salt",
		TimeDiff:      12,
		ReasonCode:    frame.ReasonSuccess,
		NodeId:        99,
	})

	require.NoError(t, err)

	resp, ok := msg.(ConnectResponse)
	require.True(t, ok, "expected ConnectResponse, got %T", msg)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "req-connect-1", resp.ID)
	assert.Equal(t, 4, resp.Result.ServerVersion)
	assert.Equal(t, ReasonCodeEnum(frame.ReasonSuccess), resp.Result.ReasonCode)
	assert.Equal(t, uint64(99), resp.Result.NodeID)
}

func TestFromProtoSendAckPreservesUint64MessageSeq(t *testing.T) {
	result := FromProtoSendAck(&frame.SendackPacket{
		MessageID:  99,
		MessageSeq: uint64(^uint32(0)) + 33,
		ReasonCode: frame.ReasonSuccess,
	})

	require.NotNil(t, result)
	assert.Equal(t, uint64(^uint32(0))+33, result.MessageSeq)
}

func TestFromProtoRecvPacketMapsStreamIDFromStreamId(t *testing.T) {
	params := FromProtoRecvPacket(&frame.RecvPacket{
		StreamNo: "stream-no",
		StreamId: 42,
	})

	assert.Equal(t, "stream-no", params.StreamNo)
	assert.Equal(t, "42", params.StreamID)
}
