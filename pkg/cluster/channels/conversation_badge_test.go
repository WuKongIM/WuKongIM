package channels

import (
	"context"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

func TestConversationBadgeSurvivesSparseSequencesAndRemoteRouting(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "badge-barrier-history", Type: 2}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	records := make([]ch.Record, 11)
	for i := range records {
		seq := uint64(i + 1)
		records[i] = ch.Record{ID: 100 + seq, Payload: []byte("barrier"), SyncOnce: true}
		if seq == 1 || seq == 4 || seq == 7 || seq == 10 {
			records[i].SyncOnce = false
			records[i].FromUID = "sender"
		}
	}
	_, err = store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: records})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{2}, ISR: []ch.NodeID{2}, MinISR: 1, Status: ch.StatusActive}})
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2, MetaSource: source, Store: factory, Forward: client})
	require.NoError(t, err)
	RegisterServiceHandlers(network, 2, leader)
	remote, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Forward: client})
	require.NoError(t, err)
	for _, service := range []*Service{leader, remote} {
		for _, tc := range []struct {
			uid                          string
			floor, keep, count, boundary uint64
		}{{"receiver", 0, 2, 4, 4}, {"receiver", 2, 1, 3, 7}, {"receiver", 4, 0, 2, 10}, {"receiver", 11, 0, 0, 11}, {"sender", 0, 0, 0, 10}} {
			result, err := service.ReadConversationHeads(ctx, []ch.ChannelID{id}, tc.uid, ConversationBadgeQuery{AfterSeq: tc.floor, KeepUnread: &tc.keep})
			require.NoError(t, err)
			require.NoError(t, result[0].Err)
			head := result[0].Head
			floor := max(tc.floor, head.CurrentUserLastSendSeq)
			count := uint64(0)
			if head.ReadThroughSeq > floor {
				count = head.ReadThroughSeq - floor - head.NonBusinessUnread
			}
			require.Equal(t, tc.count, count)
			require.True(t, head.BoundaryComputed)
			require.Equal(t, tc.boundary, head.UnreadBoundary)
			require.Equal(t, uint64(11), head.ReadThroughSeq)
			require.Equal(t, uint64(10), head.Message.MessageSeq)
		}
	}
}

func TestConversationBadgeCodecRequiresVersionTenAndPreservesExpire(t *testing.T) {
	keep := uint64(0)
	req := ConversationHeadsRequest{UID: "receiver", Items: []ConversationHeadRequest{{ChannelID: ch.ChannelID{ID: "room", Type: 2}, Badge: ConversationBadgeQuery{AfterSeq: 7, KeepUnread: &keep}}}}
	encoded, err := encodeConversationHeadsRequest(req)
	require.NoError(t, err)
	decoded, err := decodeConversationHeadsRequest(encoded)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(req, decoded))
	_, err = encodeConversationHeadsRequestVersion(req, legacyCodecVersionV9)
	require.Error(t, err)
	assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error { _, err := decodeConversationHeadsRequest(data); return err })
	response := ConversationHeadsResponse{Items: []ConversationHeadResult{{Head: ConversationHead{ReadThroughSeq: 11, NonBusinessUnread: 7, UnreadBoundary: 4, BoundaryComputed: true, Found: true, Message: ch.Message{MessageID: 77, MessageSeq: 10, Expire: 3600}}}}}
	encoded, err = encodeConversationHeadsResponse(response)
	require.NoError(t, err)
	actual, err := decodeConversationHeadsResponse(encoded)
	require.NoError(t, err)
	require.Equal(t, response, actual)
	assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error { _, err := decodeConversationHeadsResponse(data); return err })
	// Version 9 still carries Expire; introducing badge fields must not move its
	// original protocol feature boundary.
	msg := codecContractMessage(1)
	msg.Expire = 3600
	encoded, err = encodeRPCResultVersion(legacyCodecVersionV9, kindLastVisibleResponse, LastVisibleResponse{Found: true, Message: msg}, nil)
	require.NoError(t, err)
	var old LastVisibleResponse
	require.NoError(t, decodeRPCResult(encoded, kindLastVisibleResponse, &old))
	require.Equal(t, uint32(3600), old.Message.Expire)
}
