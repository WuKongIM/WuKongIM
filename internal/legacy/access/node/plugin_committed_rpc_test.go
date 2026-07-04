package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPluginCommittedCodecRoundTrip(t *testing.T) {
	event := messageevents.MessageCommitted{
		Message: channel.Message{
			MessageID:   12,
			MessageSeq:  34,
			ClientMsgNo: "c1",
			StreamNo:    "s1",
			StreamID:    5,
			Timestamp:   1234,
			FromUID:     "u1",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Topic:       "topic",
			Payload:     []byte("payload"),
		},
		SenderSessionID:                88,
		MessageScopedUIDs:              []string{"u2", "u3"},
		CMDConversationIntentSubmitted: true,
	}

	raw, err := encodePluginCommittedRequest(pluginCommittedRequest{Event: event})
	require.NoError(t, err)
	got, err := decodePluginCommittedRequest(raw)
	require.NoError(t, err)

	require.Equal(t, uint64(12), got.Event.Message.MessageID)
	require.Equal(t, uint64(34), got.Event.Message.MessageSeq)
	require.Equal(t, "c1", got.Event.Message.ClientMsgNo)
	require.Equal(t, "s1", got.Event.Message.StreamNo)
	require.Equal(t, uint64(5), got.Event.Message.StreamID)
	require.Equal(t, int32(1234), got.Event.Message.Timestamp)
	require.Equal(t, "u1", got.Event.Message.FromUID)
	require.Equal(t, "g1", got.Event.Message.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, got.Event.Message.ChannelType)
	require.Equal(t, "topic", got.Event.Message.Topic)
	require.Equal(t, []byte("payload"), got.Event.Message.Payload)
	require.Equal(t, uint64(88), got.Event.SenderSessionID)
	require.Equal(t, []string{"u2", "u3"}, got.Event.MessageScopedUIDs)
	require.True(t, got.Event.CMDConversationIntentSubmitted)
}

func TestPluginCommittedHandlerInvokesProvider(t *testing.T) {
	provider := &recordingPluginCommittedProvider{}
	adapter := New(Options{PluginCommitted: provider})
	body, err := encodePluginCommittedRequest(pluginCommittedRequest{Event: messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 1}}})
	require.NoError(t, err)

	respBody, err := adapter.handlePluginCommittedRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginCommittedResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Len(t, provider.events, 1)
	require.Equal(t, uint64(1), provider.events[0].Message.MessageID)
}

func TestPluginCommittedClientUsesDedicatedNodeRPC(t *testing.T) {
	cluster := &capturingPluginCommittedCluster{response: mustEncodePluginCommittedResponse(t, pluginCommittedResponse{Status: rpcStatusOK})}
	client := NewClient(cluster)
	event := messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 1}}

	require.NoError(t, client.SubmitPluginCommitted(context.Background(), 7, event))

	require.Equal(t, multiraft.NodeID(7), cluster.nodeID)
	require.Equal(t, pluginCommittedRPCServiceID, cluster.serviceID)
	decoded, err := decodePluginCommittedRequest(cluster.payload)
	require.NoError(t, err)
	require.Equal(t, uint64(1), decoded.Event.Message.MessageID)
}

type recordingPluginCommittedProvider struct {
	events []messageevents.MessageCommitted
	err    error
}

func (r *recordingPluginCommittedProvider) PersistAfterCommitted(_ context.Context, event messageevents.MessageCommitted) error {
	r.events = append(r.events, event.Clone())
	return r.err
}

type capturingPluginCommittedCluster struct {
	nodeID    multiraft.NodeID
	serviceID uint8
	payload   []byte
	response  []byte
	err       error
}

func (c *capturingPluginCommittedCluster) RPCMux() *transport.RPCMux { return nil }
func (c *capturingPluginCommittedCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}
func (c *capturingPluginCommittedCluster) IsLocal(multiraft.NodeID) bool      { return false }
func (c *capturingPluginCommittedCluster) SlotForKey(string) multiraft.SlotID { return 0 }
func (c *capturingPluginCommittedCluster) RPCService(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	c.nodeID = nodeID
	c.serviceID = serviceID
	c.payload = append([]byte(nil), payload...)
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}
func (c *capturingPluginCommittedCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func mustEncodePluginCommittedResponse(t *testing.T, resp pluginCommittedResponse) []byte {
	t.Helper()
	body, err := encodePluginCommittedResponse(resp)
	require.NoError(t, err)
	return body
}
