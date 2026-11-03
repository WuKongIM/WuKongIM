package proxy

import (
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

// Failure inventory: proof on the wrong fact, incomplete authority, and a
// directory-generation mismatch must not survive a trusted-node reply decoder.
func TestSendPermissionDirectoryFenceValidation(t *testing.T) {
	q := PermissionMetadataRead{Kind: PermissionMetadataReadChannel, ChannelID: "a@b", ChannelType: 1}
	fact := metadb.PermissionFact{Found: true, Channel: metadb.Channel{ChannelID: "a@b", ChannelType: 1, DirectoryProjectionState: metadb.DirectoryProjectionReady, DirectoryProjectionGeneration: 5}, PersonDirectoryFence: metadb.PersonDirectoryAppendFence{ChannelEpoch: 2, LeaderEpoch: 3, RouteGeneration: 4, DirectoryGeneration: 5}}
	require.True(t, validSendPermissionFact(q, fact))
	for _, change := range []func(*metadb.PermissionFact){func(f *metadb.PermissionFact) { f.PersonDirectoryFence.LeaderEpoch = 0 }, func(f *metadb.PermissionFact) { f.PersonDirectoryFence.DirectoryGeneration++ }, func(f *metadb.PermissionFact) { f.Channel.DirectoryProjectionState = metadb.DirectoryProjectionPending }} {
		bad := fact
		change(&bad)
		require.False(t, validSendPermissionFact(q, bad))
	}
	bad := metadb.PermissionFact{UserPolicy: metadb.SendBanResult{Status: "ok"}, PersonDirectoryFence: fact.PersonDirectoryFence}
	require.False(t, validSendPermissionFact(PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy}, bad))
	// A legacy empty proof can carry policy but cannot grant directory admission.
	fact.PersonDirectoryFence = metadb.PersonDirectoryAppendFence{}
	require.True(t, validSendPermissionFact(q, fact))
}
