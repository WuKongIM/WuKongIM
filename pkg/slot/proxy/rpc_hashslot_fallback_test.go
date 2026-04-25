package proxy

import (
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestHandleIdentityRPCComputesHashSlotFromUID(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "identity-fallback")
	hashSlot := nodes[0].cluster.HashSlotForKey(uid)
	require.NotEqual(t, uint16(2), hashSlot)

	want := metadb.User{UID: uid, Token: "identity-token"}
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateUser(ctx, want))

	body, err := json.Marshal(identityRPCRequest{
		Op:     identityRPCGetUser,
		SlotID: 2,
		UID:    uid,
	})
	require.NoError(t, err)

	respBody, err := nodes[1].store.handleIdentityRPC(ctx, body)
	require.NoError(t, err)

	resp, err := decodeIdentityRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.NotNil(t, resp.User)
	require.Equal(t, want, *resp.User)
}

func TestHandleRuntimeMetaRPCComputesHashSlotFromChannelID(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "runtime-fallback")
	hashSlot := nodes[0].cluster.HashSlotForKey(channelID)
	require.NotEqual(t, uint16(2), hashSlot)

	want := metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  2,
		ChannelEpoch: 3,
		LeaderEpoch:  4,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       2,
		MinISR:       1,
		Status:       1,
	}
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  2,
		ChannelEpoch: 3,
		LeaderEpoch:  4,
		Replicas:     []uint64{2, 1},
		ISR:          []uint64{2, 1},
		Leader:       2,
		MinISR:       1,
		Status:       1,
	}))

	body, err := json.Marshal(runtimeMetaRPCRequest{
		Op:          runtimeMetaRPCGet,
		SlotID:      2,
		ChannelID:   channelID,
		ChannelType: 2,
	})
	require.NoError(t, err)

	respBody, err := nodes[1].store.handleRuntimeMetaRPC(ctx, body)
	require.NoError(t, err)

	resp, err := decodeRuntimeMetaRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.NotNil(t, resp.Meta)
	require.Equal(t, want, *resp.Meta)
}

func TestHandleSubscriberRPCFallsBackToChannelHashSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "subscriber-fallback")
	hashSlot := nodes[0].cluster.HashSlotForKey(channelID)
	require.NotEqual(t, uint16(2), hashSlot)

	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).AddSubscribers(ctx, channelID, 2, []string{"u3", "u1", "u2"}))

	body, err := json.Marshal(subscriberRPCRequest{
		SlotID:      2,
		ChannelID:   channelID,
		ChannelType: 2,
		Snapshot:    true,
	})
	require.NoError(t, err)

	respBody, err := nodes[1].store.handleSubscriberRPC(ctx, body)
	require.NoError(t, err)

	resp, err := decodeSubscriberRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []string{"u1", "u2", "u3"}, resp.UIDs)
	require.True(t, resp.Done)
}

func TestHandleUserConversationStateRPCFallsBackToUIDHashSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "conversation-fallback")
	hashSlot := nodes[0].cluster.HashSlotForKey(uid)
	require.NotEqual(t, uint16(2), hashSlot)

	want := metadb.UserConversationState{
		UID:         uid,
		ChannelID:   "g-fallback",
		ChannelType: 2,
		ReadSeq:     9,
		DeletedToSeq: 5,
		ActiveAt:    1234,
	}
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertUserConversationState(ctx, want))

	body, err := json.Marshal(userConversationStateRPCRequest{
		Op:          userConversationStateRPCGet,
		SlotID:      2,
		UID:         uid,
		ChannelID:   want.ChannelID,
		ChannelType: want.ChannelType,
	})
	require.NoError(t, err)

	respBody, err := nodes[1].store.handleUserConversationStateRPC(ctx, body)
	require.NoError(t, err)

	resp, err := decodeUserConversationStateRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.NotNil(t, resp.State)
	require.Equal(t, want, *resp.State)
}
