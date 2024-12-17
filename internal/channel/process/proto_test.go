package process

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestAllowSendReq_EncodeDecode(t *testing.T) {
	original := &allowSendReq{
		From: "user1",
		To:   "user2",
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &allowSendReq{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.From, decoded.From)
	assert.Equal(t, original.To, decoded.To)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestSendackReq_EncodeDecode(t *testing.T) {
	original := &sendackReq{
		framer:       1,
		protoVersion: 2,
		messageId:    12345,
		messageSeq:   67890,
		clientSeq:    111213,
		clientMsgNo:  "msg123",
		reasonCode:   0,
		fromUid:      "user1",
		FromNode:     141516,
		ConnId:       171819,
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &sendackReq{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.framer, decoded.framer)
	assert.Equal(t, original.protoVersion, decoded.protoVersion)
	assert.Equal(t, original.messageId, decoded.messageId)
	assert.Equal(t, original.messageSeq, decoded.messageSeq)
	assert.Equal(t, original.clientSeq, decoded.clientSeq)
	assert.Equal(t, original.clientMsgNo, decoded.clientMsgNo)
	assert.Equal(t, original.reasonCode, decoded.reasonCode)
	assert.Equal(t, original.fromUid, decoded.fromUid)
	assert.Equal(t, original.FromNode, decoded.FromNode)
	assert.Equal(t, original.ConnId, decoded.ConnId)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestSendackBatchReq_EncodeDecode(t *testing.T) {
	original := sendackBatchReq{
		&sendackReq{
			framer:       1,
			protoVersion: 2,
			messageId:    12345,
			messageSeq:   67890,
			clientSeq:    111213,
			clientMsgNo:  "msg123",
			reasonCode:   0,
			fromUid:      "user1",
			FromNode:     141516,
			ConnId:       171819,
		},
		&sendackReq{
			framer:       3,
			protoVersion: 4,
			messageId:    54321,
			messageSeq:   98765,
			clientSeq:    313211,
			clientMsgNo:  "msg321",
			reasonCode:   1,
			fromUid:      "user2",
			FromNode:     616141,
			ConnId:       918171,
		},
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	var decoded sendackBatchReq
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, len(original), len(decoded))
	for i := range original {
		assert.Equal(t, original[i].framer, decoded[i].framer)
		assert.Equal(t, original[i].protoVersion, decoded[i].protoVersion)
		assert.Equal(t, original[i].messageId, decoded[i].messageId)
		assert.Equal(t, original[i].messageSeq, decoded[i].messageSeq)
		assert.Equal(t, original[i].clientSeq, decoded[i].clientSeq)
		assert.Equal(t, original[i].clientMsgNo, decoded[i].clientMsgNo)
		assert.Equal(t, original[i].reasonCode, decoded[i].reasonCode)
		assert.Equal(t, original[i].fromUid, decoded[i].fromUid)
		assert.Equal(t, original[i].FromNode, decoded[i].FromNode)
		assert.Equal(t, original[i].ConnId, decoded[i].ConnId)
	}

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestOutboundReq_EncodeDecode(t *testing.T) {
	original := &outboundReq{
		channelId:   "channel1",
		channelType: 1,
		fromNode:    12345,
		messages: reactor.ChannelMessageBatch{
			&reactor.ChannelMessage{
				FakeChannelId: "testChannel",
				ChannelType:   1,
				Index:         12345,
				MsgType:       reactor.ChannelMsgSend,
				ToNode:        67890,
				MessageId:     111213,
				MessageSeq:    141516,
				ReasonCode:    wkproto.ReasonCode(0),
			},
		},
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &outboundReq{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.channelId, decoded.channelId)
	assert.Equal(t, original.channelType, decoded.channelType)
	assert.Equal(t, original.fromNode, decoded.fromNode)
	assert.Equal(t, len(original.messages), len(decoded.messages))
	for i := range original.messages {
		assert.Equal(t, original.messages[i].FakeChannelId, decoded.messages[i].FakeChannelId)
		assert.Equal(t, original.messages[i].ChannelType, decoded.messages[i].ChannelType)
		assert.Equal(t, original.messages[i].Index, decoded.messages[i].Index)
		assert.Equal(t, original.messages[i].MsgType, decoded.messages[i].MsgType)
		assert.Equal(t, original.messages[i].ToNode, decoded.messages[i].ToNode)
		assert.Equal(t, original.messages[i].MessageId, decoded.messages[i].MessageId)
		assert.Equal(t, original.messages[i].MessageSeq, decoded.messages[i].MessageSeq)
		assert.Equal(t, original.messages[i].ReasonCode, decoded.messages[i].ReasonCode)
	}

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestChannelJoinReq_EncodeDecode(t *testing.T) {
	original := &channelJoinReq{
		channelId:   "channel1",
		channelType: 1,
		from:        12345,
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &channelJoinReq{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.channelId, decoded.channelId)
	assert.Equal(t, original.channelType, decoded.channelType)
	assert.Equal(t, original.from, decoded.from)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestChannelJoinResp_EncodeDecode(t *testing.T) {
	original := &channelJoinResp{
		channelId:   "channel1",
		channelType: 1,
		from:        12345,
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &channelJoinResp{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.channelId, decoded.channelId)
	assert.Equal(t, original.channelType, decoded.channelType)
	assert.Equal(t, original.from, decoded.from)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestNodeHeartbeatReq_EncodeDecode(t *testing.T) {
	original := &nodeHeartbeatReq{
		channelId:   "channel1",
		channelType: 1,
		fromNode:    12345,
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &nodeHeartbeatReq{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.channelId, decoded.channelId)
	assert.Equal(t, original.channelType, decoded.channelType)
	assert.Equal(t, original.fromNode, decoded.fromNode)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}

func TestNodeHeartbeatResp_EncodeDecode(t *testing.T) {
	original := &nodeHeartbeatResp{
		channelId:   "channel1",
		channelType: 1,
		fromNode:    12345,
	}

	encoded, err := original.encode()
	assert.NoError(t, err)

	decoded := &nodeHeartbeatResp{}
	err = decoded.decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.channelId, decoded.channelId)
	assert.Equal(t, original.channelType, decoded.channelType)
	assert.Equal(t, original.fromNode, decoded.fromNode)

	// Test error case
	err = decoded.decode([]byte("invalid data"))
	assert.Error(t, err)
}
