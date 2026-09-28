package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Removed tombstones scheduled for retirement stay discoverable through the
// recovery index; only RecoveryAtMS may be deferred; retirement drops the entry.
func TestMQTTRemovedBindingStaysDiscoverableUntilRetired(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	r := mqttSourceBindingFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, r).Status)
	r.Revision, r.Stage, r.ReleaseReason, r.ProgressRevision = 2, MQTTBindingRemoving, MQTTBindingSessionEnded, 2
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 1, r).Status)
	r.Revision, r.Stage, r.RecoveryAtMS, r.ProtectionRevision, r.UpdatedAtMS = 3, MQTTBindingRemoved, 5000, 2, 5000
	require.NoError(t, ValidateMQTTSourceBinding(r))
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 2, r).Status)

	list := func() []MQTTSourceBinding {
		rows, _, done, err := s.db.HashSlot(9).ListMQTTSourceBindingRecovery(ctx, MQTTSourceBindingRecoveryCursor{}, 16)
		require.NoError(t, err)
		require.True(t, done)
		return rows
	}
	require.Equal(t, []MQTTSourceBinding{r}, list())

	// Deferral changes only Revision, UpdatedAtMS and a later RecoveryAtMS.
	later := r
	later.Revision, later.UpdatedAtMS, later.RecoveryAtMS = 4, 6000, 9000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 3, later).Status)
	require.Equal(t, []MQTTSourceBinding{later}, list())
	for _, bad := range []func(*MQTTSourceBinding){
		func(b *MQTTSourceBinding) { b.RecoveryAtMS = 8000 },         // earlier
		func(b *MQTTSourceBinding) { b.UpdatedAtMS = 5000 },          // clock regression
		func(b *MQTTSourceBinding) { b.CompletedThrough++ },          // other field
		func(b *MQTTSourceBinding) { b.Stage = MQTTBindingRemoving }, // stage regression
	} {
		next := later
		next.Revision, next.UpdatedAtMS, next.RecoveryAtMS = 5, 7000, 10000
		bad(&next)
		b := s.db.NewBatch()
		res, err := b.CompareAndSwapMQTTSourceBinding(9, 4, next)
		if err == nil {
			require.NoError(t, b.Commit(ctx))
			require.Equal(t, MQTTSessionCASConflict, res.Status)
		}
		b.Close()
	}
	require.Equal(t, []MQTTSourceBinding{later}, list())

	// A legacy unscheduled tombstone (RecoveryAtMS 0) stays outside the index.
	legacy := mqttSourceBindingFixture()
	legacy.Key.ClientID = "legacy"
	legacy = removedMQTTSourceBinding(t, s.db, legacy)
	require.Zero(t, legacy.RecoveryAtMS)
	require.Len(t, list(), 1)

	require.Equal(t, MQTTSessionCASApplied, retireMQTTSourceBinding(t, s.db, later.Key, 4, 1).Status)
	require.Empty(t, list())
}
