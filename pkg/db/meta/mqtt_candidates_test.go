package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTSourceCandidatesNeverSkipInconsistentWitnesses(t *testing.T) {
	for _, mode := range []string{"missing", "removed", "lookahead"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			row := mqttSourceBindingFixture()
			row.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
			require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, row).Status)
			bad := row
			if mode == "lookahead" {
				bad.Key.ClientID = "client2"
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, bad).Status)
			}
			snapshot, err := s.engine.NewSnapshot()
			require.NoError(t, err)
			defer snapshot.Close()
			primary, err := mqttSourceBindingTable.primaryRowKey(9, mqttSourceBindingPrimaryKey(bad.Key))
			require.NoError(t, err)
			b := s.engine.NewBatch()
			defer b.Close()
			if mode == "removed" {
				bad.Stage, bad.ReleaseReason, bad.ProgressRevision, bad.RecoveryAtMS = MQTTBindingRemoved, MQTTBindingSessionEnded, 10, 0
				value, e := encodeMQTTSourceBindingRow(primary, bad)
				require.NoError(t, e)
				require.NoError(t, b.Set(primary, value))
			} else {
				require.NoError(t, b.Delete(primary))
			}
			require.NoError(t, b.Commit(true))
			q := MQTTRead{Kind: MQTTReadSourceCandidates, Owner: row.Key.Owner, Limit: 1}
			old, err := (&Shard{db: s.db, hashSlot: 9, readSnapshot: snapshot}).readMQTTState(context.Background(), q)
			require.NoError(t, err)
			require.Equal(t, []MQTTSourceBinding{row}, old.Bindings)
			r, err := s.db.ReadMQTTState(context.Background(), 9, q)
			require.Error(t, err, "a missing or obsolete candidate must not disappear into successful admission")
			require.Zero(t, r)
		})
	}
}
