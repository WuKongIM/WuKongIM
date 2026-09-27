package meta

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/stretchr/testify/require"
)

func createRuntimeIncarnation(t *testing.T, db *MetaDB, slot HashSlot, candidate ChannelRuntimeMeta) ChannelRuntimeMeta {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, err := b.CreateChannelRuntimeMeta(slot, candidate)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	require.True(t, r.Created)
	got, found, err := db.HashSlot(slot).GetChannelRuntimeMeta(context.Background(), candidate.ChannelID, candidate.ChannelType)
	require.NoError(t, err)
	require.True(t, found)
	return got
}

func TestRuntimeIncarnationDeleteRequiresCreateAboveRetiredAuthority(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	for _, typ := range []int64{1, 2} {
		old := testRuntimeMeta("incarnation", typ)
		old.ChannelEpoch, old.LeaderEpoch, old.RouteGeneration, old.WriteFenceVersion = 11, 13, 19, 23
		if typ == 1 {
			old.DirectoryGeneration = 29
		}
		old = createRuntimeIncarnation(t, s.db, 7, old)
		require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, old.ChannelID, typ))
		_, err := s.db.HashSlot(7).UpsertChannelRuntimeMeta(ctx, old)
		require.ErrorIs(t, err, dberrors.ErrConflict, "late upsert cannot resurrect a retired identity")
		late := s.db.NewBatch()
		_, err = late.UpsertChannelRuntimeMeta(7, old)
		require.NoError(t, err)
		require.ErrorIs(t, late.Commit(ctx), dberrors.ErrConflict)
		require.NoError(t, late.Close())
		fresh := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta(old.ChannelID, typ))
		floor := maxUint64(old.ChannelEpoch, old.LeaderEpoch, old.RouteGeneration, old.DirectoryGeneration, old.WriteFenceVersion)
		for _, v := range []uint64{fresh.ChannelEpoch, fresh.LeaderEpoch, fresh.RouteGeneration, fresh.WriteFenceVersion} {
			require.Greater(t, v, floor)
		}
		if typ == 1 {
			require.Greater(t, fresh.DirectoryGeneration, floor)
		}
		result, err := s.db.HashSlot(7).UpsertChannelRuntimeMeta(ctx, old)
		require.NoError(t, err)
		require.Equal(t, MonotonicIgnoredStale, result)
		require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, old.ChannelID, typ))
		again := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta(old.ChannelID, typ))
		require.Greater(t, again.ChannelEpoch, fresh.ChannelEpoch)
	}
	// Floors remain exact channel/type/hash-Slot identities.
	for _, q := range []struct {
		slot HashSlot
		id   string
		typ  int64
	}{{8, "incarnation", 1}, {7, "other", 1}, {7, "incarnation", 6}} {
		candidate := testRuntimeMeta(q.id, q.typ)
		require.Equal(t, normalizeChannelRuntimeMeta(candidate), createRuntimeIncarnation(t, s.db, q.slot, candidate))
	}
}

func TestRuntimeIncarnationBatchOverlayAndRollback(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	original := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta("batch", 2))
	for _, abort := range []bool{true, false} {
		b := &WriteBatch{db: &DB{meta: s.db}, batch: s.db.NewBatch()}
		require.NoError(t, b.DeleteChannelRuntimeMeta(7, "batch", 2))
		require.NoError(t, b.DeleteChannelRuntimeMeta(7, "batch", 2))
		first, err := b.CreateChannelRuntimeMeta(7, testRuntimeMeta("batch", 2))
		require.NoError(t, err)
		require.NoError(t, b.DeleteChannelRuntimeMeta(7, "batch", 2))
		second, err := b.CreateChannelRuntimeMeta(7, testRuntimeMeta("batch", 2))
		require.NoError(t, err)
		loser, err := b.CreateChannelRuntimeMeta(7, testRuntimeMeta("batch", 2))
		require.NoError(t, err)
		if abort {
			b.batch.addOp(7, func(context.Context, *batchCommitState, *engine.Batch) error { return dberrors.ErrConflict })
		}
		err = b.Commit()
		if abort {
			require.ErrorIs(t, err, dberrors.ErrConflict)
		} else {
			require.NoError(t, err)
			require.True(t, first.Created)
			require.True(t, second.Created)
			require.False(t, loser.Created)
		}
		require.NoError(t, b.Close())
		got, found, err := s.db.HashSlot(7).GetChannelRuntimeMeta(ctx, "batch", 2)
		require.NoError(t, err)
		require.True(t, found)
		if abort {
			require.Equal(t, original, got)
		} else {
			require.Equal(t, original.RouteGeneration+2, got.RouteGeneration)
		}
	}
}

func TestRuntimeIncarnationSnapshotsAndReopenRetainDeletedFloor(t *testing.T) {
	for _, backup := range []bool{false, true} {
		s := openTestMetaStore(t)
		ctx := context.Background()
		old := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta("snap", 1))
		require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, "snap", 1))
		var reader io.ReadCloser
		var err error
		if backup {
			reader, err = s.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
		} else {
			reader, err = s.db.OpenHashSlotSnapshot(ctx, []uint16{7})
		}
		require.NoError(t, err)
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		s.close(t)
		path := t.TempDir()
		db, err := Open(path)
		require.NoError(t, err)
		require.NoError(t, db.meta.ImportHashSlotSnapshot(ctx, SlotSnapshot{HashSlots: []uint16{7}, Data: body}))
		require.NoError(t, db.Close())
		db, err = Open(path)
		require.NoError(t, err)
		fresh := createRuntimeIncarnation(t, db.meta, 7, testRuntimeMeta("snap", 1))
		require.Greater(t, fresh.ChannelEpoch, old.ChannelEpoch)
		require.Greater(t, fresh.RouteGeneration, old.RouteGeneration)
		require.Greater(t, fresh.DirectoryGeneration, old.DirectoryGeneration)
		require.NoError(t, db.Close())
	}
}

func TestRuntimeIncarnationCorruptFloorNeverLooksAbsent(t *testing.T) {
	for _, mode := range []string{"zero", "version", "short", "key swap", "checksum"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			key := runtimeRetirementKey(7, "corrupt", 2)
			version := byte(1)
			payload := binary.BigEndian.AppendUint64(nil, 17)
			wrapKey := key
			switch mode {
			case "zero":
				payload = make([]byte, 8)
			case "version":
				version = 2
			case "short":
				payload = payload[:7]
			case "key swap":
				wrapKey = runtimeRetirementKey(7, "other", 2)
			}
			value := rowcodec.Wrap(wrapKey, version, rowcodec.CodecFixed, rowcodec.FlagChecksum, payload)
			if mode == "checksum" {
				value[len(value)-1] ^= 1
			}
			physical := s.engine.NewBatch()
			require.NoError(t, physical.Set(key, value))
			require.NoError(t, physical.Commit(true))
			require.NoError(t, physical.Close())
			b := s.db.NewBatch()
			defer b.Close()
			_, err := b.CreateChannelRuntimeMeta(7, testRuntimeMeta("corrupt", 2))
			require.NoError(t, err)
			require.Error(t, b.Commit(context.Background()))
			_, found, err := s.db.HashSlot(7).GetChannelRuntimeMeta(context.Background(), "corrupt", 2)
			require.NoError(t, err)
			require.False(t, found)
		})
	}
}

func TestRuntimeIncarnationMaxFloorCannotWrap(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	old := testRuntimeMeta("exhausted", 2)
	old.RouteGeneration = math.MaxUint64
	createRuntimeIncarnation(t, s.db, 7, old)
	require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(context.Background(), old.ChannelID, old.ChannelType))
	b := s.db.NewBatch()
	defer b.Close()
	_, err := b.CreateChannelRuntimeMeta(7, testRuntimeMeta("exhausted", 2))
	require.NoError(t, err)
	require.ErrorIs(t, b.Commit(context.Background()), dberrors.ErrConflict)
}

func TestRuntimeIncarnationRetiresPersonProjectionWithItsRuntime(t *testing.T) {
	for _, ready := range []bool{false, true} {
		s := openTestMetaStore(t)
		ctx := context.Background()
		m := testRuntimeMeta("alice@bob", 1)
		b := s.db.NewBatch()
		_, err := b.CreateChannelRuntimeMeta(7, m)
		require.NoError(t, err)
		require.NoError(t, b.EnsurePersonDirectoryTask(7, PersonDirectoryTask{ChannelID: m.ChannelID, ChannelType: 1}))
		require.NoError(t, b.Commit(ctx))
		require.NoError(t, b.Close())
		old := PersonDirectoryTaskLocation{HashSlot: 7, ChannelID: m.ChannelID, ChannelType: 1, Generation: 1}
		if ready {
			b = s.db.NewBatch()
			require.NoError(t, b.CompletePersonDirectoryTask(7, old))
			require.NoError(t, b.Commit(ctx))
			require.NoError(t, b.Close())
		}
		_, _, err = s.db.HashSlot(7).GetChannel(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, m.ChannelID, 1))
		channel, found, err := s.db.HashSlot(7).GetChannel(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, DirectoryProjectionPending, channel.DirectoryProjectionState)
		_, found, err = s.db.HashSlot(7).GetPersonDirectoryTask(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.False(t, found)
		b = s.db.NewBatch()
		require.NoError(t, b.CompletePersonDirectoryTask(7, old))
		require.Error(t, b.Commit(ctx))
		require.NoError(t, b.Close())
		b = s.db.NewBatch()
		_, err = b.CreateChannelRuntimeMeta(7, m)
		require.NoError(t, err)
		require.NoError(t, b.EnsurePersonDirectoryTask(7, PersonDirectoryTask{ChannelID: m.ChannelID, ChannelType: 1}))
		require.NoError(t, b.Commit(ctx))
		require.NoError(t, b.Close())
		task, found, err := s.db.HashSlot(7).GetPersonDirectoryTask(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Greater(t, task.Generation, old.Generation)
		b = s.db.NewBatch()
		require.NoError(t, b.CompletePersonDirectoryTask(7, old))
		require.ErrorIs(t, b.Commit(ctx), dberrors.ErrConflict)
		require.NoError(t, b.Close())
		next := old
		next.Generation = task.Generation
		b = s.db.NewBatch()
		require.NoError(t, b.CompletePersonDirectoryTask(7, next))
		require.NoError(t, b.Commit(ctx))
		require.NoError(t, b.Close())
		s.close(t)
	}
}

func TestRuntimeIncarnationRejectsDirectoryAdmissionWhileRetired(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	m := createRuntimeIncarnation(t, s.db, 7, testRuntimeMeta("retired-person", 1))
	require.NoError(t, s.db.HashSlot(7).DeleteChannelRuntimeMeta(ctx, m.ChannelID, 1))
	b := s.db.NewBatch()
	defer b.Close()
	require.NoError(t, b.EnsurePersonDirectoryTask(7, PersonDirectoryTask{
		ChannelID: m.ChannelID, ChannelType: 1, Generation: m.DirectoryGeneration,
	}))
	require.ErrorIs(t, b.Commit(ctx), dberrors.ErrConflict)
	_, found, err := s.db.HashSlot(7).GetPersonDirectoryTask(ctx, m.ChannelID, 1)
	require.NoError(t, err)
	require.False(t, found)
}
