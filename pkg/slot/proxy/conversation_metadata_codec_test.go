package proxy

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestConversationMetadataCodecKeepsAllRuntimeFences(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{ChannelID: "channel", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: 2, RetentionThroughSeq: 9, WriteFenceToken: "token", WriteFenceVersion: 11, WriteFenceReason: 1, WriteFenceUntilMS: 1234}
	original := permissionBatchRPCResponse{RuntimeIncluded: true, Status: rpcStatusOK, Results: []PermissionMetadataReadResult{{Found: true, Channel: metadb.Channel{ChannelID: "channel", ChannelType: 2}, Runtime: &meta}, {}}}
	body, err := encodePermissionBatchRPCResponse(original)
	require.NoError(t, err)
	decoded, err := decodePermissionBatchRPCResponse(body)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
	for i := 0; i < len(body); i++ {
		_, err = decodePermissionBatchRPCResponse(body[:i])
		require.Error(t, err)
	}
	_, err = decodePermissionBatchRPCResponse(append(body, 0))
	require.Error(t, err)
	original.RuntimeIncluded = false
	original.Results[0].Runtime = nil
	legacy, err := encodePermissionBatchRPCResponse(original)
	require.NoError(t, err)
	require.Equal(t, permissionBatchResponseMagic[:], legacy[:len(permissionBatchResponseMagic)])
	decoded, err = decodePermissionBatchRPCResponse(legacy)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}
func TestConversationMetadataRejectsLegacyResponse(t *testing.T) {
	cluster := &proxyTestMigrationCluster{localNodeID: 1, slotForKey: 7, leaders: map[multiraft.SlotID]multiraft.NodeID{7: 2}, peers: map[multiraft.SlotID][]multiraft.NodeID{7: {2}}}
	cluster.rpcService = func(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
		return encodePermissionBatchRPCResponse(permissionBatchRPCResponse{Status: rpcStatusOK, Results: []PermissionMetadataReadResult{{}}})
	}
	store := &Store{cluster: cluster, db: new(metadb.DB)}
	got := store.ReadPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: "channel", ChannelType: 2}})
	require.ErrorIs(t, got[0].Err, metadb.ErrCorruptValue)
}
