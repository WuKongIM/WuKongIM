package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

func mqttInboxAdmissionFixture() MQTTInboxAdmission {
	return MQTTInboxAdmission{ChannelID: channelid.EncodePersonChannel("alice", "bob"), DirectoryGeneration: 1, Revision: 1, UpdatedAtMS: 1000}
}

func writeMQTTInboxAdmission(t *testing.T, db *MetaDB, expected uint64, row MQTTInboxAdmission) MQTTInboxAdmissionResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, err := b.CompareAndSwapMQTTInboxAdmission(9, expected, row)
	require.NoError(t, err)
	require.Equal(t, MQTTInboxAdmissionResult{}, *r)
	require.NoError(t, b.Commit(context.Background()))
	return *r
}

func readMQTTInboxAdmission(t *testing.T, db *MetaDB, id string) *MQTTInboxAdmissionView {
	t.Helper()
	r, err := db.ReadMQTTState(context.Background(), 9, MQTTRead{Kind: MQTTReadInboxAdmission, AdmissionChannel: id})
	require.NoError(t, err)
	require.True(t, r.Done)
	require.Equal(t, MQTTReadCursor{}, r.After)
	require.NotNil(t, r.Admission)
	return r.Admission
}

func TestMQTTInboxAdmissionResumesBothParticipantsAndFencesIncarnations(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	row := mqttInboxAdmissionFixture()
	require.Equal(t, MQTTSessionCASConflict, writeMQTTInboxAdmission(t, s.db, 0, row).Status)
	require.Nil(t, readMQTTInboxAdmission(t, s.db, row.ChannelID).Checkpoint)
	b := s.db.NewBatch()
	defer b.Close()
	_, err := b.CreateChannelRuntimeMeta(9, testRuntimeMeta(row.ChannelID, 1))
	require.NoError(t, err)
	created, err := b.CompareAndSwapMQTTInboxAdmission(9, 0, row)
	require.NoError(t, err)
	require.NoError(t, b.Commit(ctx))
	require.Equal(t, MQTTSessionCASApplied, created.Status)
	require.Equal(t, row, *readMQTTInboxAdmission(t, s.db, row.ChannelID).Checkpoint)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTInboxAdmission(t, s.db, 0, row).Status)
	left, right, err := channelid.DecodePersonChannel(row.ChannelID)
	require.NoError(t, err)
	cursor := MQTTSourceBindingKey{Owner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: left}, Namespace: "main", ClientID: "z", SessionGeneration: 1, SubscriptionGeneration: 1}
	row.Revision, row.After = 2, cursor
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 1, row).Status)
	row.Revision, row.After.ClientID = 3, "aa" // Length precedes bytes.
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 2, row).Status)
	for _, mutate := range []func(*MQTTInboxAdmission){
		func(r *MQTTInboxAdmission) { r.After.ClientID = "z" },
		func(r *MQTTInboxAdmission) { r.DirectoryGeneration = 2; r.After = MQTTSourceBindingKey{} },
		func(r *MQTTInboxAdmission) { r.UpdatedAtMS-- },
		func(r *MQTTInboxAdmission) { r.Participant = 2; r.After = MQTTSourceBindingKey{} },
	} {
		bad := row
		bad.Revision++
		mutate(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTInboxAdmission(t, s.db, 3, bad).Status)
	}
	row.Revision, row.Participant, row.After = 4, 1, MQTTSourceBindingKey{}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 3, row).Status)
	cursor.Owner.ID = right
	row.Revision, row.After = 5, cursor
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 4, row).Status)
	row.Revision, row.Participant, row.After = 6, 2, MQTTSourceBindingKey{}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 5, row).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTInboxAdmission(t, s.db, 5, row).Status)
	bad := row
	bad.Revision++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTInboxAdmission(t, s.db, 6, bad).Status)
	// Business deletion keeps runtime metadata but advances its directory generation.
	require.NoError(t, s.db.HashSlot(9).UpsertChannel(ctx, Channel{ChannelID: row.ChannelID, ChannelType: 1}))
	require.NoError(t, s.db.HashSlot(9).DeleteChannel(ctx, row.ChannelID, 1))
	view := readMQTTInboxAdmission(t, s.db, row.ChannelID)
	require.Equal(t, uint64(2), view.Runtime.DirectoryGeneration)
	require.Equal(t, row, *view.Checkpoint)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTInboxAdmission(t, s.db, 5, row).Status, "old exact retry must not confirm another incarnation")
	row.Revision, row.DirectoryGeneration, row.Participant = 7, 2, 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 6, row).Status)
	// Physical runtime deletion retains a revision witness even after recreation at generation 1.
	require.NoError(t, s.db.HashSlot(9).DeleteChannelRuntimeMeta(ctx, row.ChannelID, 1))
	view = readMQTTInboxAdmission(t, s.db, row.ChannelID)
	require.Nil(t, view.Runtime)
	require.Equal(t, uint64(0), view.Checkpoint.DirectoryGeneration)
	require.Equal(t, uint64(8), view.Checkpoint.Revision)
	_, err = s.db.HashSlot(9).UpsertChannelRuntimeMeta(ctx, testRuntimeMeta(row.ChannelID, 1))
	require.NoError(t, err)
	row = mqttInboxAdmissionFixture()
	require.Equal(t, MQTTSessionCASConflict, writeMQTTInboxAdmission(t, s.db, 0, row).Status)
	row.Revision = 9
	require.Equal(t, MQTTSessionCASApplied, writeMQTTInboxAdmission(t, s.db, 8, row).Status)
}
