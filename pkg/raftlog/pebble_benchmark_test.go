package raftlog

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	benchEntriesSink []raftpb.Entry
	benchStateSink   multiraft.BootstrapState
)

func BenchmarkPebbleSaveEntries(b *testing.B) {
	cfg := loadPebbleBenchConfig(b)

	b.Run("single-group-append-small", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(1)
		nextIndex := uint64(1)
		const batchSize = 8

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mustSave(b, store, benchPersistentState(nextIndex, batchSize, 1, cfg.payload/2))
			nextIndex += batchSize
		}
		b.StopTimer()

		wantLast := nextIndex - 1
		if got := mustLastIndex(b, store); got != wantLast {
			b.Fatalf("LastIndex() = %d, want %d", got, wantLast)
		}

		got := mustEntries(b, store, wantLast-batchSize+1, wantLast+1, 0)
		if len(got) != batchSize {
			b.Fatalf("len(Entries()) = %d, want %d", len(got), batchSize)
		}
	})

	b.Run("single-group-tail-replace-small", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(2)

		const (
			initialCount = 128
			replaceFrom  = 65
			replaceCount = 64
		)
		mustSave(b, store, benchPersistentState(1, initialCount, 1, cfg.payload))
		wantLast := uint64(replaceFrom + replaceCount - 1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			term := uint64(i + 2)
			mustSave(b, store, benchPersistentState(replaceFrom, replaceCount, term, cfg.payload))
		}
		b.StopTimer()

		if got := mustLastIndex(b, store); got != wantLast {
			b.Fatalf("LastIndex() = %d, want %d", got, wantLast)
		}
		if got := mustTerm(b, store, replaceFrom); got != uint64(b.N+1) {
			b.Fatalf("Term(%d) = %d, want %d", replaceFrom, got, uint64(b.N+1))
		}
	})

	b.Run("multi-group-interleaved", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})

		groupCount := cfg.groups
		if groupCount > 32 {
			groupCount = 32
		}
		stores := make([]multiraft.Storage, groupCount)
		nextIndex := make([]uint64, groupCount)
		for i := range stores {
			stores[i] = db.ForSlot(uint64(i + 1))
			nextIndex[i] = 1
		}

		const batchSize = 4
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			group := i % groupCount
			start := nextIndex[group]
			mustSave(b, stores[group], benchPersistentState(start, batchSize, 1, cfg.payload/2))
			nextIndex[group] += batchSize
		}
		b.StopTimer()

		for i, store := range stores {
			wantLast := nextIndex[i] - 1
			if got := mustLastIndex(b, store); got != wantLast {
				b.Fatalf("group %d LastIndex() = %d, want %d", i+1, got, wantLast)
			}
		}
	})
}

func BenchmarkPebbleEntries(b *testing.B) {
	cfg := loadPebbleBenchConfig(b)

	b.Run("windowed-read", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(1)
		total := cfg.entries * 4
		mustSave(b, store, benchPersistentState(1, total, 1, cfg.payload))

		lo := uint64(total / 2)
		hi := lo + 16

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchEntriesSink = mustEntries(b, store, lo, hi, 0)
		}
		b.StopTimer()

		if len(benchEntriesSink) != int(hi-lo) {
			b.Fatalf("len(Entries()) = %d, want %d", len(benchEntriesSink), hi-lo)
		}
	})

	b.Run("large-scan", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(2)
		total := cfg.entries * 8
		mustSave(b, store, benchPersistentState(1, total, 1, cfg.payload/2))

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchEntriesSink = mustEntries(b, store, 1, uint64(total+1), 0)
		}
		b.StopTimer()

		if len(benchEntriesSink) != total {
			b.Fatalf("len(Entries()) = %d, want %d", len(benchEntriesSink), total)
		}
	})

	b.Run("max-size-capped", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(3)
		state := benchPersistentState(1, 3, 1, cfg.payload)
		mustSave(b, store, state)

		limit := uint64(state.Entries[0].Size() + state.Entries[1].Size() - 1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchEntriesSink = mustEntries(b, store, 1, 4, limit)
		}
		b.StopTimer()

		if len(benchEntriesSink) != 1 {
			b.Fatalf("len(Entries()) = %d, want 1", len(benchEntriesSink))
		}
	})

	b.Run("reopen-read", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(4)
		total := cfg.entries * 2
		mustSave(b, store, benchPersistentState(1, total, 1, cfg.payload/2))
		db = reopenPebbleDB(b, db, path)
		store = db.ForSlot(4)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			closeBenchDB(b, db, path)
			db = nil
			db = mustOpenPebbleDB(b, path)
			store = db.ForSlot(4)
			benchEntriesSink = mustEntries(b, store, 1, uint64(total+1), 0)
		}
		b.StopTimer()
		closeBenchDB(b, db, path)
		db = nil

		if len(benchEntriesSink) != total {
			b.Fatalf("len(Entries()) = %d, want %d", len(benchEntriesSink), total)
		}
	})
}

func BenchmarkPebbleMarkApplied(b *testing.B) {
	cfg := loadPebbleBenchConfig(b)

	b.Run("single-group", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mustMarkApplied(b, store, uint64(i+1))
		}
		b.StopTimer()

		db = reopenPebbleDB(b, db, path)
		state := mustInitialState(b, db.ForSlot(1))
		if state.AppliedIndex != uint64(b.N) {
			b.Fatalf("AppliedIndex = %d, want %d", state.AppliedIndex, uint64(b.N))
		}
	})

	b.Run("concurrent-multi-group", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})

		groupCount := cfg.groups
		if groupCount > 32 {
			groupCount = 32
		}
		stores := make([]multiraft.Storage, groupCount)
		progress := make([]atomic.Uint64, groupCount)
		for i := range stores {
			stores[i] = db.ForSlot(uint64(i + 1))
		}

		var (
			next   uint64
			failed atomic.Bool
			wg     sync.WaitGroup
		)
		errCh := make(chan error, 1)
		workerCount := groupCount
		if workerCount > 12 {
			workerCount = 12
		}

		b.ReportAllocs()
		b.ResetTimer()
		for worker := 0; worker < workerCount; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				for {
					if failed.Load() {
						return
					}
					op := int(atomic.AddUint64(&next, 1)) - 1
					if op >= b.N {
						return
					}

					group := op % groupCount
					index := progress[group].Add(1)
					if err := stores[group].MarkApplied(context.Background(), index); err != nil {
						if failed.CompareAndSwap(false, true) {
							errCh <- err
						}
						return
					}
				}
			}()
		}
		wg.Wait()
		b.StopTimer()

		select {
		case err := <-errCh:
			b.Fatal(err)
		default:
		}

		db = reopenPebbleDB(b, db, path)
		for i := range stores {
			state := mustInitialState(b, db.ForSlot(uint64(i+1)))
			if state.AppliedIndex != progress[i].Load() {
				b.Fatalf("group %d AppliedIndex = %d, want %d", i+1, state.AppliedIndex, progress[i].Load())
			}
		}
	})
}

func BenchmarkPebbleInitialStateAndReopen(b *testing.B) {
	cfg := loadPebbleBenchConfig(b)

	b.Run("single-group", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		store := db.ForSlot(1)
		mustSave(b, store, benchPersistentState(1, cfg.entries, 3, cfg.payload/2))
		mustMarkApplied(b, store, uint64(cfg.entries))
		db = reopenPebbleDB(b, db, path)
		closeBenchDB(b, db, path)
		db = nil

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			db = mustOpenPebbleDB(b, path)
			benchStateSink = mustInitialState(b, db.ForSlot(1))
			closeBenchDB(b, db, path)
			db = nil
		}
		b.StopTimer()

		if benchStateSink.AppliedIndex != uint64(cfg.entries) {
			b.Fatalf("AppliedIndex = %d, want %d", benchStateSink.AppliedIndex, uint64(cfg.entries))
		}
		if benchStateSink.HardState.Commit != uint64(cfg.entries) {
			b.Fatalf("Commit = %d, want %d", benchStateSink.HardState.Commit, uint64(cfg.entries))
		}
	})

	b.Run("multi-group-snapshot", func(b *testing.B) {
		db, path := openBenchDB(b)
		b.Cleanup(func() {
			closeBenchDB(b, db, path)
		})
		groupCount := cfg.groups
		if groupCount > 16 {
			groupCount = 16
		}

		for i := 0; i < groupCount; i++ {
			groupID := uint64(i + 1)
			store := db.ForSlot(groupID)
			snap := raftpb.Snapshot{
				Data: []byte{byte(groupID)},
				Metadata: raftpb.SnapshotMetadata{
					Index: 10,
					Term:  2,
					ConfState: raftpb.ConfState{
						Voters: []uint64{1, 2, 3},
					},
				},
			}
			hs := raftpb.HardState{Term: 2, Commit: 10}
			mustSave(b, store, multiraft.PersistentState{
				HardState: &hs,
				Snapshot:  &snap,
				Entries:   benchEntries(11, 4, 3, cfg.payload/4),
			})
			mustMarkApplied(b, store, 10)
		}
		db = reopenPebbleDB(b, db, path)
		closeBenchDB(b, db, path)
		db = nil

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			db = mustOpenPebbleDB(b, path)
			for groupID := 1; groupID <= groupCount; groupID++ {
				benchStateSink = mustInitialState(b, db.ForSlot(uint64(groupID)))
			}
			closeBenchDB(b, db, path)
			db = nil
		}
		b.StopTimer()

		if len(benchStateSink.ConfState.Voters) != 3 {
			b.Fatalf("len(ConfState.Voters) = %d, want 3", len(benchStateSink.ConfState.Voters))
		}
	})
}
