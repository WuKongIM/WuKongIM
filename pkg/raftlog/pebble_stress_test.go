package raftlog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestPebbleStressConcurrentWriters(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}

	db, path := openStressDB(t)
	defer func() {
		closeBenchDB(t, db, path)
	}()

	stores := stressStoresForGroups(db, cfg.groups)
	models := newStressGroupModels(cfg.groups, 1)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()
	if err := runPebbleStressLoad(ctx, stores, models, cfg, 0); err != nil {
		t.Fatalf("runPebbleStressLoad() error = %v", err)
	}

	db = reopenPebbleDB(t, db, path)
	stores = stressStoresForGroups(db, cfg.groups)
	for i := range models {
		verifyPebbleGroupState(t, stores[i], models[i].snapshot(), cfg.payload)
	}
}

func TestPebbleStressMixedReadWriteReopen(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}

	db, path := openStressDB(t)
	defer func() {
		closeBenchDB(t, db, path)
	}()

	stores := stressStoresForGroups(db, cfg.groups)
	models := newStressGroupModels(cfg.groups, 1)

	rounds := 3
	if cfg.duration < 3*time.Second {
		rounds = 1
	}
	roundDuration := cfg.duration / time.Duration(rounds)
	if roundDuration <= 0 {
		roundDuration = cfg.duration
	}
	readerCount := cfg.writers / 2
	if readerCount < 1 {
		readerCount = 1
	}

	for round := 0; round < rounds; round++ {
		ctx, cancel := context.WithTimeout(context.Background(), roundDuration)
		err := runPebbleStressLoad(ctx, stores, models, cfg, readerCount)
		cancel()
		if err != nil {
			t.Fatalf("round %d runPebbleStressLoad() error = %v", round, err)
		}

		db = reopenPebbleDB(t, db, path)
		stores = stressStoresForGroups(db, cfg.groups)
		for i := range models {
			verifyPebbleGroupState(t, stores[i], models[i].snapshot(), cfg.payload)
		}
	}
}

func TestPebbleStressSnapshotAndRecovery(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}

	db, path := openStressDB(t)
	defer func() {
		closeBenchDB(t, db, path)
	}()

	stores := stressStoresForGroups(db, cfg.groups)
	models := newStressGroupModels(cfg.groups, 3)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()
	if err := runPebbleSnapshotStress(ctx, stores, models, cfg); err != nil {
		t.Fatalf("runPebbleSnapshotStress() error = %v", err)
	}

	db = reopenPebbleDB(t, db, path)
	stores = stressStoresForGroups(db, cfg.groups)
	for i := range models {
		verifyPebbleGroupState(t, stores[i], models[i].snapshot(), cfg.payload)
	}
}

func TestPebbleStressAckedWritesSurvivePeriodicReopen(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}

	db, path := openStressDB(t)
	defer func() {
		closeBenchDB(t, db, path)
	}()

	stores := stressStoresForGroups(db, cfg.groups)
	models := newStressGroupModels(cfg.groups, 1)

	rounds := 3
	if cfg.duration < 3*time.Second {
		rounds = 1
	}
	roundDuration := cfg.duration / time.Duration(rounds)
	if roundDuration <= 0 {
		roundDuration = cfg.duration
	}

	for round := 0; round < rounds; round++ {
		ctx, cancel := context.WithTimeout(context.Background(), roundDuration)
		err := runPebbleStressLoad(ctx, stores, models, cfg, 0)
		cancel()
		if err != nil {
			t.Fatalf("round %d runPebbleStressLoad() error = %v", round, err)
		}

		db = reopenPebbleDB(t, db, path)
		stores = stressStoresForGroups(db, cfg.groups)
		for i := range models {
			verifyPebbleGroupState(t, stores[i], models[i].snapshot(), cfg.payload)
		}
	}
}

func stressStoresForGroups(db *DB, groupCount int) []multiraft.Storage {
	stores := make([]multiraft.Storage, groupCount)
	for i := range stores {
		stores[i] = db.ForSlot(uint64(i + 1))
	}
	return stores
}

func runPebbleStressLoad(ctx context.Context, stores []multiraft.Storage, models []stressGroupModel, cfg pebbleStressConfig, readerCount int) error {
	var (
		nextOp uint64
		failed atomic.Bool
		wg     sync.WaitGroup
	)
	errCh := make(chan error, 1)

	reportErr := func(err error) {
		if err == nil {
			return
		}
		if failed.CompareAndSwap(false, true) {
			errCh <- err
		}
	}

	for writer := 0; writer < cfg.writers; writer++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()

			for {
				if failed.Load() {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}

				op := int(atomic.AddUint64(&nextOp, 1)) - 1
				group := op % len(stores)
				if err := applyStressWrite(ctx, stores[group], &models[group], op, cfg.payload); err != nil {
					reportErr(fmt.Errorf("writer %d group %d: %w", writerID, group+1, err))
					return
				}
			}
		}(writer)
	}

	for reader := 0; reader < readerCount; reader++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			op := readerID
			for {
				if failed.Load() {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}

				group := op % len(stores)
				if err := runStressReadChecks(stores[group], cfg.payload); err != nil {
					reportErr(fmt.Errorf("reader %d group %d: %w", readerID, group+1, err))
					return
				}
				op += readerCount
			}
		}(reader)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func applyStressWrite(ctx context.Context, store multiraft.Storage, model *stressGroupModel, op int, payloadSize int) error {
	const (
		appendBatch  = 4
		rewriteBatch = 16
	)

	model.mu.Lock()
	defer model.mu.Unlock()

	if model.lastIndex >= rewriteBatch && op%5 == 0 {
		start := model.lastIndex - rewriteBatch + 1
		if err := store.Save(ctx, benchPersistentState(start, rewriteBatch, model.entryTerm, payloadSize)); err != nil {
			return err
		}
		if err := store.MarkApplied(ctx, model.lastIndex); err != nil {
			return err
		}
		model.appliedIndex = model.lastIndex
		return nil
	}

	start := model.lastIndex + 1
	state := benchPersistentState(start, appendBatch, model.entryTerm, payloadSize)
	if err := store.Save(ctx, state); err != nil {
		return err
	}
	last := state.Entries[len(state.Entries)-1].Index
	if err := store.MarkApplied(ctx, last); err != nil {
		return err
	}
	model.lastIndex = last
	model.appliedIndex = last
	return nil
}

func runStressReadChecks(store multiraft.Storage, payloadSize int) error {
	ctx := context.Background()

	first, err := store.FirstIndex(ctx)
	if err != nil {
		return fmt.Errorf("FirstIndex: %w", err)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		return fmt.Errorf("LastIndex: %w", err)
	}
	state, err := store.InitialState(ctx)
	if err != nil {
		return fmt.Errorf("InitialState: %w", err)
	}

	if state.AppliedIndex > last {
		return fmt.Errorf("AppliedIndex %d > LastIndex %d", state.AppliedIndex, last)
	}
	if last == 0 {
		if first != 1 {
			return fmt.Errorf("FirstIndex = %d, want 1 for empty store", first)
		}
		return nil
	}
	if first > last {
		return fmt.Errorf("FirstIndex %d > LastIndex %d", first, last)
	}

	lo := last
	if lo > 7 {
		lo -= 7
	}
	if lo < first {
		lo = first
	}
	entries, err := store.Entries(ctx, lo, last+1, 0)
	if err != nil {
		return fmt.Errorf("Entries(%d,%d): %w", lo, last+1, err)
	}
	if len(entries) != int(last-lo+1) {
		return fmt.Errorf("len(Entries()) = %d, want %d", len(entries), last-lo+1)
	}
	for i, entry := range entries {
		wantIndex := lo + uint64(i)
		if entry.Index != wantIndex {
			return fmt.Errorf("entries[%d].Index = %d, want %d", i, entry.Index, wantIndex)
		}
		if entry.Term != 1 {
			return fmt.Errorf("entries[%d].Term = %d, want 1", i, entry.Term)
		}
		if len(entry.Data) != payloadSize {
			return fmt.Errorf("entries[%d].Data len = %d, want %d", i, len(entry.Data), payloadSize)
		}
	}

	term, err := store.Term(ctx, last)
	if err != nil {
		return fmt.Errorf("Term(%d): %w", last, err)
	}
	if term != 1 {
		return fmt.Errorf("Term(%d) = %d, want 1", last, term)
	}
	return nil
}

func runPebbleSnapshotStress(ctx context.Context, stores []multiraft.Storage, models []stressGroupModel, cfg pebbleStressConfig) error {
	var (
		nextOp uint64
		failed atomic.Bool
		wg     sync.WaitGroup
	)
	errCh := make(chan error, 1)

	reportErr := func(err error) {
		if err == nil {
			return
		}
		if failed.CompareAndSwap(false, true) {
			errCh <- err
		}
	}

	for worker := 0; worker < cfg.writers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				if failed.Load() {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}

				op := int(atomic.AddUint64(&nextOp, 1)) - 1
				group := op % len(stores)
				if err := applySnapshotStressWrite(ctx, stores[group], &models[group], cfg.payload); err != nil {
					reportErr(fmt.Errorf("snapshot worker %d group %d: %w", workerID, group+1, err))
					return
				}
			}
		}(worker)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func applySnapshotStressWrite(ctx context.Context, store multiraft.Storage, model *stressGroupModel, payloadSize int) error {
	const (
		snapshotAdvance = 8
		postSnapEntries = 4
		snapshotTerm    = 2
		entryTerm       = 3
	)

	model.mu.Lock()
	defer model.mu.Unlock()

	snapshotIndex := model.lastIndex + snapshotAdvance
	confState := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	snap := raftpb.Snapshot{
		Data: []byte{byte(model.groupID), byte(snapshotIndex)},
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapshotIndex,
			Term:      snapshotTerm,
			ConfState: cloneConfState(confState),
		},
	}
	entries := benchEntries(snapshotIndex+1, postSnapEntries, entryTerm, payloadSize)
	hs := raftpb.HardState{
		Term:   entryTerm,
		Commit: snapshotIndex + postSnapEntries,
	}
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries:   entries,
	}); err != nil {
		return err
	}
	if err := store.MarkApplied(ctx, hs.Commit); err != nil {
		return err
	}

	model.snapshotIndex = snapshotIndex
	model.snapshotTerm = snapshotTerm
	model.firstIndex = snapshotIndex + 1
	model.lastIndex = hs.Commit
	model.appliedIndex = hs.Commit
	model.entryTerm = entryTerm
	model.confState = cloneConfState(confState)
	return nil
}
