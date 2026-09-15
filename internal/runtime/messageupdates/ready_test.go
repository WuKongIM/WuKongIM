package messageupdates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Drive one scheduling wave and join its bounded dispatch lanes.
func readyWorker(d Dispatcher) *Worker {
	return &Worker{dispatcher: d, runContext: context.Background(), ready: make(chan updateKey, readyCapacity),
		pending: make(map[updateKey]readyEntry), wake: make(chan struct{}, 1)}
}

func readyTask(id uint64) metadb.MessageUpdate {
	return metadb.MessageUpdate{ChannelID: "cold-channel", ChannelType: 2, MessageID: id, MessageSeq: id, Version: 1}
}

func TestCommittedWorkDispatchesWithoutScanningColdSlots(t *testing.T) {
	d := &dispatchStub{}
	w := readyWorker(d) // No Source: known work must never need a discovery scan.
	w.NotifyCommitted(readyTask(1))
	if len(w.wake) != 1 || w.dispatchReady(context.Background()) != 0 {
		t.Fatal("commit did not wake a successful direct dispatch")
	}
	if len(d.ids) != 1 || d.ids[0] != 1 || len(w.pending) != 0 {
		t.Fatalf("dispatched=%v pending=%d", d.ids, len(w.pending))
	}
}

func TestReadyQueueBoundsCoalescesAndDropsPayloads(t *testing.T) {
	w := readyWorker(&dispatchStub{})
	for id := uint64(1); id <= readyCapacity+10; id++ {
		task := readyTask(id)
		task.Payload = []byte("must not retain")
		task.PendingAfterUID = "must not retain"
		w.NotifyCommitted(task)
	}
	newer := readyTask(1)
	newer.Version = 3
	w.NotifyCommitted(newer)
	w.NotifyCommitted(readyTask(1))
	if len(w.pending) != readyCapacity || len(w.ready) != readyCapacity || len(w.wake) != 1 {
		t.Fatal("queue or wake budget exceeded")
	}
	first, ok := w.takeReady()
	if !ok || first.Version != 3 || first.MessageID != 1 {
		t.Fatalf("coalescing lost newest version: %+v", first)
	}
	for _, task := range w.pending {
		if len(task.Payload) != 0 || task.PendingAfterUID != "" {
			t.Fatal("queue retained content or subscriber cursor")
		}
	}
	oversized := readyTask(readyCapacity + 1)
	oversized.ChannelID = strings.Repeat("x", 1025)
	w.NotifyCommitted(oversized)
	if len(w.pending) != readyCapacity-1 {
		t.Fatal("oversized identity retained")
	}
}

func TestReadyWaveYieldsAndFailedTargetsFallBackToRepair(t *testing.T) {
	d := &dispatchStub{more: true}
	w := readyWorker(d)
	w.NotifyCommitted(readyTask(1))
	w.NotifyCommitted(readyTask(2))
	w.dispatchReady(context.Background())
	counts := map[uint64]int{}
	for _, id := range d.ids {
		counts[id]++
	}
	if len(d.ids) != 32 || counts[1] != 16 || counts[2] != 16 || len(w.pending) != 2 {
		t.Fatalf("unfair or unbounded wave: ids=%v pending=%d", d.ids, len(w.pending))
	}
	d.err = errors.New("retry through durable source")
	if failures := w.dispatchReady(context.Background()); failures != 2 || len(w.pending) != 0 || len(d.ids) != 34 {
		t.Fatalf("failed work spun immediately: failures=%d calls=%d", failures, len(d.ids))
	}
}

func TestReadyQueueDoesNotAcceptStoppedOrCancelledWork(t *testing.T) {
	w := New(nil, nil, nil, nil)
	w.NotifyCommitted(readyTask(1))
	if len(w.pending) != 0 {
		t.Fatal("stopped worker retained work")
	}
	w = readyWorker(&dispatchStub{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.runContext = ctx
	w.NotifyCommitted(readyTask(1))
	if len(w.pending) != 0 {
		t.Fatal("stopping worker retained work")
	}
}

func BenchmarkCommittedCoalescing(b *testing.B) {
	w := readyWorker(nil)
	task := readyTask(1)
	w.NotifyCommitted(task)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		task.Version++
		w.NotifyCommitted(task)
	}
}

func TestReadyWakeSurvivesCoincidentRepairTick(t *testing.T) {
	wake := make(chan struct{}, 1)
	ticks := make(chan time.Time, 1)
	wake <- struct{}{}
	ticks <- time.Time{}
	repair, ok := nextWorkerTurn(context.Background(), ticks, wake, false)
	if !ok || !repair || len(wake) != 1 {
		t.Fatal("repair consumed the only commit wake")
	}
	// Even if that scan took a full tick, known work gets a turn before another scan.
	ticks <- time.Time{}
	repair, ok = nextWorkerTurn(context.Background(), ticks, wake, true)
	if !ok || repair || len(ticks) != 1 {
		t.Fatal("slow scans starved committed work")
	}
	wake <- struct{}{}
	repair, ok = nextWorkerTurn(context.Background(), ticks, wake, false)
	if !ok || !repair || len(wake) != 1 {
		t.Fatal("ready work starved due repair")
	}
}

func TestReadyDependencyDeadlineFallsBackWithoutBudgetRetry(t *testing.T) {
	d := &dispatchStub{err: context.DeadlineExceeded}
	w := readyWorker(d)
	w.NotifyCommitted(readyTask(1))
	if w.dispatchReady(context.Background()) != 1 || len(w.pending) != 0 || len(d.ids) != 1 {
		t.Fatal("dependency deadline retried despite a live scheduling budget")
	}
}

func TestReadyBudgetRetryCannotReplaceNewerCommit(t *testing.T) {
	w := readyWorker(&dispatchStub{})
	old := readyTask(1)
	newer := old
	newer.Version++
	w.NotifyCommitted(newer)
	w.enqueueReady(old, true)
	entry := w.pending[updateKey{newer.ChannelID, newer.ChannelType, newer.MessageID}]
	if entry.Version != newer.Version || entry.budgetRetried {
		t.Fatal("old budget retry replaced a fresh committed version")
	}
	w.enqueueReady(newer, true) // A duplicate must not rewrite an existing queue entry.
	if w.pending[updateKey{newer.ChannelID, newer.ChannelType, newer.MessageID}].budgetRetried {
		t.Fatal("duplicate reset the queued version's retry state")
	}
}

func TestReadyPartialPagesPreserveSpentBudgetRetry(t *testing.T) {
	d := &dispatchStub{more: true}
	w := readyWorker(d)
	w.enqueueReady(readyTask(1), true)
	w.NotifyCommitted(readyTask(1)) // A duplicate cannot renew a spent retry.
	w.dispatchReady(context.Background())
	if len(w.pending) != 1 || len(d.ids) != 32 {
		t.Fatalf("paging lost its bounded continuation: pending=%d calls=%d", len(w.pending), len(d.ids))
	}
	for _, entry := range w.pending {
		if !entry.budgetRetried {
			t.Fatal("partial paging renewed the spent budget retry")
		}
	}
	newer := readyTask(1)
	newer.Version++
	w.NotifyCommitted(newer)
	for _, entry := range w.pending {
		if entry.Version != newer.Version || entry.budgetRetried {
			t.Fatal("new commit inherited old retry state")
		}
	}
}
