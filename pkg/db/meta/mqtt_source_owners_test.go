package meta

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

type mqttSourceOwnerBudgetContext struct {
	context.Context
	checks int
}

func (c *mqttSourceOwnerBudgetContext) Err() error {
	c.checks++
	if c.checks > 32 {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestMQTTSourceOwnersDistinctBoundedAndOrdered(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	owners := []MQTTBindingOwner{{Kind: MQTTBindingChannel, ID: "2:z", Generation: "a"}, {Kind: MQTTBindingChannel, ID: "2:z", Generation: "bb"}, {Kind: MQTTBindingChannel, ID: "2:aa", Generation: "a"}}
	b := s.db.NewBatch()
	defer b.Close()
	for i, o := range owners {
		for j := 0; j < 128; j++ {
			r := mqttSourceBindingFixture()
			r.Key.Owner = o
			r.Key.ClientID = fmt.Sprintf("client-%d", j)
			_, err := b.CompareAndSwapMQTTSourceBinding(9, 0, r)
			require.NoError(t, err)
			if i == 1 {
				r.Revision = 2
				r.IntentRevision++
				r.Stage = MQTTBindingRemoving
				_, err = b.CompareAndSwapMQTTSourceBinding(9, 1, r)
				require.NoError(t, err)
			}
		}
	}
	// Removed sources and UID discovery do not create replay jobs.
	for _, removed := range []bool{false, true} {
		r := mqttSourceBindingFixture()
		r.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}
		if removed {
			r.Key.Owner = MQTTBindingOwner{Kind: MQTTBindingChannel, ID: "2:removed", Generation: "g"}
			r.Stage = MQTTBindingRemoved
			r.ProgressRevision = 1
			r.ReleaseReason = MQTTBindingSessionEnded
			r.RecoveryAtMS = 0
		}
		_, err := b.CompareAndSwapMQTTSourceBinding(9, 0, r)
		require.NoError(t, err)
	}
	require.NoError(t, b.Commit(context.Background()))
	var cursor MQTTBindingOwner
	for i, want := range owners {
		ctx := &mqttSourceOwnerBudgetContext{Context: context.Background()}
		page, next, done, err := s.db.HashSlot(9).ListMQTTSourceOwners(ctx, cursor, 1)
		require.NoError(t, err, "discovery must skip the entire subscriber prefix")
		require.Equal(t, []MQTTBindingOwner{want}, page)
		require.Equal(t, want, next)
		require.Equal(t, i == len(owners)-1, done)
		cursor = next
	}
	page, next, done, err := s.db.HashSlot(9).ListMQTTSourceOwners(context.Background(), cursor, 1)
	require.NoError(t, err)
	require.Empty(t, page)
	require.Equal(t, cursor, next)
	require.True(t, done)
	require.Negative(t, CompareMQTTBindingOwners(owners[0], owners[1]))
	require.Negative(t, CompareMQTTBindingOwners(owners[1], owners[2]))
	query := MQTTRead{Kind: MQTTReadSourceOwners, Limit: 2}
	result, err := s.db.ReadMQTTState(context.Background(), 9, query)
	require.NoError(t, err)
	require.Equal(t, owners[:2], result.SourceOwners)
	require.Equal(t, owners[1], result.After.SourceOwner)
	require.False(t, result.Done)
}

func TestMQTTSourceOwnersPinnedWitnessAndErrors(t *testing.T) {
	for _, mode := range []string{"removed", "missing", "corrupt", "bad_index"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			r := mqttSourceBindingFixture()
			writeMQTTSourceBinding(t, s.db, 0, r)
			snap, err := s.db.engine.NewSnapshot()
			require.NoError(t, err)
			defer snap.Close()
			view := &Shard{db: s.db, hashSlot: 9, readSnapshot: snap}
			key, err := mqttSourceBindingTable.primaryRowKey(9, mqttSourceBindingPrimaryKey(r.Key))
			require.NoError(t, err)
			raw := s.db.engine.NewBatch()
			defer raw.Close()
			switch mode {
			case "removed":
				r.Revision = 2
				r.IntentRevision++
				r.Stage = MQTTBindingRemoving
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, r).Status)
				r.Revision = 3
				r.Stage = MQTTBindingRemoved
				r.ProgressRevision = 2
				r.ReleaseReason = MQTTBindingSessionEnded
				r.RecoveryAtMS = 0
				r.ProtectionRevision = 2
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, r).Status)
			case "missing":
				require.NoError(t, raw.Delete(key))
				require.NoError(t, raw.Commit(true))
			case "corrupt":
				require.NoError(t, raw.Set(key, []byte("bad")))
				require.NoError(t, raw.Commit(true))
			case "bad_index":
				prefix, err := encodeTableIndexScanPrefix(9, TableIDMQTTSourceBinding, 4, mqttBindingOwnerParts(r.Key.Owner))
				require.NoError(t, err)
				require.NoError(t, raw.Set(append(prefix, 0), nil))
				require.NoError(t, raw.Commit(true))
			}
			old, _, done, err := view.ListMQTTSourceOwners(context.Background(), MQTTBindingOwner{}, 1)
			require.NoError(t, err)
			require.Equal(t, []MQTTBindingOwner{r.Key.Owner}, old)
			require.True(t, done)
			page, _, done, err := s.db.HashSlot(9).ListMQTTSourceOwners(context.Background(), MQTTBindingOwner{}, 1)
			if mode == "removed" {
				require.NoError(t, err)
				require.Empty(t, page)
				require.True(t, done)
			} else {
				require.Error(t, err)
				require.Empty(t, page)
				require.False(t, done)
			}
		})
	}
	s := openTestMetaStore(t)
	defer s.close(t)
	for _, q := range []MQTTRead{
		{Kind: MQTTReadSourceOwners, Limit: 0}, {Kind: MQTTReadSourceOwners, Limit: 65},
		{Kind: MQTTReadSourceOwners, Limit: 1, Owner: MQTTBindingOwner{Kind: MQTTBindingChannel, ID: "2:g", Generation: "g"}},
		{Kind: MQTTReadSourceOwners, Limit: 1, After: MQTTReadCursor{SourceOwner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}}},
		{Kind: MQTTReadSourceOwners, Limit: 1, After: MQTTReadCursor{SourceOwner: MQTTBindingOwner{Kind: MQTTBindingChannel, ID: "2:g"}}},
	} {
		_, err := s.db.ReadMQTTState(context.Background(), 9, q)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := s.db.HashSlot(9).ListMQTTSourceOwners(ctx, MQTTBindingOwner{}, 1)
	require.ErrorIs(t, err, context.Canceled)
}
