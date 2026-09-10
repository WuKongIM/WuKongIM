package proxy

import (
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMembershipResponseVisibilityExtensionIsVersionedAndAligned(t *testing.T) {
	row := metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1}
	legacy := membershipRPCResponse{Membership: &row, Memberships: []metadb.UserChannelMembership{row, row}, Done: true, CMDMemberships: []metadb.UserCMDChannelMembership{}}
	old, err := encodeMembershipRPCResponse(legacy)
	require.NoError(t, err)
	require.True(t, runtimeMetaHasMagic(old, membershipRPCResponseMagic[:]))
	got, err := decodeMembershipRPCResponse(old)
	require.NoError(t, err)
	require.Equal(t, legacy, got)
	row.ConversationHiddenThroughSeq = 99
	legacy.Memberships[1].ConversationHiddenThroughSeq = 101
	encoded, err := encodeMembershipRPCResponse(legacy)
	require.NoError(t, err)
	require.True(t, runtimeMetaHasMagic(encoded, membershipRPCResponseVisibilityMagic[:]))
	require.False(t, runtimeMetaHasMagic(encoded, membershipRPCResponseMagic[:]), "old readers must reject the extension")
	got, err = decodeMembershipRPCResponse(encoded)
	require.NoError(t, err)
	require.Equal(t, legacy, got)
	_, err = decodeMembershipRPCResponse(encoded[:len(encoded)-1])
	require.Error(t, err)
	_, err = decodeMembershipRPCResponse(append(encoded, 0))
	require.Error(t, err)
}
