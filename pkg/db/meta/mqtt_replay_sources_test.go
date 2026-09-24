package meta

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTReplaySourcesKeepTombstonesWithoutConsumerAuthority(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	owners := []MQTTBindingOwner{{Kind: MQTTBindingChannel, ID: "2:z", Generation: "a"}, {Kind: MQTTBindingChannel, ID: "2:z", Generation: "bb"}, {Kind: MQTTBindingChannel, ID: "2:aa", Generation: "a"}}
	b := s.db.NewBatch()
	defer b.Close()
	for i, owner := range owners {
		for j := 0; j < 128; j++ {
			r := mqttSourceBindingFixture()
			r.Key.Owner, r.Key.ClientID = owner, fmt.Sprintf("client-%03d", j)
			if i == 1 || j == 0 {
				r.Stage, r.ProgressRevision, r.ReleaseReason, r.RecoveryAtMS = MQTTBindingRemoved, 1, MQTTBindingSessionEnded, 0
			}
			_, err := b.CompareAndSwapMQTTSourceBinding(9, 0, r)
			require.NoError(t, err)
		}
	}
	uid := mqttSourceBindingFixture()
	uid.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
	_, err := b.CompareAndSwapMQTTSourceBinding(9, 0, uid)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	q := MQTTRead{Kind: MQTTReadReplaySources, Limit: 1}
	require.True(t, q.Recovery())
	require.Equal(t, MQTTReadKind(17), q.Kind)
	for i, owner := range owners {
		ctx := &mqttSourceOwnerBudgetContext{Context: context.Background()}
		page, e := s.db.ReadMQTTState(ctx, 9, q)
		require.NoError(t, e, "one source must not scan its 128 bindings")
		require.Equal(t, []MQTTBindingOwner{owner}, page.SourceOwners)
		require.Equal(t, owner, page.After.SourceOwner)
		require.Equal(t, i == len(owners)-1, page.Done)
		q.After = page.After
	}
	empty, err := s.db.ReadMQTTState(context.Background(), 9, q)
	require.NoError(t, err)
	require.Empty(t, empty.SourceOwners)
	require.Equal(t, q.After, empty.After)
	require.True(t, empty.Done)
	active, err := s.db.ReadMQTTState(context.Background(), 9, MQTTRead{Kind: MQTTReadSourceOwners, Limit: 64})
	require.NoError(t, err)
	require.Equal(t, []MQTTBindingOwner{owners[0], owners[2]}, active.SourceOwners)
	retention, err := s.db.ReadMQTTState(context.Background(), 9, MQTTRead{Kind: MQTTReadSourceRetention, Owner: owners[1], Limit: 1})
	require.NoError(t, err)
	require.Empty(t, retention.Bindings, "a cleanup hint must not restore a discharged consumer")
	require.True(t, retention.Done)
}

func TestMQTTReplaySourcesPinnedRowsAndFailureBounds(t *testing.T) {
	for _, mode := range []string{"removed", "corrupt_value", "corrupt_key", "canceled", "invalid_limit", "foreign_fields"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			r := mqttSourceBindingFixture()
			writeMQTTSourceBinding(t, s.db, 0, r)
			snapshot, err := s.db.engine.NewSnapshot()
			require.NoError(t, err)
			defer snapshot.Close()
			view := &Shard{db: s.db, hashSlot: 9, readSnapshot: snapshot}
			key, err := mqttSourceBindingTable.primaryRowKey(9, mqttSourceBindingPrimaryKey(r.Key))
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := MQTTRead{Kind: MQTTReadReplaySources, Limit: 1}
			switch mode {
			case "removed":
				r.Revision, r.Stage, r.ReleaseReason, r.ProgressRevision = 2, MQTTBindingRemoving, MQTTBindingSessionEnded, 2
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, r).Status)
				r.Revision, r.Stage, r.RecoveryAtMS, r.ProtectionRevision = 3, MQTTBindingRemoved, 0, 2
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, r).Status)
			case "corrupt_value", "corrupt_key":
				raw := s.db.engine.NewBatch()
				defer raw.Close()
				if mode == "corrupt_key" {
					key, err = encodeKeyParts(encodeRowPrefix(9, TableIDMQTTSourceBinding), mqttBindingOwnerParts(r.Key.Owner))
					require.NoError(t, err)
				}
				require.NoError(t, raw.Set(key, []byte("bad")))
				require.NoError(t, raw.Commit(true))
			case "canceled":
				cancel()
			case "invalid_limit":
				q.Limit = 65
			case "foreign_fields":
				q.Owner = r.Key.Owner
			}
			old, _, done, err := view.ListMQTTReplaySources(context.Background(), MQTTBindingOwner{}, 1)
			require.NoError(t, err)
			require.Equal(t, []MQTTBindingOwner{r.Key.Owner}, old)
			require.True(t, done)
			got, err := s.db.ReadMQTTState(ctx, 9, q)
			if mode == "removed" {
				require.NoError(t, err)
				require.Equal(t, []MQTTBindingOwner{r.Key.Owner}, got.SourceOwners)
				require.True(t, got.Done)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
		})
	}
}
