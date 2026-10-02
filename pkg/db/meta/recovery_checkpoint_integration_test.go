//go:build integration

package meta

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryCheckpointAtomicAndOfflineInvalidation(t *testing.T) {
	ctx := context.Background()
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "reopen", true: "offline_write"}[offline], func(t *testing.T) {
			path := t.TempDir()
			db, err := Open(path)
			require.NoError(t, err)
			require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
			b := db.MetaDB().NewBatch()
			require.NoError(t, b.UpsertUser(0, User{UID: "checkpoint-user", Token: "first"}))
			require.NoError(t, b.SetSlotRecoveryCheckpoint(1, 7, []byte("proof-at-seven")))
			require.NoError(t, b.Commit(ctx))
			require.NoError(t, b.Close())
			require.NoError(t, db.Close())
			if offline {
				// This open deliberately does not enable the new protocol, just like
				// an older program or an offline importer retaining the watermark.
				db, err = Open(path)
				require.NoError(t, err)
				b = db.MetaDB().NewBatch()
				require.NoError(t, b.UpsertUser(0, User{UID: "checkpoint-user", Token: "offline"}))
				require.NoError(t, b.Commit(ctx))
				require.NoError(t, b.Close())
				require.NoError(t, db.Close())
			}
			db, err = Open(path)
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
			index, proof, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, !offline, ok)
			if ok {
				require.EqualValues(t, 7, index)
				require.Equal(t, []byte("proof-at-seven"), proof)
			}
		})
	}
}

func TestRecoveryCheckpointRejectsUncertifiedMutation(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	db, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	b := db.MetaDB().NewBatch()
	require.NoError(t, b.SetSlotRecoveryCheckpoint(1, 7, []byte("proof")))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	b = db.MetaDB().NewBatch()
	require.NoError(t, b.UpsertUser(0, User{UID: "untracked", Token: "value"}))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRecoveryCheckpointDoesNotReviveStaleLiveProof(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	db, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	epoch := db.MetaDB().RecoveryEpoch()
	ordinary := db.MetaDB().NewBatch()
	require.NoError(t, ordinary.UpsertUser(0, User{UID: "untracked", Token: "value"}))
	require.NoError(t, ordinary.Commit(ctx))
	require.NoError(t, ordinary.Close())
	certified := db.NewWriteBatch()
	require.NoError(t, certified.SetSlotRecoveryCheckpointAt(1, 9, []byte("stale live chain"), epoch))
	require.NoError(t, certified.Commit())
	require.NoError(t, certified.Close())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	index, err := db.MetaDB().SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 9, index, "business progress remains durable")
	_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
	require.NoError(t, err)
	require.False(t, ok, "the next normal commit revived an invalidated certificate")
}

// Failure matrix: a fenced Slot restore must remove its own proof without
// invalidating a disjoint Slot; interruption must retain that isolation. A stale
// live proof must stay rejected without poisoning a newly anchored neighbor.
func TestRecoveryCheckpointStartupRestoreIsolatesSlots(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "interrupted"}[interrupt], func(t *testing.T) {
			ctx := context.Background()
			source := openTestMetaStore(t)
			defer source.close(t)
			require.NoError(t, source.db.HashSlot(5).UpsertUser(ctx, User{UID: "restored"}))
			snap, err := source.db.ExportHashSlotSnapshot(ctx, []uint16{5})
			require.NoError(t, err)
			path := t.TempDir()
			db, err := Open(path)
			require.NoError(t, err)
			require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
			b := db.MetaDB().NewBatch()
			require.NoError(t, b.SetSlotRecoveryCheckpoint(1, 7, []byte("target-old-proof")))
			require.NoError(t, b.SetSlotRecoveryCheckpoint(2, 8, []byte("neighbor-proof")))
			require.NoError(t, b.Commit(ctx))
			require.NoError(t, b.Close())
			epoch := db.MetaDB().RecoveryEpoch()
			restoreCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			err = db.MetaDB().RestoreStartupSnapshot(restoreCtx, 1, 9, []uint16{5}, bytes.NewReader(snap.Data), int64(len(snap.Data)), func(p SnapshotRestoreProgress) {
				if interrupt && p.Stage == "snapshot_install" {
					cancel()
				}
			})
			if interrupt {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, epoch, db.MetaDB().RecoveryEpoch(), "known Slot restore invalidated every live anchor")
			require.NoError(t, db.Close())
			db, err = Open(path)
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
			_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
			require.NoError(t, err)
			require.False(t, ok, "target's old proof survived replacement")
			index, proof, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 2)
			require.NoError(t, err)
			require.True(t, ok, "unrelated Slot lost its checkpoint")
			require.EqualValues(t, 8, index)
			require.Equal(t, []byte("neighbor-proof"), proof)
		})
	}
}

func TestRecoveryCheckpointStaleSlotDoesNotInvalidateFreshSlot(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	db, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	staleEpoch := db.MetaDB().RecoveryEpoch()
	unknown := db.MetaDB().NewBatch()
	require.NoError(t, unknown.UpsertUser(0, User{UID: "unknown"}))
	require.NoError(t, unknown.Commit(ctx))
	require.NoError(t, unknown.Close())
	freshEpoch := db.MetaDB().RecoveryEpoch()
	// One physical batch contains a fresh and a stale Slot chain.
	b := db.MetaDB().NewBatch()
	require.NoError(t, b.SetSlotRecoveryCheckpointAt(2, 8, []byte("fresh"), freshEpoch))
	require.NoError(t, b.SetSlotRecoveryCheckpointAt(1, 7, []byte("stale"), staleEpoch))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.Equal(t, freshEpoch, db.MetaDB().RecoveryEpoch())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
	require.NoError(t, err)
	require.False(t, ok)
	_, proof, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("fresh"), proof)
}

func TestRecoveryCheckpointSnapshotlessSlotPreservesNeighbor(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	db, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	b := db.MetaDB().NewBatch()
	require.NoError(t, b.SetSlotRecoveryCheckpoint(2, 8, []byte("neighbor")))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	epoch := db.MetaDB().RecoveryEpoch()
	b = db.MetaDB().NewBatch()
	require.NoError(t, b.UpsertUser(5, User{UID: "snapshotless"}))
	require.NoError(t, b.ClearSlotRecoveryCheckpoint(1, 9))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.Equal(t, epoch, db.MetaDB().RecoveryEpoch())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
	require.NoError(t, err)
	require.False(t, ok)
	index, err := db.MetaDB().SlotAppliedIndex(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 9, index)
	_, proof, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("neighbor"), proof)
}

// An unaware writer can affect a Slot that is not opened in this process.
// Resealing writes from another Slot must not revive that dormant certificate
// on the following restart.
func TestRecoveryCheckpointUnawareWriterPermanentlyInvalidatesDormantSlots(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	db, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	b := db.MetaDB().NewBatch()
	require.NoError(t, b.SetSlotRecoveryCheckpoint(2, 8, []byte("dormant-old-proof")))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
	db, err = Open(path) // Deliberately unaware older writer.
	require.NoError(t, err)
	b = db.MetaDB().NewBatch()
	require.NoError(t, b.UpsertUser(5, User{UID: "offline"}))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	b = db.MetaDB().NewBatch()
	require.NoError(t, b.SetSlotRecoveryCheckpoint(1, 9, []byte("new-live-slot-proof")))
	require.NoError(t, b.Commit(ctx))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.MetaDB().EnableRecoveryCheckpoints())
	_, _, ok, err := db.MetaDB().SlotRecoveryCheckpoint(ctx, 2)
	require.NoError(t, err)
	require.False(t, ok, "resealing another Slot revived a dormant certificate")
	_, _, ok, err = db.MetaDB().SlotRecoveryCheckpoint(ctx, 1)
	require.NoError(t, err)
	require.True(t, ok)
}
