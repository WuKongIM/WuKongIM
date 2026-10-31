//go:build integration

package presence_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// External generation claims hold fifteen executors. A victim with real empty
// OwnerGeneration pages must finish bounded useful work before yielding behind
// new cold claims; a fifth page must still yield to the original FIFO.
type slotTurnFixture struct {
	*slotFairFixture
	owners                                                    map[uint64]*p.OwnerGeneration
	ids                                                       []uint64
	seedEntered                                               chan struct{}
	victimFirst, victimRelease, seedFree, coldFree, cold2Free chan struct{}
	resumeReached, resumeRelease, cold2Entered                chan struct{}
	resumeAt                                                  int32
	failSecond                                                bool
	cancelSecond                                              context.CancelFunc
	secondOnce                                                sync.Once
	coldEntered                                               chan struct{}
	firstOnce, coldOnce                                       sync.Once
	pages, ends                                               atomic.Int32
	rows                                                      atomic.Int64
	stringBytes                                               atomic.Uint64
}

func (f *slotTurnFixture) ClaimSlotGeneration(ctx context.Context, target p.RouteTarget) (p.GenerationClaim, []uint64, error) {
	claim, _, err := f.slotFairFixture.ClaimSlotGeneration(ctx, target)
	if err != nil {
		return p.GenerationClaim{}, nil, err
	}
	if target.HashSlot != 15 {
		gate := f.coldFree
		if target.HashSlot < 15 {
			f.seedEntered <- struct{}{}
			gate = f.seedFree
		} else if target.HashSlot >= 48 {
			f.secondOnce.Do(func() { close(f.cold2Entered) })
			gate = f.cold2Free
		} else {
			f.coldOnce.Do(func() { close(f.coldEntered) })
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return p.GenerationClaim{}, nil, ctx.Err()
		}
	}
	return claim, append([]uint64(nil), f.ids...), nil
}
func (f *slotTurnFixture) BeginSlot(ctx context.Context, claim p.GenerationClaim, id uint64) (p.OwnerCut, error) {
	return f.owners[id].BeginSlot(ctx, claim)
}
func (f *slotTurnFixture) ReadSlotPage(ctx context.Context, req p.SlotPageRequest, budget *p.RecoveryResultBudget) (*p.OwnedSlotPage, error) {
	page, err := f.owners[req.Owner.NodeID].ReadSlotPage(ctx, req, budget)
	if err == nil && req.Claim.Target.HashSlot == 15 {
		index := f.pages.Add(1)
		f.rows.Add(int64(len(page.Page.Routes)))
		for _, route := range page.Page.Routes {
			f.stringBytes.Add(uint64(len(route.UID) + len(route.DeviceID) + len(route.Listener)))
		}
		var gate <-chan struct{}
		if index == 1 {
			f.firstOnce.Do(func() { close(f.victimFirst) })
			gate = f.victimRelease
		} else if index == f.resumeAt {
			close(f.resumeReached)
			gate = f.resumeRelease
		}
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return page, ctx.Err()
			}
		}
		if index == 2 {
			if f.cancelSecond != nil {
				f.cancelSecond()
				return page, context.Canceled
			}
			if f.failSecond {
				return page, errors.New("controlled second owner page failure")
			}
		}
	}
	return page, err
}
func (f *slotTurnFixture) EndSlot(ctx context.Context, req p.SlotEndRequest) (p.SlotEnd, error) {
	end, err := f.owners[req.Owner.NodeID].EndSlot(ctx, req)
	if err == nil && req.Claim.Target.HashSlot == 15 {
		f.ends.Add(1)
	}
	return end, err
}
func (f *slotTurnFixture) AbortSlot(ctx context.Context, claim p.GenerationClaim, cut p.OwnerCut) error {
	return f.owners[cut.NodeID].AbortSlot(ctx, claim, cut)
}
func (f *slotTurnFixture) ReadGenerationUIDs(ctx context.Context, claim p.GenerationClaim, cut p.OwnerCut, uids []string, budget *p.RecoveryResultBudget) (*p.OwnedSlotRoutes, error) {
	return f.owners[cut.NodeID].ReadGenerationUIDs(ctx, claim, cut, uids, budget)
}

func TestPresenceSmallRecoveryTurnsFinishWorkAndRetainFIFO(t *testing.T) {
	cases := []struct {
		name                                        string
		owners, rows, uidBytes, firstCut, secondCut int
		fail, cancel                                bool
	}{
		{name: "three_empty", owners: 3},
		{name: "five_empty_fifo", owners: 5, firstCut: 4},
		{name: "page_cut_resets", owners: 9, firstCut: 4, secondCut: 8},
		{name: "row_cut_resets", owners: 5, rows: 2048, firstCut: 2, secondCut: 4},
		{name: "byte_cut_resets", owners: 5, rows: 1024, uidBytes: 2048, firstCut: 2, secondCut: 4},
		{name: "second_page_error", owners: 3, fail: true},
		{name: "second_page_cancel", owners: 3, cancel: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owners := tc.owners
			directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
			f := &slotTurnFixture{slotFairFixture: &slotFairFixture{claims: make(map[uint16]p.GenerationClaim)}, owners: make(map[uint64]*p.OwnerGeneration), seedEntered: make(chan struct{}, 15), victimFirst: make(chan struct{}), victimRelease: make(chan struct{}), seedFree: make(chan struct{}), coldFree: make(chan struct{}), cold2Free: make(chan struct{}), coldEntered: make(chan struct{}), cold2Entered: make(chan struct{}), resumeReached: make(chan struct{}), resumeRelease: make(chan struct{}), failSecond: tc.fail}
			if tc.secondCut > 0 {
				f.resumeAt = int32(tc.firstCut + 1)
			}
			for id := uint64(1); id <= uint64(owners); id++ {
				f.ids = append(f.ids, id)
				registry := online.NewRegistry(online.RegistryOptions{})
				for i := 1; i <= tc.rows; i++ {
					uid := fmt.Sprintf("turn-owned-%d-%04d-", id, i)
					if len(uid) < tc.uidBytes {
						uid += strings.Repeat("u", tc.uidBytes-len(uid))
					}
					route := online.OwnerRoute{UID: uid, HashSlot: 15, OwnerNodeID: id, OwnerBootID: 10 + id, OwnerSeq: uint64(i), SessionID: uint64(i), DeviceID: "device", LastActivityUnix: 100, ConnectedUnix: 100}
					if err := registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
						t.Fatal(err)
					}
					if err := registry.MarkActive(uint64(i)); err != nil {
						t.Fatal(err)
					}
				}
				f.owners[id] = p.NewOwnerGeneration(registry, id, 10+id, f.ValidateSlotGeneration)
			}
			targets := make([]p.RouteTarget, 80)
			for i := range targets {
				targets[i] = p.RouteTarget{HashSlot: uint16(i), SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}
				directory.BecomeAuthority(targets[i])
			}
			slots := p.NewSlotRecovery(directory, f, f)
			recovery := p.NewAuthorityRecovery(slots, slots)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			victimCtx, victimCancel := context.WithCancel(ctx)
			defer victimCancel()
			if tc.cancel {
				f.cancelSecond = victimCancel
			}
			var callers sync.WaitGroup
			var victimOnce, seedsOnce, coldOnce, cold2Once, resumeOnce sync.Once
			freeVictim := func() { victimOnce.Do(func() { close(f.victimRelease) }) }
			freeSeeds := func() { seedsOnce.Do(func() { close(f.seedFree) }) }
			freeCold := func() { coldOnce.Do(func() { close(f.coldFree) }) }
			freeCold2 := func() { cold2Once.Do(func() { close(f.cold2Free) }) }
			freeResume := func() { resumeOnce.Do(func() { close(f.resumeRelease) }) }
			t.Cleanup(func() {
				cancel()
				freeVictim()
				freeSeeds()
				freeCold()
				freeCold2()
				freeResume()
				callers.Wait()
				cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				if err := recovery.Close(cleanup); err != nil {
					t.Error(err)
				}
				if err := slots.Close(cleanup); err != nil {
					t.Error(err)
				}
				credits, stats := slots.RecoveryResultBudget().Stats(), recovery.Stats()
				t.Logf("joined=true result_rows=%d result_bytes=%d queued=%d active=%d fenced_keys=%d", credits.Rows, credits.Bytes, stats.QueuedCalls, stats.ActiveBatches, stats.FencedUIDs)
				if credits.Rows != 0 || credits.Bytes != 0 || stats.QueuedCalls != 0 || stats.ActiveBatches != 0 || stats.ResultCalls != 0 || stats.Members != 0 || stats.FencedUIDs != 0 || stats.OwnedUIDBytes != 0 {
					t.Errorf("joined recovery retained ownership: credits=%+v admission=%+v", credits, stats)
				}
			})
			lookup := func(index int, done chan<- error) {
				callers.Add(1)
				go func() {
					defer callers.Done()
					callCtx := ctx
					if index == 15 {
						callCtx = victimCtx
					}
					routes, err := recovery.Endpoints(callCtx, targets[index], []string{fmt.Sprintf("absent-turn-%d", index)})
					if err == nil && len(routes) != 0 {
						err = fmt.Errorf("empty owners produced %d routes", len(routes))
					}
					if done != nil {
						done <- err
					}
				}()
			}
			for i := 0; i < 15; i++ {
				lookup(i, nil)
				select {
				case <-f.seedEntered:
				case <-ctx.Done():
					t.Fatal("seed did not hold actual generation I/O")
				}
			}
			victim := make(chan error, 1)
			lookup(15, victim)
			waitArchitectureEvent(t, f.victimFirst, "victim first owned empty page")
			if !tc.fail && !tc.cancel {
				for i := 16; i < 48; i++ {
					lookup(i, nil)
				}
				for recovery.Stats().QueuedCalls < 32 {
					if ctx.Err() != nil {
						t.Fatal("new cold calls were not queued before victim release")
					}
					runtime.Gosched()
				}
			}
			freeVictim()
			if tc.firstCut > 0 {
				waitArchitectureEvent(t, f.coldEntered, "fresh cold call after bounded victim turn")
				if f.pages.Load() != int32(tc.firstCut) || f.ends.Load() != int32(tc.firstCut) {
					t.Fatalf("first work cut: pages=%d ends=%d want=%d", f.pages.Load(), f.ends.Load(), tc.firstCut)
				}
				if tc.rows == 2048 && f.rows.Load() != 4096 {
					t.Fatal("row fixture never reached cumulative row cut")
				}
				if tc.uidBytes > 0 && (f.stringBytes.Load() < 4<<20 || f.rows.Load() >= 4096) {
					t.Fatal("byte fixture did not independently reach byte cut")
				}
				select {
				case err := <-victim:
					t.Fatalf("victim completed before every owner End: %v", err)
				default:
				}
				freeCold()
			}
			if tc.secondCut > 0 {
				waitArchitectureEvent(t, f.resumeReached, "first page of resumed victim turn")
				beforeKeys := recovery.Stats().FencedUIDs
				for i := 48; i < 80; i++ {
					lookup(i, nil)
				}
				for recovery.Stats().FencedUIDs < beforeKeys+32 {
					if ctx.Err() != nil {
						t.Fatal("second cold wave was not admitted while the resumed turn was held")
					}
					runtime.Gosched()
				}
				freeResume()
				waitArchitectureEvent(t, f.cold2Entered, "FIFO admission after second bounded turn")
				if f.pages.Load() != int32(tc.secondCut) || f.ends.Load() != int32(tc.secondCut) {
					t.Fatalf("resumed work cut: pages=%d ends=%d want=%d", f.pages.Load(), f.ends.Load(), tc.secondCut)
				}
				freeCold2()
			}
			// Coordination watchdog only: the external claims remain blocked, so
			// the old one-page yield cannot complete irrespective of disk speed.
			select {
			case err := <-victim:
				if tc.fail || tc.cancel {
					if err == nil {
						t.Fatal("second-page failure created a complete offline certificate")
					}
					if tc.cancel && !errors.Is(err, context.Canceled) {
						t.Fatalf("original caller cancellation changed family: %v", err)
					}
					if f.pages.Load() != 2 || f.ends.Load() != 1 {
						t.Fatalf("failure continued beyond its actual second page: pages=%d ends=%d", f.pages.Load(), f.ends.Load())
					}
					_, complete, _ := directory.CurrentGeneration(targets[15])
					if complete {
						t.Fatal("failed page published Complete")
					}
					return
				}
				if err != nil {
					t.Fatalf("bounded recovery failed: %v", err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("trivial remaining owner pages yielded behind blocked cold claims")
			}
			if f.pages.Load() != int32(owners) || f.ends.Load() != int32(owners) {
				t.Fatalf("published without every real owner page/End: pages=%d ends=%d", f.pages.Load(), f.ends.Load())
			}
			t.Logf("owners=%d pages=%d ends=%d rows=%d string_bytes=%d first_cut=%d second_cut=%d all_owner_completion=true", owners, f.pages.Load(), f.ends.Load(), f.rows.Load(), f.stringBytes.Load(), tc.firstCut, tc.secondCut)
		})
	}
}
