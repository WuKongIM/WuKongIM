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

func (s *groupSourceStore) RetireLiveMQTTSourceBinding(ctx context.Context, key meta.MQTTSourceBindingKey, expected, through uint64) (meta.MQTTSourceBindingResult, error) {
	b := s.db.NewBatch()
	defer b.Close()
	r, err := b.RetireLiveMQTTSourceBinding(7, key, expected, through)
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

func TestSourceRetirementFencesEndedSubscriptionsOfLiveSession(t *testing.T) {
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
	// Every subscription through this generation is Removed, so the live
	// Session's unsubscribe tombstone retires behind a subscription fence.
	done, err := retire.Reconcile(ctx, b.Key)
	require.NoError(t, err)
	require.True(t, done)
	r, err = f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: b.Key})
	require.NoError(t, err)
	require.Empty(t, r.Bindings)
	fresh := b
	fresh.Revision, fresh.Stage, fresh.RecoveryAtMS, fresh.ReleaseReason, fresh.ProtectionRevision = 1, meta.MQTTBindingPreparing, 1000, 0, 0
	fresh.BoundaryKnown, fresh.EndKnown, fresh.StartAfter, fresh.CompletedThrough, fresh.EndThrough, fresh.ProgressRevision = false, false, 0, 0, 0, 0
	res, err := f.store.CompareAndSwapMQTTSourceBinding(ctx, 0, fresh)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASConflict, res.Status, "late insert of a fenced subscription")
	// A later subscription generation of the same live Session is admitted.
	later := fresh
	later.Key.SubscriptionGeneration++
	res, err = f.store.CompareAndSwapMQTTSourceBinding(ctx, 0, later)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, res.Status)
	_, err = app.NewSourceRetirement(app.SourceRetirementOptions{})
	require.ErrorIs(t, err, app.ErrInvalid)
}
