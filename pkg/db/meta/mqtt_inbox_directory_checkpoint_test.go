package meta

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Directory candidates include unrelated types and tombstones. Every legal
// native ID must fit durable progress before projection may skip that candidate.
func TestMQTTInboxDirectoryLongCheckpointSurvivesRestore(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	qualification := mqttSourceBindingFixture()
	qualification.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
	qualification.AuthorizationVersion = 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, 0, qualification).Status)
	keys := []ChannelKey{{strings.Repeat("a", 1025), 2}, {strings.Repeat("界", 1365) + "z", 255}}
	for _, k := range keys {
		require.NoError(t, st.db.HashSlot(9).UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "alice", ChannelID: k.ChannelID, ChannelType: k.ChannelType, Tombstone: true, TombstoneAt: 1}))
	}
	query := MQTTRead{Kind: MQTTReadInboxDirectory, Owner: qualification.Key.Owner, Limit: 1}
	for i, k := range keys {
		page, err := st.db.ReadMQTTState(ctx, 9, query)
		require.NoError(t, err)
		require.Equal(t, []ChannelKey{k}, page.Directory)
		require.Equal(t, i == len(keys)-1, page.Done)
		expected := qualification.Revision
		qualification.Revision++
		qualification.DiscoveryAfterChannelID, qualification.DiscoveryAfterChannelType = k.ChannelID, uint8(k.ChannelType)
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, st.db, expected, qualification).Status)
		query.After = page.After
	}
	reader, err := st.db.OpenBackupHashSlotSnapshot(ctx, []uint16{9})
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	_, err = VerifyBackupHashSlotSnapshotReader(ctx, []uint16{9}, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	restored := openTestMetaStore(t)
	defer restored.close(t)
	require.NoError(t, restored.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{9}, bytes.NewReader(payload), int64(len(payload)), false))
	got, found, err := restored.db.HashSlot(9).GetMQTTSourceBinding(ctx, qualification.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, qualification, got)
	query.After.Directory = ChannelKey{got.DiscoveryAfterChannelID, int64(got.DiscoveryAfterChannelType)}
	page, err := restored.db.ReadMQTTState(ctx, 9, query)
	require.NoError(t, err)
	require.Empty(t, page.Directory)
	require.True(t, page.Done)
	require.Equal(t, query.After, page.After)
	regressed := got
	regressed.Revision++
	regressed.DiscoveryAfterChannelID = keys[0].ChannelID
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, restored.db, got.Revision, regressed).Status)
	for _, invalid := range []string{strings.Repeat("a", 4097), strings.Repeat("界", 1366), "bad\x00id"} {
		bad := got
		bad.DiscoveryAfterChannelID = invalid
		require.Error(t, ValidateMQTTSourceBinding(bad))
	}
	got.Revision++
	got.DiscoveryDone, got.Stage = true, MQTTBindingActive
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, restored.db, qualification.Revision, got).Status)
}
