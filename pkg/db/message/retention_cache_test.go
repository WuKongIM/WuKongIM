package message

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestRepeatedRetentionReadAvoidsStorageAllocations(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	ctx := context.Background()
	_, present, err := log.LoadRetentionState(ctx)
	require.NoError(t, err)
	require.False(t, present)
	allocs := testing.AllocsPerRun(100, func() {
		state, present, err := log.LoadRetentionState(ctx)
		if err != nil || present || state != (RetentionState{}) {
			t.Fatalf("retention = %+v, present=%v, err=%v", state, present, err)
		}
	})
	require.Zero(t, allocs, "unchanged retention reads should reuse the bounded channel state")
}

func TestRetentionReadFollowsMutationsAndLeaseReclamation(t *testing.T) {
	eng := openCompatEngine(t)
	key, id := channel.ChannelKey("retention-cache"), channel.ChannelID{ID: "retention-cache", Type: 1}
	s := mustForChannel(t, eng, key, id)
	ctx := context.Background()
	_, err := s.Append([]channel.Record{
		compatOperationalRecord(t, id, 9001, "one", "sender", 1),
		compatOperationalRecord(t, id, 9002, "two", "sender", 2),
		compatOperationalRecord(t, id, 9003, "three", "sender", 3),
	})
	require.NoError(t, err)
	check := func(want RetentionState, present bool) {
		t.Helper()
		for range 2 { // Exercise both durable misses and retained hits.
			got, ok, err := s.log.LoadRetentionState(ctx)
			require.NoError(t, err)
			require.Equal(t, present, ok)
			require.Equal(t, want, got)
		}
	}
	check(RetentionState{}, false)
	want := RetentionState{LocalRetentionThroughSeq: 1, RetainedMaxSeq: 3}
	require.NoError(t, s.log.StoreRetentionState(ctx, want))
	check(want, true)
	require.NoError(t, s.Close())
	s = mustForChannel(t, eng, key, id)
	defer s.Close()
	check(want, true)
	require.NoError(t, s.AdoptRetentionBoundary(ctx, 2, "dispatch"))
	want.LocalRetentionThroughSeq = 2
	check(want, true)
	_, err = s.TrimMessagesThroughLimit(ctx, 2, RetentionTrimOptions{MaxMessages: 1})
	require.NoError(t, err)
	want.PhysicalRetentionThroughSeq = 1
	check(want, true)
	require.NoError(t, s.TruncateLogAndHistory(ctx, 2))
	want.RetainedMaxSeq = 2
	check(want, true)
	require.NoError(t, s.DiscardForRestore(ctx))
	check(RetentionState{}, false)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = s.log.LoadRetentionState(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, eng.engine.Close())
	_, _, err = s.log.LoadRetentionState(ctx)
	require.ErrorIs(t, err, dberrors.ErrClosed)
}

func TestRetentionReadImportInvalidatesActiveAndRetiredLeases(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, retired := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/retired=%v", stream, retired), func(t *testing.T) {
				ctx := context.Background()
				source := openTestMessageStore(t)
				defer source.close(t)
				log := testChannelLog(source)
				want := RetentionState{LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 2, RetainedMaxSeq: 2}
				require.NoError(t, log.StoreRetentionState(ctx, want))
				cut := Checkpoint{Epoch: 1, HW: 2}
				require.NoError(t, log.StoreCheckpoint(ctx, cut))
				r, err := source.db.OpenBackupSnapshot(ctx, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: log.key, ID: log.id, Checkpoint: cut}}})
				require.NoError(t, err)
				body, err := io.ReadAll(r)
				require.NoError(t, err)
				require.NoError(t, r.Close())
				target := openTestMessageStore(t)
				defer target.close(t)
				live := testChannelLog(target)
				_, ok, err := live.LoadRetentionState(ctx)
				require.NoError(t, err)
				require.False(t, ok)
				if retired {
					require.NoError(t, live.Close())
				}
				if stream {
					_, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
				} else {
					_, err = target.db.ImportBackupSnapshot(ctx, body)
				}
				require.NoError(t, err)
				if retired {
					live = testChannelLog(target)
				}
				defer live.Close()
				got, ok, err := live.LoadRetentionState(ctx)
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, want, got)
			})
		}
	}
}

func TestConcurrentRetentionReadsObserveAcknowledgedWrites(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	defer log.Close()
	ctx := context.Background()
	var readers sync.WaitGroup
	for i := uint64(1); i <= 20; i++ {
		want := RetentionState{LocalRetentionThroughSeq: i, RetainedMaxSeq: i}
		require.NoError(t, log.StoreRetentionState(ctx, want))
		for range 4 {
			readers.Go(func() {
				got, present, err := log.LoadRetentionState(ctx)
				if err != nil || !present || got.LocalRetentionThroughSeq < i {
					t.Errorf("acknowledged %d, got %+v present=%v err=%v", i, got, present, err)
				}
			})
		}
	}
	readers.Wait()
}
