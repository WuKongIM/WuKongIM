//go:build integration

package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A selected control batch must retain terminal Future ownership between queue
// removal and RawNode submission. The existing public observer seams hold that
// interval while Close or a physical Save failure fences the Slot. A queued
// configuration follows the held proposal in the same selected batch.
func TestRuntimeStorageSelectedControlBatchRetainsTerminalFutures(t *testing.T) {
	for _, mode := range []string{"runtime_close", "close_slot", "fatal_save"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			store := newStorageFenceGate()
			scheduler := newBlockingSchedulerInflightObserver()
			var releaseSchedulerOnce sync.Once
			releaseScheduler := func() { releaseSchedulerOnce.Do(func() { close(scheduler.release) }) }
			owner := &storageControlStageGate{entered: make(chan struct{}), release: make(chan struct{})}
			rt, err := New(Options{
				NodeID: 1, Workers: 1, TickInterval: time.Hour,
				Transport: &internalFakeTransport{}, Observer: scheduler,
				Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1},
			})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			var closeDone chan struct{}
			var closeErr error
			t.Cleanup(func() {
				// Observer callbacks run on the owner. Release every test gate
				// before joining workers, including when a RED assertion aborts.
				owner.unblock()
				releaseScheduler()
				store.release()
				if closeDone != nil {
					select {
					case <-closeDone:
					case <-ctx.Done():
						t.Error("terminal operation did not join after all fixture gates released")
					}
				}
				if err := rt.Close(); err != nil {
					t.Error(err)
				}
				cancel()
			})
			const id SlotID = 604
			if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
				Slot:   SlotOptions{ID: id, Storage: store, StateMachine: &internalFakeStateMachine{}},
				Voters: []NodeID{1},
			}); err != nil {
				t.Fatal(err)
			}
			var baseline Status
			storageAsyncWait(t, ctx, func() bool {
				s, err := rt.Status(id)
				if err != nil || s.Role != RoleLeader || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex {
					return false
				}
				baseline = s
				return true
			})
			if err := rt.ReadBarrier(ctx, id); err != nil {
				t.Fatal(err)
			}
			store.target.Store(baseline.CommitIndex + 1)
			store.armed.Store(true)
			held, err := rt.Propose(ctx, id, proposalString("terminal-fence-witness"))
			if err != nil {
				t.Fatal(err)
			}
			storageFenceEntered(t, ctx, store.entered)
			if _, err := rt.FreshStatus(ctx, id); err != nil {
				t.Fatal(err)
			}

			// Hold owner dispatch so both controls are present before the
			// batch is taken. The proposal's public stage then holds the
			// selected batch before either control enters RawNode.
			scheduler.Arm()
			proposal, err := rt.Propose(WithProposalStageObserver(ctx, owner), id, proposalString("selected-terminal-proposal"))
			if err != nil {
				t.Fatal(err)
			}
			storageFenceEntered(t, ctx, scheduler.blocked)
			config, err := rt.ChangeConfig(ctx, id, ConfigChange{Type: AddLearner, NodeID: 2})
			if err != nil {
				t.Fatal(err)
			}
			releaseScheduler()
			storageFenceEntered(t, ctx, owner.entered)

			expected := ErrSlotClosed
			switch mode {
			case "runtime_close", "close_slot":
				if mode == "runtime_close" {
					expected = ErrRuntimeClosed
				}
				closeDone = make(chan struct{})
				go func() {
					if mode == "runtime_close" {
						closeErr = rt.Close()
					} else {
						closeErr = rt.CloseSlot(ctx, id)
					}
					close(closeDone)
				}()
			case "fatal_save":
				expected = errors.New("selected-control physical Save failed")
				store.internalFakeStorage.mu.Lock()
				store.internalFakeStorage.saveErr = expected
				store.internalFakeStorage.mu.Unlock()
				store.release()
			}
			if _, err := held.Wait(ctx); !errors.Is(err, expected) {
				t.Fatalf("existing held Future did not prove terminal fence: got=%v want=%v", err, expected)
			}
			if closeDone != nil {
				select {
				case <-closeDone:
					t.Fatal("close returned before held owner and physical Save joined")
				default:
				}
			}
			owner.unblock()
			store.release()
			if closeDone != nil {
				select {
				case <-closeDone:
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				case <-ctx.Done():
					t.Fatal("terminal operation failed to join released owner and storage")
				}
			}
			for _, selected := range []struct {
				name   string
				future Future
			}{{"proposal", proposal}, {"config", config}} {
				bound, stop := context.WithTimeout(ctx, 250*time.Millisecond)
				_, err := selected.future.Wait(bound)
				stop()
				if !errors.Is(err, expected) {
					t.Errorf("selected %s Future lost terminal ownership: got=%v want=%v", selected.name, err, expected)
					continue
				}
				if _, err := selected.future.Wait(ctx); !errors.Is(err, expected) {
					t.Errorf("selected %s Future changed after late owner completion: %v", selected.name, err)
				}
			}
			state, err := store.InitialState(ctx)
			if err != nil || state.AppliedIndex != baseline.AppliedIndex || state.HardState.Commit != baseline.CommitIndex {
				t.Fatalf("selected controls advanced durable state after terminal fence: applied=%d commit=%d err=%v", state.AppliedIndex, state.HardState.Commit, err)
			}
		})
	}
}

// storageControlStageGate blocks only the public pre-RawNode proposal stage.
// Its release is idempotent so failed assertions cannot strand Runtime.Close.
type storageControlStageGate struct {
	entered, release chan struct{}
	enterOnce        sync.Once
	releaseOnce      sync.Once
}

func (g *storageControlStageGate) ObserveProposalStage(stage string, _ string, _ time.Duration) {
	if stage == "meta_create_slot_control_wait" {
		g.enterOnce.Do(func() { close(g.entered) })
		<-g.release
	}
}

func (g *storageControlStageGate) unblock() {
	g.releaseOnce.Do(func() { close(g.release) })
}
