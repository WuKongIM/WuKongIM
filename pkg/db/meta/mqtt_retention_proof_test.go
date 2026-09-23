package meta

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

func TestMQTTRetentionRejectsSkippedWitnessesInPinnedView(t *testing.T) {
	for _, mode := range []string{"missing", "stale", "corrupt", "malformed", "removed"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			r := mqttSourceBindingFixture()
			require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, r).Status)
			snapshot, err := s.db.engine.NewSnapshot()
			require.NoError(t, err)
			defer snapshot.Close()
			view := &Shard{db: s.db, hashSlot: 9, readSnapshot: snapshot}
			table := mqttSourceBindingTable
			index, ok := table.indexByID(4)
			require.True(t, ok)
			oldKey, err := table.indexEntryKey(9, index, mqttSourceBindingRetentionParts(r.Key, 0), mqttSourceBindingPrimaryKey(r.Key))
			require.NoError(t, err)
			primary, err := table.primaryRowKey(9, mqttSourceBindingPrimaryKey(r.Key))
			require.NoError(t, err)
			batch := s.db.engine.NewBatch()
			defer batch.Close()
			switch mode {
			case "missing":
				require.NoError(t, batch.Delete(primary))
			case "corrupt":
				require.NoError(t, batch.Set(primary, []byte("corrupt")))
			case "stale":
				changed := r
				changed.Revision, changed.BoundaryKnown, changed.StartAfter, changed.CompletedThrough, changed.ProtectionRevision = 2, true, 5, 5, 1
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, changed).Status)
				require.NoError(t, batch.Set(oldKey, nil))
			case "malformed":
				prefix, e := encodeTableIndexScanPrefix(9, table.spec.ID, 4, mqttBindingOwnerParts(r.Key.Owner))
				require.NoError(t, e)
				require.NoError(t, batch.Set(append(prefix, 0), nil))
			case "removed":
				changed := r
				changed.Revision, changed.Stage, changed.ProgressRevision, changed.ReleaseReason = 2, MQTTBindingRemoving, 3, MQTTBindingSessionEnded
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, changed).Status)
				changed.Revision, changed.Stage, changed.ProtectionRevision, changed.RecoveryAtMS = 3, MQTTBindingRemoved, 2, 0
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, changed).Status)
			}
			require.NoError(t, batch.Commit(true))
			old, after, done, err := view.ListMQTTSourceBindingRetention(context.Background(), r.Key.Owner, MQTTSourceBindingRetentionCursor{}, 1)
			require.NoError(t, err)
			require.Equal(t, []MQTTSourceBinding{r}, old)
			require.True(t, done)
			require.Zero(t, after)
			page, _, done, err := s.db.HashSlot(9).ListMQTTSourceBindingRetention(context.Background(), r.Key.Owner, MQTTSourceBindingRetentionCursor{}, 1)
			if mode == "removed" {
				require.NoError(t, err)
				require.Empty(t, page)
				require.True(t, done)
			} else {
				require.Error(t, err, "an inconsistent witness must never be skipped")
				require.Empty(t, page)
				require.False(t, done)
				_, err = s.db.ReadMQTTState(context.Background(), 9, MQTTRead{Kind: MQTTReadSourceRetention, Owner: r.Key.Owner, Limit: 1})
				require.Error(t, err)
			}
		})
	}
}

func TestMQTTRetentionFirstPageHasConstantWitnessBudget(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	b := s.db.NewBatch()
	defer b.Close()
	owner := mqttSourceBindingFixture().Key.Owner
	for i := 0; i < 256; i++ {
		r := mqttSourceBindingFixture()
		r.Key.ClientID = fmt.Sprintf("client-%03d", i)
		_, err := b.CompareAndSwapMQTTSourceBinding(9, 0, r)
		require.NoError(t, err)
	}
	require.NoError(t, b.Commit(context.Background()))
	var after MQTTSourceBindingRetentionCursor
	for i := 0; i < 3; i++ {
		ctx := &mqttSourceOwnerBudgetContext{Context: context.Background()}
		page, next, done, err := s.db.HashSlot(9).ListMQTTSourceBindingRetention(ctx, owner, after, 1)
		require.NoError(t, err)
		require.Len(t, page, 1)
		require.Equal(t, fmt.Sprintf("client-%03d", i), page[0].Key.ClientID)
		require.Equal(t, page[0].Key, next.Key)
		require.Zero(t, next.CompletedThrough)
		require.False(t, done)
		after = next
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := s.db.HashSlot(9).ListMQTTSourceBindingRetention(ctx, owner, after, 1)
	require.ErrorIs(t, err, context.Canceled)
	_, _, _, err = s.db.HashSlot(9).ListMQTTSourceBindingRetention(context.Background(), owner, after, 257)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
}
