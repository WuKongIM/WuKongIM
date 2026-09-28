package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// removedMQTTSourceBinding drives the fixture through Removing and an
// acknowledged Removed transition, returning the final tombstone.
func removedMQTTSourceBinding(t *testing.T, db *MetaDB, r MQTTSourceBinding) MQTTSourceBinding {
	t.Helper()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, db, 0, r).Status)
	r.Revision, r.Stage, r.ReleaseReason, r.ProgressRevision = 2, MQTTBindingRemoving, MQTTBindingSessionEnded, 2
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, db, 1, r).Status)
	r.Revision, r.Stage, r.RecoveryAtMS, r.ProtectionRevision = 3, MQTTBindingRemoved, 0, 2
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, db, 2, r).Status)
	return r
}

func retireMQTTSourceBinding(t *testing.T, db *MetaDB, key MQTTSourceBindingKey, expected, closed uint64) MQTTSourceBindingResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	result, err := b.RetireMQTTSourceBinding(9, key, expected, closed)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	return *result
}

func TestMQTTSourceBindingRetirementFencesResurrectionAndKeepsReplayDiscovery(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	fresh := mqttSourceBindingFixture()
	r := removedMQTTSourceBinding(t, s.db, fresh)

	require.Equal(t, MQTTSessionCASApplied, retireMQTTSourceBinding(t, s.db, r.Key, 3, 1).Status)
	_, found, err := s.db.HashSlot(9).GetMQTTSourceBinding(context.Background(), r.Key)
	require.NoError(t, err)
	require.False(t, found, "retired tombstone row must be deleted")
	// Retrying the same retirement after a lost response is not a false success.
	require.Equal(t, MQTTSessionCASConflict, retireMQTTSourceBinding(t, s.db, r.Key, 3, 1).Status)

	// Delayed Preparing and Removed inserts of the retired lifetime are fenced.
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 0, fresh).Status)
	late := fresh
	late.Key.SubscriptionGeneration = 1
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 0, late).Status)
	late.Stage, late.ProgressRevision, late.ReleaseReason, late.RecoveryAtMS = MQTTBindingRemoved, 1, MQTTBindingSessionEnded, 0
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 0, late).Status)

	// A newer lifetime, even with a lower subscription generation, is unaffected.
	next := fresh
	next.Key.SessionGeneration, next.Key.SubscriptionGeneration = 2, 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, next).Status)

	// The fence never regresses.
	other := mqttSourceBindingFixture()
	other.Key.SessionGeneration = 5
	other = removedMQTTSourceBinding(t, s.db, other)
	require.Equal(t, MQTTSessionCASApplied, retireMQTTSourceBinding(t, s.db, other.Key, 3, 5).Status)
	old := fresh
	old.Key.SessionGeneration = 4
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSourceBinding(t, s.db, 0, old).Status)

	// Replay discovery survives the last primary row of an owner.
	gone := mqttSourceBindingFixture()
	gone.Key.Owner.Generation = "source-0"
	gone = removedMQTTSourceBinding(t, s.db, gone)
	require.Equal(t, MQTTSessionCASApplied, retireMQTTSourceBinding(t, s.db, gone.Key, 3, 1).Status)
	owners, _, done, err := s.db.HashSlot(9).ListMQTTReplaySources(context.Background(), MQTTBindingOwner{}, 64)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []MQTTBindingOwner{gone.Key.Owner, fresh.Key.Owner}, owners)
	one, after, done, err := s.db.HashSlot(9).ListMQTTReplaySources(context.Background(), MQTTBindingOwner{}, 1)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []MQTTBindingOwner{gone.Key.Owner}, one)
	rest, _, done, err := s.db.HashSlot(9).ListMQTTReplaySources(context.Background(), after, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []MQTTBindingOwner{fresh.Key.Owner}, rest)
}

func TestMQTTSourceBindingRetirementRejectsUnprovenRows(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttSourceBindingFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSourceBinding(t, s.db, 0, r).Status)
	// Not Removed.
	require.Equal(t, MQTTSessionCASConflict, retireMQTTSourceBinding(t, s.db, r.Key, 1, 1).Status)
	// Live Session lifetime (closedThrough below the row's generation).
	b := s.db.NewBatch()
	defer b.Close()
	_, err := b.RetireMQTTSourceBinding(9, r.Key, 1, 0)
	require.Error(t, err)
	// Stale expected revision.
	removed := mqttSourceBindingFixture()
	removed.Key.ClientID = "c2"
	removed = removedMQTTSourceBinding(t, s.db, removed)
	require.Equal(t, MQTTSessionCASConflict, retireMQTTSourceBinding(t, s.db, removed.Key, 2, 1).Status)
	_, found, err := s.db.HashSlot(9).GetMQTTSourceBinding(context.Background(), removed.Key)
	require.NoError(t, err)
	require.True(t, found)
}
