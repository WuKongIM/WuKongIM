package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestStoreReadsUIDMembershipDirectoryFromAuthoritativeRemoteSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	ctx := context.Background()
	uid := findUIDForSlot(t, nodes[1].cluster, 2, "membership-remote")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, uid)
	ordinary := metadb.UserChannelMembership{
		UID: uid, ChannelID: "g1", ChannelType: 2,
		JoinSeq: 4, ReadSeq: 5, DeletedToSeq: 6, ActivatedAt: 7,
		SourceVersion: 8, UpdatedAt: 9,
	}
	command := metadb.UserCMDChannelMembership{
		UID: uid, CommandChannelID: "g1@cmd", ChannelType: 2,
		StartSeq: 10, AckSeq: 11, UpdatedAt: 12,
	}
	shard := nodes[1].db.MetaDB().HashSlot(metadb.HashSlot(hashSlot))
	require.NoError(t, shard.UpsertUserChannelMembership(ctx, ordinary))
	require.NoError(t, shard.UpsertUserCMDChannelMembership(ctx, command))

	page, cursor, done, err := nodes[0].store.ListUserChannelMembershipPage(
		ctx, uid, metadb.UserChannelMembershipCursor{}, 10,
	)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.UserChannelMembership{ordinary}, page)
	require.Equal(t, metadb.UserChannelMembershipCursor{
		ActivatedAt: ordinary.ActivatedAt, ChannelID: ordinary.ChannelID, ChannelType: ordinary.ChannelType,
	}, cursor)

	point, ok, err := nodes[0].store.GetUserChannelMembership(ctx, uid, ordinary.ChannelID, ordinary.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, ordinary, point)

	cmdPage, cmdCursor, done, err := nodes[0].store.ListUserCMDChannelMembershipPage(
		ctx, uid, metadb.UserCMDChannelMembershipCursor{}, 10,
	)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.UserCMDChannelMembership{command}, cmdPage)
	require.Equal(t, metadb.UserCMDChannelMembershipCursor{
		CommandChannelID: command.CommandChannelID, ChannelType: command.ChannelType,
	}, cmdCursor)
}

func TestMembershipRPCRejectsUIDBoundToDifferentSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	uid := findUIDForSlot(t, nodes[0].cluster, 2, "membership-wrong-slot")
	payload, err := encodeMembershipRPCRequest(membershipRPCRequest{
		Op: membershipRPCListOrdinary, SlotID: 1, UID: uid, Limit: 10,
	})
	require.NoError(t, err)

	_, err = nodes[0].store.handleMembershipRPC(context.Background(), payload)
	require.ErrorContains(t, err, "uid slot mismatch")
}
