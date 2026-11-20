//go:build integration

package multiraft_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var controlledApplyWriteError = errors.New("controlled state write failure")

// Failure inventory: uncommitted setup, missing separate queued tasks, control
// timeout, early Future completion, wrong durable values/results, and failed
// cleanup invalidate the resource comparison. The existing public Runtime,
// Storage and FSM ports are the only control/observation boundaries.
func TestRuntimeQueuedCommittedWritesShareDurableApply(t *testing.T) { queuedApplyCase(t, "success") }

// The same controlled queued prefix must retain per-Future cancellation,
// fatal-write and CloseSlot fences; later arrivals cannot join a running batch.
func TestRuntimeQueuedApplyCompletionFences(t *testing.T) {
	for _, mode := range []string{"late_and_cancel", "write_error", "close_slot"} {
		t.Run(mode, func(t *testing.T) { queuedApplyCase(t, mode) })
	}
}

func queuedApplyCase(t *testing.T, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	dir := t.TempDir()
	db := openExternalSnapshotDB(t, filepath.Join(dir, "raft"), filepath.Join(dir, "snapshots"))
	fsm := &queuedApplyDisk{path: filepath.Join(dir, "state"), entered: make(chan struct{}), release: make(chan struct{})}
	if mode != "success" {
		fsm.second = make(chan struct{})
		fsm.releaseSecond = make(chan struct{})
		fsm.failSecond = mode == "write_error"
	}
	observer := &queuedApplyObserver{events: make(chan int, 16)}
	rt, err := multiraft.New(multiraft.Options{NodeID: 1, Workers: 1, TickInterval: 50 * time.Millisecond,
		Transport: externalSnapshotTransport{}, Observer: observer,
		Raft: multiraft.RaftOptions{ElectionTick: 40, HeartbeatTick: 2, PreVote: true, CheckQuorum: true}})
	if err != nil {
		cancel()
		_ = db.Close()
		t.Fatal(err)
	}
	report := struct {
		Complete, Cleanup, ResourceRED, ControlTimedOut bool
		DurableCalls                                    []int
		Error, Mode                                     string
	}{Mode: mode}
	t.Cleanup(func() {
		fsm.unblock()
		fsm.unblockSecond()
		cancel()
		cleanup := errors.Join(rt.Close(), db.Close())
		report.Cleanup = cleanup == nil
		report.ControlTimedOut = fsm.expired.Load()
		fsm.mu.Lock()
		report.DurableCalls = append([]int(nil), fsm.calls...)
		fsm.mu.Unlock()
		if cleanup != nil {
			t.Error(cleanup)
			report.Error = cleanup.Error()
		}
		report.ResourceRED = mode == "success" && report.Complete && report.Cleanup && !report.ControlTimedOut && len(report.DurableCalls) > 2
		encoded, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil {
			t.Error(encodeErr)
			return
		}
		if path := os.Getenv("WK_QUEUED_APPLY_REPORT"); path != "" {
			if mode != "success" {
				path += "." + mode + ".json"
			}
			f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				t.Error(e)
				report.Complete = false
				report.ResourceRED = false
				report.Error = "artifact creation failed"
				encoded, _ = json.Marshal(report)
				t.Log(string(encoded))
				return
			}
			_, e = f.Write(encoded)
			e = errors.Join(e, f.Close())
			if e != nil {
				t.Error(e)
				report.Complete = false
				report.ResourceRED = false
				report.Error = "artifact write failed"
				encoded, _ = json.Marshal(report)
			}
		}
		t.Log(string(encoded))
	})
	const id multiraft.SlotID = 605
	store := db.ForSlot(uint64(id))
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{Slot: multiraft.SlotOptions{ID: id, Storage: store, StateMachine: fsm}, Voters: []multiraft.NodeID{1}, Campaign: true}); err != nil {
		t.Fatal(err)
	}
	waitForExternalSnapshotCondition(t, func() bool { _, ok := coalescedDiskCurrentTermDurable(ctx, rt, id, store, 0); return ok })
	futures := make([]multiraft.Future, 0, 3)
	completed := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		f, e := rt.Propose(ctx, id, externalProposalPayload(0, []byte(fmt.Sprintf("value-%d", i))))
		if e != nil {
			t.Fatal(e)
		}
		futures = append(futures, f)
		cf, ok := f.(multiraft.CompletionFuture)
		if !ok || !cf.ObserveCompletion(queuedApplyCompletion{completed}) {
			t.Fatal("missing completion observer")
		}
		if i == 0 {
			select {
			case <-fsm.entered:
			case <-ctx.Done():
				t.Fatal("first real state write did not enter")
			}
			// Public same-Slot controls finish the owner turn that emitted A's
			// queue observation before the next proposal is admitted.
			for j := 0; j < 2; j++ {
				if _, e := rt.FreshStatus(ctx, id); e != nil {
					t.Fatal(e)
				}
			}
			for len(observer.events) > 0 {
				<-observer.events
			}
		} else {
			for {
				select {
				case depth := <-observer.events:
					if depth == i {
						goto queued
					}
				case <-ctx.Done():
					t.Fatal("separate committed apply task missing")
				}
			}
		}
	queued:
	}
	if fsm.expired.Load() || observer.dropped.Load() {
		t.Fatal("controlled setup invalid")
	}
	select {
	case <-completed:
		t.Fatal("Future completed before durable state write")
	default:
	}

	fsm.unblock()
	if mode != "success" {
		select {
		case <-fsm.second:
		case <-ctx.Done():
			t.Fatal("queued batch did not enter")
		}
		if fsm.secondCount.Load() != 2 {
			t.Fatal("safety setup did not form the two-command batch")
		}
		if _, e := futures[0].Wait(ctx); e != nil {
			t.Fatal(e)
		}
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal("first completion missing")
		}
		select {
		case <-completed:
			t.Fatal("queued Future completed before actual batch write")
		default:
		}
		switch mode {
		case "late_and_cancel":
			canceled, stop := context.WithCancel(ctx)
			stop()
			if _, e := futures[1].Wait(canceled); !errors.Is(e, context.Canceled) {
				t.Fatal("canceled caller did not return cancellation")
			}
			f, e := rt.Propose(ctx, id, externalProposalPayload(0, []byte("value-3")))
			if e != nil {
				t.Fatal(e)
			}
			futures = append(futures, f)
			lateQueued := false
			for !lateQueued {
				select {
				case depth := <-observer.events:
					lateQueued = depth == 1
				case <-ctx.Done():
					t.Fatal("later independent task missing")
				}
			}
		case "close_slot":
			done := make(chan error, 1)
			go func() { done <- rt.CloseSlot(ctx, id) }()
			waitForExternalSnapshotCondition(t, func() bool { _, e := rt.Status(id); return errors.Is(e, multiraft.ErrSlotNotFound) })
			select {
			case <-done:
				t.Fatal("CloseSlot returned before owned write joined")
			default:
			}
			fsm.unblockSecond()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("CloseSlot did not join")
			}
		}
		fsm.unblockSecond()
		if mode == "write_error" || mode == "close_slot" {
			want := controlledApplyWriteError
			if mode == "close_slot" {
				want = multiraft.ErrSlotClosed
			}
			for _, f := range futures[1:] {
				if _, e := f.Wait(ctx); !errors.Is(e, want) {
					t.Fatalf("wrong terminal cause: got %v, want %v", e, want)
				}
			}
			if fsm.expired.Load() {
				t.Fatal("controlled safety window expired")
			}
			report.Complete = true
			return
		}
	}
	var last uint64
	for i, f := range futures {
		r, e := f.Wait(ctx)
		if e != nil || string(r.Data) != fmt.Sprintf("value-%d", i) || (last != 0 && r.Index != last+1) {
			t.Fatalf("Future result/order: %v", e)
		}
		last = r.Index
	}
	if e := rt.ReadBarrier(ctx, id); e != nil {
		t.Fatal(e)
	}
	snap, e := fsm.Snapshot(ctx)
	if e != nil || string(snap.Data) != func() string {
		v := "value-0\nvalue-1\nvalue-2\n"
		if mode == "late_and_cancel" {
			v += "value-3\n"
		}
		return v
	}() {
		t.Fatalf("durable state mismatch: %v", e)
	}
	saved, e := store.InitialState(ctx)
	if e != nil || saved.AppliedIndex != last || saved.HardState.Commit < last {
		t.Fatalf("durable applied boundary: %v", e)
	}
	report.Complete = true
	fsm.mu.Lock()
	calls := append([]int(nil), fsm.calls...)
	fsm.mu.Unlock()
	wantCalls := 2
	if mode == "late_and_cancel" {
		wantCalls = 3
	}
	if len(calls) != wantCalls || calls[0] != 1 || calls[1] != 2 || (wantCalls == 3 && calls[2] != 1) {
		t.Errorf("already-queued committed suffix used durable calls %v, want [1 2]", calls)
	}
}

// queuedApplyDisk durably appends each public FSM batch with one actual Sync.
// Only the first entrance is controlled, with a hard two-second safety bound.
type queuedApplyDisk struct {
	path                  string
	mu                    sync.Mutex
	calls                 []int
	once                  sync.Once
	entered, release      chan struct{}
	expired               atomic.Bool
	second, releaseSecond chan struct{}
	secondOnce            sync.Once
	secondCount           atomic.Int32
	failSecond            bool
}

func (s *queuedApplyDisk) unblock() { s.once.Do(func() { close(s.release) }) }
func (s *queuedApplyDisk) unblockSecond() {
	if s.releaseSecond != nil {
		s.secondOnce.Do(func() { close(s.releaseSecond) })
	}
}
func (s *queuedApplyDisk) Apply(ctx context.Context, cmd multiraft.Command) ([]byte, error) {
	r, e := s.ApplyBatch(ctx, []multiraft.Command{cmd})
	if e != nil {
		return nil, e
	}
	return r[0], nil
}
func (s *queuedApplyDisk) ApplyBatch(ctx context.Context, cmds []multiraft.Command) ([][]byte, error) {
	s.mu.Lock()
	first := len(s.calls) == 0
	second := len(s.calls) == 1 && s.second != nil
	s.mu.Unlock()
	if first {
		close(s.entered)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-s.release:
		case <-timer.C:
			s.expired.Store(true)
			return nil, context.DeadlineExceeded
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if second {
		s.secondCount.Store(int32(len(cmds)))
		close(s.second)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-s.releaseSecond:
		case <-timer.C:
			s.expired.Store(true)
			return nil, context.DeadlineExceeded
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Consume the one controlled entrance before any later task arrives.
		s.second = nil
		if s.failSecond {
			return nil, controlledApplyWriteError
		}
	}
	f, e := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	results := make([][]byte, len(cmds))
	for i, cmd := range cmds {
		results[i] = append([]byte(nil), cmd.Data...)
		if _, e = f.Write(append(append([]byte(nil), cmd.Data...), '\n')); e != nil {
			_ = f.Close()
			return nil, e
		}
	}
	e = errors.Join(f.Sync(), f.Close())
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	s.calls = append(s.calls, len(cmds))
	s.mu.Unlock()
	return results, nil
}
func (s *queuedApplyDisk) Restore(context.Context, multiraft.Snapshot) error {
	return errors.New("unexpected restore in fresh fixture")
}
func (s *queuedApplyDisk) Snapshot(context.Context) (multiraft.Snapshot, error) {
	b, e := os.ReadFile(s.path)
	return multiraft.Snapshot{Data: b}, e
}

type queuedApplyCompletion struct{ completed chan struct{} }

func (o queuedApplyCompletion) ObserveFutureCompletion(multiraft.Result, error) {
	o.completed <- struct{}{}
}

type queuedApplyObserver struct {
	events  chan int
	dropped atomic.Bool
}

func (*queuedApplyObserver) SetSchedulerWorkers(int)                              {}
func (*queuedApplyObserver) SetSchedulerInflight(int)                             {}
func (*queuedApplyObserver) SetSchedulerState(multiraft.SchedulerStateEvent)      {}
func (*queuedApplyObserver) ObserveSchedulerAdmission(string)                     {}
func (*queuedApplyObserver) ObserveSchedulerTask(string, time.Duration)           {}
func (*queuedApplyObserver) ObserveSlotApplyTask(multiraft.SlotID, time.Duration) {}
func (o *queuedApplyObserver) ObserveSlotApplyQueue(_ multiraft.SlotID, depth int) {
	select {
	case o.events <- depth:
	default:
		o.dropped.Store(true)
	}
}
