package mqttsession_test

import (
	"context"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func (s *groupSourceStore) RetireMQTTSourceBinding(ctx context.Context, key meta.MQTTSourceBindingKey, expected, closed uint64) (meta.MQTTSourceBindingResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.RetireMQTTSourceBinding(7, key, expected, closed)
	if err != nil {
		return meta.MQTTSourceBindingResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTSourceBindingResult{}, err
	}
	return *r, nil
}

func TestSourceRetirementDeletesEndedTombstoneAndFencesResurrection(t *testing.T) {
	ctx := context.Background()
	f, _, removal, b := completedRemovalFixture(t, false)
	retire, err := app.NewSourceRetirement(app.SourceRetirementOptions{Store: f.store})
	require.NoError(t, err)
	// Removing (unacknowledged) is not retirable.
	done, err := retire.Reconcile(ctx, b.Key)
	require.NoError(t, err)
	require.False(t, done)
	for i := 0; i < 2; i++ {
		_, err = removal.Reconcile(ctx, b.Key)
		require.NoError(t, err)
	}
	done, err = retire.Reconcile(ctx, b.Key)
	require.NoError(t, err)
	require.True(t, done)
	r, err := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: b.Key})
	require.NoError(t, err)
	require.Empty(t, r.Bindings)
	// Absent row is a no-op, and the retired lifetime cannot be re-prepared.
	done, err = retire.Reconcile(ctx, b.Key)
	require.NoError(t, err)
	require.False(t, done)
	fresh := b
	fresh.Revision, fresh.Stage, fresh.RecoveryAtMS, fresh.ReleaseReason, fresh.ProtectionRevision = 1, meta.MQTTBindingPreparing, 1000, 0, 0
	fresh.BoundaryKnown, fresh.EndKnown, fresh.StartAfter, fresh.CompletedThrough, fresh.EndThrough, fresh.ProgressRevision = false, false, 0, 0, 0, 0
	res, err := f.store.CompareAndSwapMQTTSourceBinding(ctx, 0, fresh)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASConflict, res.Status)
}

func TestSourceRetirementKeepsLiveLifetimeTombstones(t *testing.T) {
	ctx := context.Background()
	f, _, removal, b := completedRemovalFixture(t, true)
	for i := 0; i < 2; i++ {
		_, err := removal.Reconcile(ctx, b.Key)
		require.NoError(t, err)
	}
	r, err := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: b.Key})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTBindingRemoved, r.Bindings[0].Stage)
	retire, err := app.NewSourceRetirement(app.SourceRetirementOptions{Store: f.store})
	require.NoError(t, err)
	done, err := retire.Reconcile(ctx, b.Key)
	require.NoError(t, err)
	require.False(t, done, "unsubscribe tombstone of a live Session must stay")
	_, err = app.NewSourceRetirement(app.SourceRetirementOptions{})
	require.ErrorIs(t, err, app.ErrInvalid)
}
