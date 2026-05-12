package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestCMDConversationStateRPCBinaryCodecRoundTripsOperations(t *testing.T) {
	tests := []cmdConversationStateRPCRequest{
		{
			Op:          cmdConversationStateRPCGet,
			SlotID:      2,
			HashSlot:    7,
			UID:         "u1",
			ChannelID:   "g1____cmd",
			ChannelType: 2,
			States:      []metadb.CMDConversationState{},
			Patches:     []metadb.CMDConversationReadPatch{},
		},
		{
			Op:       cmdConversationStateRPCList,
			SlotID:   2,
			HashSlot: 7,
			UID:      "u1",
			Limit:    64,
			States:   []metadb.CMDConversationState{},
			Patches:  []metadb.CMDConversationReadPatch{},
		},
		{
			Op:       cmdConversationStateRPCUpsert,
			SlotID:   2,
			HashSlot: 7,
			States: []metadb.CMDConversationState{{
				UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 4, ActiveAt: 5, UpdatedAt: 6,
			}},
			Patches: []metadb.CMDConversationReadPatch{},
		},
		{
			Op:       cmdConversationStateRPCAdvanceRead,
			SlotID:   2,
			HashSlot: 7,
			States:   []metadb.CMDConversationState{},
			Patches: []metadb.CMDConversationReadPatch{{
				UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 8, UpdatedAt: 9,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.Op, func(t *testing.T) {
			body, err := encodeCMDConversationStateRPCRequestBinary(tt)
			require.NoError(t, err)
			require.True(t, isCMDConversationStateRPCRequestBinary(body))

			got, err := decodeCMDConversationStateRPCRequest(body)
			require.NoError(t, err)
			require.Equal(t, tt, got)
		})
	}

	resp := cmdConversationStateRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		State:    &metadb.CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 4, ActiveAt: 5, UpdatedAt: 6},
		States:   []metadb.CMDConversationState{{UID: "u2", ChannelID: "g2____cmd", ChannelType: 3, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 9, UpdatedAt: 10}},
	}
	body, err := encodeCMDConversationStateRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isCMDConversationStateRPCResponseBinary(body))

	gotResp, err := decodeCMDConversationStateRPCResponse(body)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestStoreCMDConversationStateRoutesToUIDOwnerAndStaysIsolatedFromChat(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	localUID := findUIDForSlot(t, nodes[0].cluster, 1, "local-cmd-state")
	remoteUID := findUIDForSlot(t, nodes[0].cluster, 2, "remote-cmd-state")
	localHashSlot := mustHashSlotForKey(t, nodes[0].cluster, localUID)
	remoteHashSlot := mustHashSlotForKey(t, nodes[0].cluster, remoteUID)

	require.NoError(t, nodes[0].store.UpsertCMDConversationStates(ctx, []metadb.CMDConversationState{
		{UID: localUID, ChannelID: "local____cmd", ChannelType: 2, ReadSeq: 1, ActiveAt: 100, UpdatedAt: 10},
		{UID: remoteUID, ChannelID: "old____cmd", ChannelType: 2, ReadSeq: 2, ActiveAt: 200, UpdatedAt: 20},
		{UID: remoteUID, ChannelID: "new____cmd", ChannelType: 2, ReadSeq: 3, ActiveAt: 300, UpdatedAt: 30},
	}))

	localState, err := nodes[0].db.ForHashSlot(localHashSlot).GetCMDConversationState(ctx, localUID, "local____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), localState.ReadSeq)
	remoteState, err := nodes[1].db.ForHashSlot(remoteHashSlot).GetCMDConversationState(ctx, remoteUID, "new____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(3), remoteState.ReadSeq)

	listed, err := nodes[0].store.ListCMDConversationActive(ctx, remoteUID, 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.CMDConversationState{
		{UID: remoteUID, ChannelID: "new____cmd", ChannelType: 2, ReadSeq: 3, ActiveAt: 300, UpdatedAt: 30},
		{UID: remoteUID, ChannelID: "old____cmd", ChannelType: 2, ReadSeq: 2, ActiveAt: 200, UpdatedAt: 20},
	}, listed)

	got, err := nodes[0].store.GetCMDConversationState(ctx, remoteUID, "new____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, listed[0], got)

	require.NoError(t, nodes[1].db.ForHashSlot(remoteHashSlot).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:         remoteUID,
		ChannelID:   "new____cmd",
		ChannelType: 2,
		ReadSeq:     11,
		ActiveAt:    400,
		UpdatedAt:   40,
	}))

	require.NoError(t, nodes[0].store.AdvanceCMDConversationReadSeq(ctx, []metadb.CMDConversationReadPatch{{
		UID: remoteUID, ChannelID: "new____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 90,
	}}))

	advanced, err := nodes[1].db.ForHashSlot(remoteHashSlot).GetCMDConversationState(ctx, remoteUID, "new____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(9), advanced.ReadSeq)
	require.Equal(t, int64(90), advanced.UpdatedAt)

	chatState, err := nodes[1].db.ForHashSlot(remoteHashSlot).GetUserConversationState(ctx, remoteUID, "new____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(11), chatState.ReadSeq)
	require.Equal(t, int64(40), chatState.UpdatedAt)
}
