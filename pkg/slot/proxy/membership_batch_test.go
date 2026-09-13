package proxy

import (
	"context"
	"errors"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMembershipBatchReadsOnlyExactKeysAtUIDAuthority(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	uid := findUIDForSlot(t, nodes[1].cluster, 2, "batch-member")
	shard := nodes[1].db.MetaDB().HashSlot(metadb.HashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid)))
	row := metadb.UserChannelMembership{UID: uid, ChannelID: "g1", ChannelType: 2, JoinSeq: 4, DeletedToSeq: 7, ConversationHiddenThroughSeq: 123, Tombstone: true}
	require.NoError(t, shard.UpsertUserChannelMembership(context.Background(), row))
	keys := []metadb.ChannelKey{{ChannelID: "absent", ChannelType: 2}, {ChannelID: "g1", ChannelType: 2}, {ChannelID: "g1", ChannelType: 2}}
	rows, err := nodes[0].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserChannelMembership{row}, rows)
	// The local authoritative path has the same sparse/deduplicated result.
	local, err := nodes[1].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.NoError(t, err)
	require.Equal(t, rows, local)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = nodes[0].store.GetUserChannelMemberships(ctx, uid, keys)
	require.ErrorIs(t, err, context.Canceled)
}

func TestMembershipBatchCodecBoundsAndIdentity(t *testing.T) {
	req := membershipRPCRequest{Op: membershipRPCGetBatch, UID: "u1", SlotID: 1, Keys: []metadb.ChannelKey{{ChannelID: "g1", ChannelType: 2}}}
	body, err := encodeMembershipRPCRequest(req)
	require.NoError(t, err)
	got, err := decodeMembershipRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	for i := 0; i < len(body); i++ {
		_, err := decodeMembershipRPCRequest(body[:i])
		require.Error(t, err)
	}
	_, err = decodeMembershipRPCRequest(append(body, 0))
	require.Error(t, err)
	req.Keys = make([]metadb.ChannelKey, MembershipReadBatchMaxKeys+1)
	_, err = encodeMembershipRPCRequest(req)
	require.Error(t, err)
	keys := []metadb.ChannelKey{{ChannelID: "g1", ChannelType: 2}}
	row := metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2}
	require.NoError(t, validateMembershipBatchRows("u1", keys, []metadb.UserChannelMembership{row}))
	require.Error(t, validateMembershipBatchRows("u2", keys, []metadb.UserChannelMembership{row}))
	require.Error(t, validateMembershipBatchRows("u1", keys, []metadb.UserChannelMembership{row, row}))
}

func TestMembershipBatchRPCFailureDoesNotBecomeMissingRows(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	uid := findUIDForSlot(t, nodes[1].cluster, 2, "batch-failure")
	keys := []metadb.ChannelKey{{ChannelID: "g1", ChannelType: 2}}
	target := nodes[1].cluster
	original := target.handlers[membershipRPCServiceID]
	calls := 0
	target.handlers[membershipRPCServiceID] = func(ctx context.Context, body []byte) ([]byte, error) { calls++; return original(ctx, body) }
	rows, err := nodes[0].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Equal(t, 1, calls)
	target.handlers[membershipRPCServiceID] = func(context.Context, []byte) ([]byte, error) { return nil, errors.New("disk failed") }
	rows, err = nodes[0].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.ErrorContains(t, err, "disk failed")
	require.Nil(t, rows)
	target.handlers[membershipRPCServiceID] = func(context.Context, []byte) ([]byte, error) {
		return encodeMembershipRPCResponse(membershipRPCResponse{Status: rpcStatusNotFound})
	}
	_, err = nodes[0].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.ErrorContains(t, err, "unexpected membership batch status")
	target.handlers[membershipRPCServiceID] = original
	delete(nodes[0].cluster.nodes, 2)
	_, err = nodes[0].store.GetUserChannelMemberships(context.Background(), uid, keys)
	require.Error(t, err)
	nodes[0].cluster.nodes[2] = target
}
