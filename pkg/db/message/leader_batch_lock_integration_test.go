//go:build integration

package message

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestStoreAppendBatchWaitingCohortReleasesEarlierLocks(t *testing.T) {
	for _, busyLock := range []string{"append", "checkpoint"} {
		for _, cause := range []string{"canceled", "deadline"} {
			t.Run(busyLock+"/"+cause, func(t *testing.T) {
				eng := openCompatEngine(t)
				first, second, items := leaderBatchLockItems(t, eng)
				defer first.Close()
				defer second.Close()
				var busy *sync.Mutex
				if busyLock == "append" {
					busy = &second.log.appendMu
				} else {
					busy = &second.log.checkpointMu
				}
				busy.Lock()
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(busy.Unlock) }
				defer release()
				ctx, cancel := context.WithCancel(context.Background())
				wantErr := context.Canceled
				if cause == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 80*time.Millisecond)
					wantErr = context.DeadlineExceeded
				}
				defer cancel()
				started := make(chan struct{})
				completed := make(chan []AppendBatchResult, 1)
				go func() {
					close(started)
					completed <- StoreAppendBatch(ctx, items)
				}()
				<-started
				// Match the existing real-lease busy-lock integration fixtures.
				// This settles the waiting goroutine, not a product latency bound.
				time.Sleep(20 * time.Millisecond)
				freeWhileBusy := leaderBatchEarlierLocksAvailable(first.log.channelEntry)
				pinsWhileBusy := eng.db.registry.snapshot().backgroundPins
				if cause == "deadline" {
					<-ctx.Done()
				} else {
					cancel()
				}
				var results []AppendBatchResult
				returnedWhileBusy := false
				select {
				case results = <-completed:
					returnedWhileBusy = true
				case <-time.After(time.Second):
				}
				// Always release and join the original owner before reporting RED.
				release()
				if !returnedWhileBusy {
					select {
					case results = <-completed:
					case <-time.After(2 * time.Second):
						t.Fatal("append owner did not join after the busy lock was released")
					}
				}
				if !freeWhileBusy {
					t.Error("waiting leader cohort retained an earlier unrelated append/checkpoint lock")
				}
				if !returnedWhileBusy {
					t.Error("pre-admission context end did not return while the later lock remained busy")
				}
				if pinsWhileBusy != 0 || eng.db.registry.snapshot().backgroundPins != 0 {
					t.Errorf("pre-admission background pins = %d then %d, want zero", pinsWhileBusy, eng.db.registry.snapshot().backgroundPins)
				}
				if len(results) != len(items) {
					t.Fatalf("results length = %d, want %d", len(results), len(items))
				}
				for index, result := range results {
					if !errors.Is(result.Err, wantErr) || result.Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten || result.BaseOffset != 0 || result.LastOffset != 0 {
						t.Errorf("result[%d] = %+v, want aligned context failure without submission", index, result)
					}
				}
				for _, store := range []*ChannelStore{first, second} {
					if !leaderBatchEarlierLocksAvailable(store.log.channelEntry) {
						t.Error("canceled batch retained a canonical lock after joining")
					}
					if leo, err := store.LEOWithError(); err != nil || leo != 0 {
						t.Errorf("canceled LEO = %d, %v, want zero", leo, err)
					}
					rows, err := store.Read(0, 1<<20)
					if err != nil || len(rows) != 0 {
						t.Errorf("canceled rows = %d, %v, want none", len(rows), err)
					}
				}
			})
		}
	}
}

func TestStoreAppendBatchBusyLockReleasePreservesAlignedExactResults(t *testing.T) {
	for _, busyLock := range []string{"append", "checkpoint"} {
		t.Run(busyLock, func(t *testing.T) {
			eng := openCompatEngine(t)
			first, second, items := leaderBatchLockItems(t, eng)
			defer first.Close()
			defer second.Close()
			var busy *sync.Mutex
			if busyLock == "append" {
				busy = &second.log.appendMu
			} else {
				busy = &second.log.checkpointMu
			}
			busy.Lock()
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(busy.Unlock) }
			defer release()
			completed := make(chan []AppendBatchResult, 1)
			go func() { completed <- StoreAppendBatch(context.Background(), items) }()
			time.Sleep(20 * time.Millisecond)
			release()
			var results []AppendBatchResult
			select {
			case results = <-completed:
			case <-time.After(2 * time.Second):
				t.Fatal("released leader batch did not join")
			}
			if len(results) != 3 {
				t.Fatalf("results length = %d, want 3", len(results))
			}
			for index, result := range results {
				want := quorumlog.AppendOutcomeDurable
				if index == 2 {
					want = quorumlog.AppendOutcomeAlreadyDurable
				}
				if result.Err != nil || result.Outcome != want || result.BaseOffset != 0 || result.LastOffset != 1 {
					t.Errorf("result[%d] = %+v, want aligned exact outcome %v", index, result, want)
				}
			}
			for index, store := range []*ChannelStore{second, first} {
				rows, err := store.Read(0, 1<<20)
				if err != nil || len(rows) != 1 || rows[0].ID != items[index].Records[0].ID {
					t.Errorf("aligned rows[%d] = %+v, %v", index, rows, err)
				}
				checkpoint, err := store.LoadCheckpoint()
				if err != nil || checkpoint.HW != 1 {
					t.Errorf("checkpoint[%d] = %+v, %v, want HW 1", index, checkpoint, err)
				}
				proposal, found, err := store.LoadDurableProposal(context.Background(), items[index].Proposal.CommandID, 16, 1<<20)
				if err != nil || !found || proposal.Manifest != items[index].Proposal {
					t.Errorf("durable proposal[%d] = %+v, %v, %v", index, proposal, found, err)
				}
			}
			for index, result := range StoreAppendBatch(context.Background(), items) {
				if result.Err != nil || result.Outcome != quorumlog.AppendOutcomeAlreadyDurable || result.BaseOffset != 0 || result.LastOffset != 1 {
					t.Errorf("exact retry[%d] = %+v, want already durable", index, result)
				}
			}
		})
	}
}

// leaderBatchEarlierLocksAvailable probes only fixture-owned canonical locks.
// Its bounded retries tolerate the no-hold helper's brief acquisition attempt.
func leaderBatchEarlierLocksAvailable(entry *channelEntry) bool {
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		if entry.appendMu.TryLock() {
			if entry.checkpointMu.TryLock() {
				entry.checkpointMu.Unlock()
				entry.appendMu.Unlock()
				return true
			}
			entry.appendMu.Unlock()
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

func leaderBatchLockItems(t *testing.T, eng *Engine) (*ChannelStore, *ChannelStore, []AppendBatchItem) {
	t.Helper()
	first := mustForChannel(t, eng, "leader-lock-a:1", channel.ChannelID{ID: "leader-lock-a", Type: 1})
	second := mustForChannel(t, eng, "leader-lock-b:1", channel.ChannelID{ID: "leader-lock-b", Type: 1})
	stores := []*ChannelStore{second, first}
	items := make([]AppendBatchItem, 0, 3)
	for index, store := range stores {
		records := []channel.Record{compatExactTestRecord(t, 5, uint64(9900+index), store.id.ID, "leader-lock-client")}
		manifest := sealCompatProposalManifest(t, DurableProposalManifest{
			Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
			CommandID: [32]byte{byte(index + 1)}, BaseOffset: 0, LastOffset: 1,
		}, records)
		items = append(items, AppendBatchItem{Store: store, Records: records, Committed: 1,
			ExactBaseOffset: true, Proposal: manifest, ServerAllocatedMessageIDs: true})
	}
	// Reverse key order and an exact duplicate test result alignment independently
	// of the canonical lock order and the physical per-Channel write set.
	items = append(items, items[0])
	return first, second, items
}
