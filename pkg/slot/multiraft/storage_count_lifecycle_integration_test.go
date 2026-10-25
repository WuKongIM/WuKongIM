//go:build integration

package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The count pause owns no additional Ready payload. Cancellation, fatal Save,
// close and reopen must preserve existing Future classes and join physical IO;
// a new generation may reuse the Slot only after the held Save has returned.
func TestRuntimeStorageCountBackpressureLifecycle(t *testing.T) {
	for _, mode := range []string{"close_slot", "reload_slot", "fatal"} {
		t.Run(mode, func(t *testing.T) {
			gate := newStorageFenceGate()
			const id SlotID = 603
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			observer := newBlockingSchedulerInflightObserver()
			var releaseOnce sync.Once
			releaseOwner := func() { releaseOnce.Do(func() { close(observer.release) }) }
			rt, err := New(Options{NodeID: 1, Workers: 1, TickInterval: 10 * time.Millisecond, Transport: &internalFakeTransport{}, Observer: observer, Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, MaxApplyingTasks: 1}})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				releaseOwner()
				gate.release()
				cancel()
				if e := rt.Close(); e != nil {
					t.Error(e)
				}
			})
			if err = rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: gate, StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1}}); err != nil {
				t.Fatal(err)
			}
			storageAsyncWait(t, ctx, func() bool {
				s, e := rt.Status(id)
				return e == nil && s.Role == RoleLeader && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
			})
			if err = rt.ReadBarrier(ctx, id); err != nil {
				t.Fatal(err)
			}
			if compact, e := rt.CompactLog(ctx, id); e != nil || !compact.Compacted {
				t.Fatalf("recoverable baseline snapshot: compacted=%v err=%v", compact.Compacted, e)
			}
			baseline, e := rt.Status(id)
			if e != nil {
				t.Fatal(e)
			}
			ownerObservationArm(t, rt, id, gate)
			first, e := rt.Propose(ctx, id, proposalString("held-count-credit"))
			if e != nil {
				t.Fatal(e)
			}
			storageFenceEntered(t, ctx, gate.entered)
			observer.Arm()
			second, e := rt.Propose(ctx, id, proposalString("queued-at-count-boundary"))
			if e != nil {
				t.Fatal(e)
			}
			storageFenceEntered(t, ctx, observer.blocked)
			expected := ErrSlotClosed
			done := make(chan error, 1)
			if mode == "fatal" {
				expected = errors.New("controlled count-boundary Save failure")
				gate.internalFakeStorage.mu.Lock()
				gate.internalFakeStorage.saveErr = expected
				gate.internalFakeStorage.mu.Unlock()
				gate.release()
			} else {
				go func() {
					if mode == "close_slot" {
						done <- rt.CloseSlot(ctx, id)
					} else {
						done <- rt.ReloadSlot(ctx, id)
					}
				}()
			}
			for i, f := range []Future{first, second} {
				probe, stop := context.WithTimeout(ctx, 200*time.Millisecond)
				_, e = f.Wait(probe)
				stop()
				if !errors.Is(e, expected) {
					t.Fatalf("terminal queued Future %d: got=%v want=%v", i, e, expected)
				}
				if _, e = f.Wait(ctx); !errors.Is(e, expected) {
					t.Fatalf("count-boundary Future class: got=%v want=%v", e, expected)
				}
			}
			if mode != "fatal" {
				select {
				case e := <-done:
					t.Fatalf("lifecycle returned before held physical Save: %v", e)
				default:
				}
				gate.release()
				releaseOwner()
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-ctx.Done():
					t.Fatal("lifecycle failed to join physical Save")
				}
				if mode == "close_slot" {
					if e = rt.OpenSlot(ctx, SlotOptions{ID: id, Storage: gate, StateMachine: &internalFakeStateMachine{}}); e != nil {
						t.Fatal(e)
					}
				}
				storageAsyncWait(t, ctx, func() bool {
					s, e := rt.Status(id)
					return e == nil && s.Role == RoleLeader && s.AppliedIndex == s.CommitIndex
				})
				f, e := rt.Propose(ctx, id, proposalString("new-generation"))
				if e != nil {
					t.Fatal(e)
				}
				r, e := f.Wait(ctx)
				if e != nil || string(r.Data) != "ok:new-generation" {
					t.Fatalf("new generation result index=%d err=%v", r.Index, e)
				}
				if e = rt.ReadBarrier(ctx, id); e != nil {
					t.Fatal(e)
				}
			} else {
				releaseOwner()
				state, e := gate.InitialState(ctx)
				if e != nil || state.AppliedIndex != baseline.AppliedIndex {
					t.Fatalf("failed write advanced durable apply=%d err=%v", state.AppliedIndex, e)
				}
				if _, e = rt.Propose(ctx, id, proposalString("after-fatal")); !errors.Is(e, expected) {
					t.Fatalf("fatal admission lost cause: %v", e)
				}
			}
			for _, f := range []Future{first, second} {
				if _, e = f.Wait(context.Background()); !errors.Is(e, expected) {
					t.Fatalf("late completion rewrote Future: %v", e)
				}
			}
		})
	}
}
