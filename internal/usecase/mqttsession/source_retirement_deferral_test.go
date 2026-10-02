package mqttsession_test

import (
	"context"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// An unprovable scheduled tombstone must be pushed back with a durable,
// doubling delay instead of being rescanned on every pass; an unscheduled
// (legacy) tombstone is left untouched.
func TestSourceRetirementDefersUnprovableTombstoneWithBackoff(t *testing.T) {
	ctx := context.Background()
	f, _, _, b := completedRemovalFixture(t, true)
	ghost := b
	ghost.Key.ClientID = "ghost"
	ghost.Revision, ghost.Stage, ghost.ReleaseReason, ghost.ProgressRevision = 1, meta.MQTTBindingRemoved, meta.MQTTBindingSessionEnded, 1
	ghost.BoundaryKnown, ghost.StartAfter, ghost.CompletedThrough, ghost.EndKnown, ghost.EndThrough, ghost.ProtectionRevision = false, 0, 0, false, 0, 0
	ghost.RecoveryAtMS, ghost.UpdatedAtMS = 1000, 1000
	res, err := f.store.CompareAndSwapMQTTSourceBinding(ctx, 0, ghost)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, res.Status)

	now := time.UnixMilli(5000)
	retire, err := app.NewSourceRetirement(app.SourceRetirementOptions{Store: f.store, Now: func() time.Time { return now }})
	require.NoError(t, err)
	read := func(k meta.MQTTSourceBindingKey) meta.MQTTSourceBinding {
		r, err := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: k})
		require.NoError(t, err)
		require.Len(t, r.Bindings, 1)
		return r.Bindings[0]
	}
	done, err := retire.Reconcile(ctx, ghost.Key)
	require.NoError(t, err)
	require.False(t, done)
	got := read(ghost.Key)
	require.Equal(t, uint64(2), got.Revision)
	require.Equal(t, int64(5000), got.UpdatedAtMS)
	require.Equal(t, int64(6000), got.RecoveryAtMS, "first deferral waits the minimum")

	now = time.UnixMilli(7000)
	done, err = retire.Reconcile(ctx, ghost.Key)
	require.NoError(t, err)
	require.False(t, done)
	got = read(ghost.Key)
	require.Equal(t, int64(9000), got.RecoveryAtMS, "delay doubles")

	// A clock behind the row never shortens the schedule.
	now = time.UnixMilli(6000)
	_, err = retire.Reconcile(ctx, ghost.Key)
	require.ErrorIs(t, err, app.ErrClock)
	require.Equal(t, int64(9000), read(ghost.Key).RecoveryAtMS)

	legacy := ghost
	legacy.Key.ClientID, legacy.Revision, legacy.RecoveryAtMS = "legacy", 1, 0
	res, err = f.store.CompareAndSwapMQTTSourceBinding(ctx, 0, legacy)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, res.Status)
	now = time.UnixMilli(20000)
	done, err = retire.Reconcile(ctx, legacy.Key)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, uint64(1), read(legacy.Key).Revision)
}
