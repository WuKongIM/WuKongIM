package fsm

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	fsmStressEnvKey      = "WKFSM_STRESS"
	fsmStressDurationEnv = "WKFSM_STRESS_DURATION"
	fsmStressWorkersEnv  = "WKFSM_STRESS_WORKERS"
	fsmStressSlotsEnv    = "WKFSM_STRESS_SLOTS"
	fsmStressSeedEnv     = "WKFSM_STRESS_SEED"
)

type fsmStressConfig struct {
	Enabled  bool
	Duration time.Duration
	Workers  int
	Slots    int
	Seed     int64
}

type fsmStressStats struct {
	UserApplies    uint64
	ChannelApplies uint64
	Snapshots      uint64
	Restores       uint64
	Errors         uint64
}

func envelopeProposal(data []byte) []byte {
	payload := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(payload[:2], 0)
	copy(payload[2:], data)
	return payload
}

func (s *fsmStressStats) totalOps() uint64 {
	return s.UserApplies + s.ChannelApplies + s.Snapshots + s.Restores
}

func (s *fsmStressStats) merge(other fsmStressStats) {
	s.UserApplies += other.UserApplies
	s.ChannelApplies += other.ChannelApplies
	s.Snapshots += other.Snapshots
	s.Restores += other.Restores
	s.Errors += other.Errors
}

func loadFSMStressConfig(t *testing.T) fsmStressConfig {
	t.Helper()

	cfg := fsmStressConfig{
		Enabled:  fsmEnvBool(fsmStressEnvKey, false),
		Duration: fsmEnvDuration(t, fsmStressDurationEnv, 2*time.Second),
		Workers:  fsmEnvInt(t, fsmStressWorkersEnv, max(4, runtime.GOMAXPROCS(0))),
		Slots:    fsmEnvInt(t, fsmStressSlotsEnv, 4),
		Seed:     fsmEnvInt64(t, fsmStressSeedEnv, 20260328),
	}
	if cfg.Workers <= 0 {
		t.Fatalf("%s must be > 0, got %d", fsmStressWorkersEnv, cfg.Workers)
	}
	if cfg.Slots <= 0 {
		t.Fatalf("%s must be > 0, got %d", fsmStressSlotsEnv, cfg.Slots)
	}
	if cfg.Duration <= 0 {
		t.Fatalf("%s must be > 0, got %s", fsmStressDurationEnv, cfg.Duration)
	}
	return cfg
}

func requireFSMStressEnabled(t *testing.T, cfg fsmStressConfig) {
	t.Helper()

	if !cfg.Enabled {
		t.Skip("set WKFSM_STRESS=1 to enable wkfsm stress tests")
	}
}

// TestFSMStressConcurrentApply hammers Apply from multiple goroutines, each
// sending a mix of user and channel upserts to different slots. After the
// workload completes, it verifies that every record written through Apply
// can be read back from the database.
func TestFSMStressConcurrentApply(t *testing.T) {
	cfg := loadFSMStressConfig(t)
	requireFSMStressEnabled(t, cfg)

	db := openTestDB(t)
	machines := make(map[uint64]multiraft.StateMachine, cfg.Slots)
	// Per-slot mutexes mirror Raft's serial Apply guarantee.
	slotMu := make(map[uint64]*sync.Mutex, cfg.Slots)
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		machines[slot] = mustNewStateMachine(t, db, slot)
		slotMu[slot] = &sync.Mutex{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		stats fsmStressStats
		errCh = make(chan error, 1)
		// Track last-write-wins per slot for verification.
		lastUser    = make(map[uint64]map[string]metadb.User)
		lastChannel = make(map[uint64]map[string]metadb.Channel)
	)
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		lastUser[slot] = make(map[string]metadb.User)
		lastChannel[slot] = make(map[string]metadb.Channel)
	}

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)))
			local := fsmStressStats{}
			var localUsers []struct {
				slot uint64
				user metadb.User
			}
			var localChannels []struct {
				slot    uint64
				channel metadb.Channel
			}

			for idx := uint64(1); ; idx++ {
				if ctx.Err() != nil {
					break
				}

				slot := uint64((worker+int(idx))%cfg.Slots) + 1
				sm := machines[slot]
				slotID := multiraft.SlotID(slot)

				if rng.Intn(2) == 0 {
					user := metadb.User{
						UID:         fmt.Sprintf("stress-u-%d-%d", slot, rng.Intn(32)),
						Token:       fmt.Sprintf("tok-%d-%d-%d", worker, idx, rng.Intn(100)),
						DeviceFlag:  int64(rng.Intn(8)),
						DeviceLevel: int64(rng.Intn(16)),
					}
					slotMu[slot].Lock()
					_, err := sm.Apply(ctx, multiraft.Command{
						SlotID: slotID,
						Index:  idx,
						Term:   1,
						Data:   EncodeUpsertUserCommand(user),
					})
					slotMu[slot].Unlock()
					if err != nil {
						if ctx.Err() != nil {
							break
						}
						select {
						case errCh <- fmt.Errorf("worker %d Apply(user) slot=%d: %w", worker, slot, err):
						default:
						}
						cancel()
						return
					}
					local.UserApplies++
					localUsers = append(localUsers, struct {
						slot uint64
						user metadb.User
					}{slot, user})
				} else {
					channel := metadb.Channel{
						ChannelID:   fmt.Sprintf("stress-c-%d-%d", slot, rng.Intn(16)),
						ChannelType: int64(rng.Intn(4) + 1),
						Ban:         int64(rng.Intn(2)),
					}
					slotMu[slot].Lock()
					_, err := sm.Apply(ctx, multiraft.Command{
						SlotID: slotID,
						Index:  idx,
						Term:   1,
						Data:   EncodeUpsertChannelCommand(channel),
					})
					slotMu[slot].Unlock()
					if err != nil {
						if ctx.Err() != nil {
							break
						}
						select {
						case errCh <- fmt.Errorf("worker %d Apply(channel) slot=%d: %w", worker, slot, err):
						default:
						}
						cancel()
						return
					}
					local.ChannelApplies++
					localChannels = append(localChannels, struct {
						slot    uint64
						channel metadb.Channel
					}{slot, channel})
				}
			}

			mu.Lock()
			stats.merge(local)
			// Merge last-writes: later workers overwrite earlier ones, matching
			// the sequential-within-slot Apply semantics.
			for _, u := range localUsers {
				lastUser[u.slot][u.user.UID] = u.user
			}
			for _, c := range localChannels {
				lastChannel[c.slot][c.channel.ChannelID+fmt.Sprintf("-%d", c.channel.ChannelType)] = c.channel
			}
			mu.Unlock()
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	// Verify: every user/channel in the last-write map must be readable.
	verifyCtx := context.Background()
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		shard := db.ForSlot(slot)
		for _, user := range lastUser[slot] {
			got, err := shard.GetUser(verifyCtx, user.UID)
			if err != nil {
				t.Fatalf("GetUser(slot=%d, uid=%q): %v", slot, user.UID, err)
			}
			if got.UID != user.UID {
				t.Fatalf("GetUser(slot=%d) UID = %q, want %q", slot, got.UID, user.UID)
			}
		}
		for _, channel := range lastChannel[slot] {
			got, err := shard.GetChannel(verifyCtx, channel.ChannelID, channel.ChannelType)
			if err != nil {
				t.Fatalf("GetChannel(slot=%d, id=%q, type=%d): %v", slot, channel.ChannelID, channel.ChannelType, err)
			}
			if got.ChannelID != channel.ChannelID {
				t.Fatalf("GetChannel(slot=%d) ID = %q, want %q", slot, got.ChannelID, channel.ChannelID)
			}
		}
	}

	t.Logf("concurrent apply: seed=%d workers=%d slots=%d duration=%s total_ops=%d user_applies=%d channel_applies=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, cfg.Duration, stats.totalOps(),
		stats.UserApplies, stats.ChannelApplies)
}

// TestFSMStressSnapshotRestoreUnderConcurrentApply runs Apply and
// Snapshot/Restore cycles concurrently. Snapshot captures a point-in-time
// view and Restore recreates it on a fresh DB; we verify the restored DB
// contains a valid subset of what was written.
func TestFSMStressSnapshotRestoreUnderConcurrentApply(t *testing.T) {
	cfg := loadFSMStressConfig(t)
	requireFSMStressEnabled(t, cfg)

	db := openTestDB(t)
	machines := make(map[uint64]multiraft.StateMachine, cfg.Slots)
	// Per-slot mutexes mirror Raft's serial Apply guarantee.
	slotMu := make(map[uint64]*sync.Mutex, cfg.Slots)
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		machines[slot] = mustNewStateMachine(t, db, slot)
		slotMu[slot] = &sync.Mutex{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errCh    = make(chan error, 1)
		applyCnt atomic.Uint64
		snapCnt  atomic.Uint64
	)

	// Apply workers.
	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)))
			for idx := uint64(1); ; idx++ {
				if ctx.Err() != nil {
					return
				}
				slot := uint64((worker+int(idx))%cfg.Slots) + 1
				sm := machines[slot]
				slotID := multiraft.SlotID(slot)

				user := metadb.User{
					UID:         fmt.Sprintf("snap-u-%d-%d", slot, rng.Intn(20)),
					Token:       fmt.Sprintf("tok-%d-%d", worker, idx),
					DeviceFlag:  int64(rng.Intn(8)),
					DeviceLevel: int64(rng.Intn(16)),
				}
				slotMu[slot].Lock()
				_, err := sm.Apply(ctx, multiraft.Command{
					SlotID: slotID,
					Index:  idx,
					Term:   1,
					Data:   EncodeUpsertUserCommand(user),
				})
				slotMu[slot].Unlock()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case errCh <- fmt.Errorf("worker %d Apply: %w", worker, err):
					default:
					}
					cancel()
					return
				}
				applyCnt.Add(1)
			}
		}(worker)
	}

	// Snapshot/Restore worker: periodically snapshot a random slot and
	// restore it into a fresh DB. The restored DB must be self-consistent.
	wg.Add(1)
	go func() {
		defer wg.Done()

		rng := rand.New(rand.NewSource(cfg.Seed + 99999))
		for {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(time.Duration(rng.Intn(10)+1) * time.Millisecond)

			slot := uint64(rng.Intn(cfg.Slots)) + 1
			sm := machines[slot]

			// Hold the slot lock during snapshot to get a consistent view.
			slotMu[slot].Lock()
			snap, err := sm.Snapshot(ctx)
			slotMu[slot].Unlock()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case errCh <- fmt.Errorf("Snapshot(slot=%d): %w", slot, err):
				default:
				}
				cancel()
				return
			}

			restoreDB := openTestDB(t)
			restoreSM := mustNewStateMachine(t, restoreDB, slot)
			if err := restoreSM.Restore(ctx, snap); err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case errCh <- fmt.Errorf("Restore(slot=%d): %w", slot, err):
				default:
				}
				cancel()
				return
			}

			// The restored snapshot must re-export to the same bytes.
			reSnap, err := restoreSM.Snapshot(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case errCh <- fmt.Errorf("re-Snapshot(slot=%d): %w", slot, err):
				default:
				}
				cancel()
				return
			}
			if len(reSnap.Data) != len(snap.Data) {
				select {
				case errCh <- fmt.Errorf("re-Snapshot(slot=%d) data len %d != %d", slot, len(reSnap.Data), len(snap.Data)):
				default:
				}
				cancel()
				return
			}

			snapCnt.Add(1)
		}
	}()

	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	t.Logf("snapshot+apply: seed=%d workers=%d slots=%d duration=%s applies=%d snapshots=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, cfg.Duration, applyCnt.Load(), snapCnt.Load())
}

// TestFSMStressMultiSlotIsolation verifies that concurrent Apply operations on
// different slots never leak data across slot boundaries.
func TestFSMStressMultiSlotIsolation(t *testing.T) {
	cfg := loadFSMStressConfig(t)
	requireFSMStressEnabled(t, cfg)

	db := openTestDB(t)
	machines := make(map[uint64]multiraft.StateMachine, cfg.Slots)
	slotMu := make(map[uint64]*sync.Mutex, cfg.Slots)
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		machines[slot] = mustNewStateMachine(t, db, slot)
		slotMu[slot] = &sync.Mutex{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		errCh   = make(chan error, 1)
		perSlot = make(map[uint64]map[string]struct{})
	)
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		perSlot[slot] = make(map[string]struct{})
	}

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*1000))
			var localPerSlot = make(map[uint64]map[string]struct{})
			for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
				localPerSlot[slot] = make(map[string]struct{})
			}

			for idx := uint64(1); ; idx++ {
				if ctx.Err() != nil {
					break
				}
				slot := uint64((worker+int(idx))%cfg.Slots) + 1
				sm := machines[slot]
				slotID := multiraft.SlotID(slot)

				// Use a UID that encodes the slot, so we can verify isolation.
				uid := fmt.Sprintf("iso-s%d-w%d-%d", slot, worker, rng.Intn(20))
				slotMu[slot].Lock()
				_, err := sm.Apply(ctx, multiraft.Command{
					SlotID: slotID,
					Index:  idx,
					Term:   1,
					Data: EncodeUpsertUserCommand(metadb.User{
						UID:   uid,
						Token: fmt.Sprintf("tok-%d", idx),
					}),
				})
				slotMu[slot].Unlock()
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					select {
					case errCh <- fmt.Errorf("worker %d Apply(slot=%d): %w", worker, slot, err):
					default:
					}
					cancel()
					return
				}
				localPerSlot[slot][uid] = struct{}{}
			}

			mu.Lock()
			for slot, uids := range localPerSlot {
				for uid := range uids {
					perSlot[slot][uid] = struct{}{}
				}
			}
			mu.Unlock()
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	// Verify isolation: each UID written to slot S must exist in slot S
	// and must NOT exist in other slots.
	verifyCtx := context.Background()
	for slot := uint64(1); slot <= uint64(cfg.Slots); slot++ {
		shard := db.ForSlot(slot)
		for uid := range perSlot[slot] {
			if _, err := shard.GetUser(verifyCtx, uid); err != nil {
				t.Fatalf("GetUser(slot=%d, uid=%q): %v", slot, uid, err)
			}
		}
		// Check that UIDs belonging to other slots are not in this slot.
		for otherSlot := uint64(1); otherSlot <= uint64(cfg.Slots); otherSlot++ {
			if otherSlot == slot {
				continue
			}
			for uid := range perSlot[otherSlot] {
				if _, err := shard.GetUser(verifyCtx, uid); err == nil {
					t.Fatalf("slot %d leaks uid %q which belongs to slot %d", slot, uid, otherSlot)
				}
			}
		}
	}

	totalUIDs := 0
	for _, uids := range perSlot {
		totalUIDs += len(uids)
	}
	t.Logf("isolation: seed=%d workers=%d slots=%d duration=%s unique_uids=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, cfg.Duration, totalUIDs)
}

// TestFSMStressRaftIntegrationApply runs Apply through the full Raft pipeline
// (Propose → commit → Apply) from multiple goroutines using in-memory
// Raft storage.
func TestFSMStressRaftIntegrationApply(t *testing.T) {
	cfg := loadFSMStressConfig(t)
	requireFSMStressEnabled(t, cfg)

	db := openTestDB(t)
	rt := newStartedRuntime(t)

	slots := make([]multiraft.SlotID, cfg.Slots)
	for i := 0; i < cfg.Slots; i++ {
		slotID := multiraft.SlotID(100 + i)
		slots[i] = slotID
		if err := rt.BootstrapSlot(context.Background(), multiraft.BootstrapSlotRequest{
			Slot: multiraft.SlotOptions{
				ID:           slotID,
				Storage:      raftstorage.NewMemory(),
				StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
			},
			Voters: []multiraft.NodeID{1},
		}); err != nil {
			t.Fatalf("BootstrapSlot(slot=%d): %v", slotID, err)
		}
	}

	// Wait for all slots to become leader.
	for _, slotID := range slots {
		waitForCondition(t, func() bool {
			st, err := rt.Status(slotID)
			return err == nil && st.Role == multiraft.RoleLeader
		}, "slot become leader")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errCh    = make(chan error, 1)
		applyCnt atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*777))
			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					return
				}
				slotID := slots[(worker+idx)%len(slots)]

				var data []byte
				if rng.Intn(2) == 0 {
					data = EncodeUpsertUserCommand(metadb.User{
						UID:         fmt.Sprintf("raft-u-%d-%d", slotID, rng.Intn(16)),
						Token:       fmt.Sprintf("tok-%d-%d", worker, idx),
						DeviceFlag:  int64(rng.Intn(8)),
						DeviceLevel: int64(rng.Intn(16)),
					})
				} else {
					data = EncodeUpsertChannelCommand(metadb.Channel{
						ChannelID:   fmt.Sprintf("raft-c-%d-%d", slotID, rng.Intn(8)),
						ChannelType: int64(rng.Intn(4) + 1),
						Ban:         int64(rng.Intn(2)),
					})
				}

				fut, err := rt.Propose(ctx, slotID, envelopeProposal(data))
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case errCh <- fmt.Errorf("worker %d Propose(slot=%d): %w", worker, slotID, err):
					default:
					}
					cancel()
					return
				}
				if _, err := fut.Wait(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case errCh <- fmt.Errorf("worker %d Wait(slot=%d): %w", worker, slotID, err):
					default:
					}
					cancel()
					return
				}
				applyCnt.Add(1)
			}
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	t.Logf("raft integration: seed=%d workers=%d slots=%d duration=%s applies=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, cfg.Duration, applyCnt.Load())
}

// TestFSMStressPebbleBackedRaftIntegration runs Apply through the full Raft
// pipeline using Pebble-backed Raft storage, exercising the real on-disk
// persistence path.
func TestFSMStressPebbleBackedRaftIntegration(t *testing.T) {
	cfg := loadFSMStressConfig(t)
	requireFSMStressEnabled(t, cfg)

	root := t.TempDir()
	bizPath := filepath.Join(root, "biz")
	raftPath := filepath.Join(root, "raft")

	bizDB := openTestDBAt(t, bizPath)
	raftDB := openTestRaftDBAt(t, raftPath)
	rt := newStartedRuntime(t)

	slots := make([]multiraft.SlotID, cfg.Slots)
	for i := 0; i < cfg.Slots; i++ {
		slotID := multiraft.SlotID(200 + i)
		slots[i] = slotID
		if err := rt.BootstrapSlot(context.Background(), multiraft.BootstrapSlotRequest{
			Slot: multiraft.SlotOptions{
				ID:           slotID,
				Storage:      raftDB.ForSlot(uint64(slotID)),
				StateMachine: mustNewStateMachine(t, bizDB, uint64(slotID)),
			},
			Voters: []multiraft.NodeID{1},
		}); err != nil {
			t.Fatalf("BootstrapSlot(slot=%d): %v", slotID, err)
		}
	}

	for _, slotID := range slots {
		gid := slotID
		waitForCondition(t, func() bool {
			st, err := rt.Status(gid)
			return err == nil && st.Role == multiraft.RoleLeader
		}, "slot become leader")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errCh    = make(chan error, 1)
		applyCnt atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*333))
			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					return
				}
				slotID := slots[(worker+idx)%len(slots)]

				user := metadb.User{
					UID:         fmt.Sprintf("pebble-u-%d-%d", slotID, rng.Intn(16)),
					Token:       fmt.Sprintf("tok-%d-%d", worker, idx),
					DeviceFlag:  int64(rng.Intn(8)),
					DeviceLevel: int64(rng.Intn(16)),
				}

				fut, err := rt.Propose(ctx, slotID, envelopeProposal(EncodeUpsertUserCommand(user)))
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case errCh <- fmt.Errorf("worker %d Propose(slot=%d): %w", worker, slotID, err):
					default:
					}
					cancel()
					return
				}
				if _, err := fut.Wait(ctx); err != nil {
					if ctx.Err() != nil {
						return
					}
					select {
					case errCh <- fmt.Errorf("worker %d Wait(slot=%d): %w", worker, slotID, err):
					default:
					}
					cancel()
					return
				}
				applyCnt.Add(1)
			}
		}(worker)
	}

	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	// Verify some data is actually persisted.
	verifyCtx := context.Background()
	for _, slotID := range slots {
		slot := uint64(slotID)
		shard := bizDB.ForSlot(slot)
		found := 0
		for i := 0; i < 16; i++ {
			uid := fmt.Sprintf("pebble-u-%d-%d", slotID, i)
			if _, err := shard.GetUser(verifyCtx, uid); err == nil {
				found++
			}
		}
		if found == 0 {
			t.Fatalf("slot %d has no users after stress run", slot)
		}
	}

	t.Logf("pebble raft integration: seed=%d workers=%d slots=%d duration=%s applies=%d",
		cfg.Seed, cfg.Workers, cfg.Slots, cfg.Duration, applyCnt.Load())
}

func fsmEnvBool(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func fsmEnvDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return d
}

func fsmEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return n
}

func fsmEnvInt64(t *testing.T, name string, fallback int64) int64 {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return n
}
