package meta

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type stressStats struct {
	Seed           int64
	Workers        int
	Slots          int
	Duration       time.Duration
	UserCreates    uint64
	UserUpdates    uint64
	UserDeletes    uint64
	UserReads      uint64
	ChannelCreates uint64
	ChannelUpdates uint64
	ChannelDeletes uint64
	ChannelReads   uint64
	ChannelLists   uint64
	SnapshotBytes  int
}

func TestStressConcurrentUsersAndChannels(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	db := openTestDB(t)
	model := newStressModel()

	stats, err := runMixedStressWorkload(t, db, model, cfg)
	if err != nil {
		t.Fatalf("runMixedStressWorkload(): %v", err)
	}
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		assertSlotMatchesModel(t, db, slot, model)
	}
	t.Logf("mixed stress: seed=%d workers=%d slots=%d duration=%s total_ops=%d user_create=%d user_update=%d user_delete=%d user_read=%d channel_create=%d channel_update=%d channel_delete=%d channel_read=%d channel_list=%d",
		stats.Seed, stats.Workers, stats.Slots, stats.Duration, stats.totalOps(),
		stats.UserCreates, stats.UserUpdates, stats.UserDeletes, stats.UserReads,
		stats.ChannelCreates, stats.ChannelUpdates, stats.ChannelDeletes, stats.ChannelReads, stats.ChannelLists)
}

func TestStressChannelIndexConsistency(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	db := openTestDB(t)
	model := newStressModel()

	if err := runChannelIndexStress(t, db, model, cfg); err != nil {
		t.Fatalf("runChannelIndexStress(): %v", err)
	}
}

func TestStressSnapshotRestoreUnderLoad(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	source := openTestDB(t)
	model := newStressModel()
	stats, err := runMixedStressWorkload(t, source, model, cfg)
	if err != nil {
		t.Fatalf("runMixedStressWorkload(): %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "restore-db")
	target := openTestDBAt(t, targetPath)
	if err := verifySnapshotRestore(t, source, target, model, cfg); err != nil {
		t.Fatalf("verifySnapshotRestore(): %v", err)
	}
	t.Logf("snapshot source load: seed=%d workers=%d slots=%d duration=%s total_ops=%d",
		stats.Seed, stats.Workers, stats.Slots, stats.Duration, stats.totalOps())
}

func requireStressEnabled(t *testing.T, cfg stressConfig) {
	t.Helper()

	if !cfg.Enabled {
		t.Skip("set WKDB_STRESS=1 to enable metadb stress tests")
	}
}

func runMixedStressWorkload(t *testing.T, db *DB, model *stressModel, cfg stressConfig) (stressStats, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	stats := stressStats{
		Seed:     cfg.Seed,
		Workers:  cfg.Workers,
		Slots:    cfg.Slots,
		Duration: cfg.Duration,
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		errCh = make(chan error, 1)
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)))
			local := stressStats{}

			for opIndex := 0; ; opIndex++ {
				if ctx.Err() != nil {
					break
				}
				slot := uint64((worker+opIndex)%cfg.Slots) + 1
				if err := runMixedStressOperation(ctx, db.ForSlot(slot), model, slot, worker, opIndex, rng, &local); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					break
				}
			}

			mu.Lock()
			stats.merge(local)
			mu.Unlock()
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return stats, err
	default:
		return stats, nil
	}
}

func runMixedStressOperation(ctx context.Context, shard *ShardStore, model *stressModel, slot uint64, worker int, opIndex int, rng *rand.Rand, stats *stressStats) error {
	userID := stressUserID(slot, rng.Intn(32))
	channelID := stressChannelID(slot, rng.Intn(16))
	channelType := int64(rng.Intn(4) + 1)

	switch rng.Intn(9) {
	case 0:
		user := stressUserRecord(userID, worker, opIndex)
		err := shard.CreateUser(ctx, user)
		if err == nil {
			model.putUser(slot, user)
			stats.UserCreates++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrAlreadyExists) {
			return nil
		}
		return err
	case 1:
		user := stressUserRecord(userID, worker, opIndex)
		err := shard.UpdateUser(ctx, user)
		if err == nil {
			model.putUser(slot, user)
			stats.UserUpdates++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	case 2:
		err := shard.DeleteUser(ctx, userID)
		if err == nil {
			model.deleteUser(slot, userID)
			stats.UserDeletes++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	case 3:
		_, err := shard.GetUser(ctx, userID)
		if err = normalizeStressErr(ctx, err); err == nil {
			stats.UserReads++
			return nil
		}
		if err == nil || errors.Is(err, ErrNotFound) {
			stats.UserReads++
			return nil
		}
		return err
	case 4:
		channel := stressChannelRecord(channelID, channelType, worker, opIndex)
		err := shard.CreateChannel(ctx, channel)
		if err == nil {
			model.putChannel(slot, channel)
			stats.ChannelCreates++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrAlreadyExists) {
			return nil
		}
		return err
	case 5:
		channel := stressChannelRecord(channelID, channelType, worker, opIndex)
		err := shard.UpdateChannel(ctx, channel)
		if err == nil {
			model.putChannel(slot, channel)
			stats.ChannelUpdates++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	case 6:
		err := shard.DeleteChannel(ctx, channelID, channelType)
		if err == nil {
			model.deleteChannel(slot, channelID, channelType)
			stats.ChannelDeletes++
			return nil
		}
		if err = normalizeStressErr(ctx, err); err == nil {
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	case 7:
		_, err := shard.GetChannel(ctx, channelID, channelType)
		if err = normalizeStressErr(ctx, err); err == nil {
			stats.ChannelReads++
			return nil
		}
		if err == nil || errors.Is(err, ErrNotFound) {
			stats.ChannelReads++
			return nil
		}
		return err
	default:
		_, err := shard.ListChannelsByChannelID(ctx, channelID)
		if err = normalizeStressErr(ctx, err); err == nil {
			stats.ChannelLists++
			return nil
		}
		if err == nil {
			stats.ChannelLists++
			return nil
		}
		return err
	}
}

func runChannelIndexStress(t *testing.T, db *DB, model *stressModel, cfg stressConfig) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	stats := stressStats{
		Seed:     cfg.Seed,
		Workers:  cfg.Workers,
		Slots:    cfg.Slots,
		Duration: cfg.Duration,
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		errCh = make(chan error, 1)
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + 10_000 + int64(worker)))
			local := stressStats{}

			for opIndex := 0; ; opIndex++ {
				if ctx.Err() != nil {
					break
				}

				slot := uint64((worker+opIndex)%cfg.Slots) + 1
				channelID := fmt.Sprintf("index-worker-%d-channel-%d", worker, rng.Intn(6))
				channelType := int64(rng.Intn(4) + 1)
				shard := db.ForSlot(slot)

				switch rng.Intn(5) {
				case 0:
					channel := stressChannelRecord(channelID, channelType, worker, opIndex)
					err := shard.CreateChannel(ctx, channel)
					if err == nil {
						model.putChannel(slot, channel)
						local.ChannelCreates++
						continue
					}
					if err = normalizeStressErr(ctx, err); err == nil {
						continue
					}
					if errors.Is(err, ErrAlreadyExists) {
						continue
					}
					select {
					case errCh <- err:
					default:
					}
					cancel()
					break
				case 1:
					channel := stressChannelRecord(channelID, channelType, worker, opIndex)
					err := shard.UpdateChannel(ctx, channel)
					if err == nil {
						model.putChannel(slot, channel)
						local.ChannelUpdates++
						continue
					}
					if err = normalizeStressErr(ctx, err); err == nil {
						continue
					}
					if errors.Is(err, ErrNotFound) {
						continue
					}
					select {
					case errCh <- err:
					default:
					}
					cancel()
					break
				case 2:
					err := shard.DeleteChannel(ctx, channelID, channelType)
					if err == nil {
						model.deleteChannel(slot, channelID, channelType)
						local.ChannelDeletes++
						continue
					}
					if err = normalizeStressErr(ctx, err); err == nil {
						continue
					}
					if errors.Is(err, ErrNotFound) {
						continue
					}
					select {
					case errCh <- err:
					default:
					}
					cancel()
					break
				case 3:
					_, err := shard.GetChannel(ctx, channelID, channelType)
					if err = normalizeStressErr(ctx, err); err == nil {
						local.ChannelReads++
						continue
					}
					if err == nil || errors.Is(err, ErrNotFound) {
						local.ChannelReads++
						continue
					}
					select {
					case errCh <- err:
					default:
					}
					cancel()
					break
				default:
					got, err := shard.ListChannelsByChannelID(ctx, channelID)
					if err = normalizeStressErr(ctx, err); err == nil {
						local.ChannelLists++
						continue
					}
					if err != nil {
						select {
						case errCh <- err:
						default:
						}
						cancel()
						break
					}
					want := model.listChannels(slot, channelID)
					if !reflect.DeepEqual(got, want) {
						select {
						case errCh <- fmt.Errorf("slot=%d channelID=%q list mismatch: got=%#v want=%#v", slot, channelID, got, want):
						default:
						}
						cancel()
						break
					}
					local.ChannelLists++
				}
			}

			mu.Lock()
			stats.merge(local)
			mu.Unlock()
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
	}

	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		assertSlotMatchesModel(t, db, slot, model)
	}
	t.Logf("index stress: seed=%d workers=%d slots=%d duration=%s total_ops=%d channel_create=%d channel_update=%d channel_delete=%d channel_read=%d channel_list=%d",
		stats.Seed, stats.Workers, stats.Slots, stats.Duration, stats.totalOps(),
		stats.ChannelCreates, stats.ChannelUpdates, stats.ChannelDeletes, stats.ChannelReads, stats.ChannelLists)
	return nil
}

func verifySnapshotRestore(t *testing.T, source *DB, target *DB, model *stressModel, cfg stressConfig) error {
	t.Helper()

	ctx := context.Background()
	slot := uint64(1)
	snap, err := source.ExportSlotSnapshot(ctx, slot)
	if err != nil {
		return err
	}
	if err := target.ImportSlotSnapshot(ctx, snap); err != nil {
		return err
	}

	assertSlotMatchesModel(t, target, slot, model)
	for other := uint64(1); other <= uint64(cfg.Slots); other++ {
		if other == slot {
			continue
		}
		if err := ensureSlotEmpty(target, other); err != nil {
			return err
		}
	}

	t.Logf("snapshot restore: seed=%d workers=%d slots=%d slot=%d bytes=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, slot, len(snap.Data))
	return nil
}

func ensureSlotEmpty(db *DB, slot uint64) error {
	ctx := context.Background()
	snap, err := db.ExportSlotSnapshot(ctx, slot)
	if err != nil {
		return err
	}
	decoded, err := decodeSlotSnapshotPayload(snap.Data)
	if err != nil {
		return err
	}
	if len(decoded.Entries) != 0 {
		return fmt.Errorf("slot %d not empty after restore, entries=%d", slot, len(decoded.Entries))
	}
	return nil
}

func (s *stressStats) merge(other stressStats) {
	s.UserCreates += other.UserCreates
	s.UserUpdates += other.UserUpdates
	s.UserDeletes += other.UserDeletes
	s.UserReads += other.UserReads
	s.ChannelCreates += other.ChannelCreates
	s.ChannelUpdates += other.ChannelUpdates
	s.ChannelDeletes += other.ChannelDeletes
	s.ChannelReads += other.ChannelReads
	s.ChannelLists += other.ChannelLists
	if other.SnapshotBytes > s.SnapshotBytes {
		s.SnapshotBytes = other.SnapshotBytes
	}
}

func (s stressStats) totalOps() uint64 {
	return s.UserCreates + s.UserUpdates + s.UserDeletes + s.UserReads +
		s.ChannelCreates + s.ChannelUpdates + s.ChannelDeletes + s.ChannelReads + s.ChannelLists
}

func stressUserID(slot uint64, index int) string {
	return fmt.Sprintf("stress-user-%d-%d", slot, index)
}

func stressChannelID(slot uint64, index int) string {
	return fmt.Sprintf("stress-channel-%d-%d", slot, index)
}

func stressUserRecord(uid string, worker int, opIndex int) User {
	return User{
		UID:         uid,
		Token:       fmt.Sprintf("token-%d-%d", worker, opIndex),
		DeviceFlag:  int64((worker + opIndex) % 8),
		DeviceLevel: int64((worker + opIndex) % 16),
	}
}

func stressChannelRecord(channelID string, channelType int64, worker int, opIndex int) Channel {
	return Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         int64((worker + opIndex) % 2),
	}
}

func normalizeStressErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
	return err
}
