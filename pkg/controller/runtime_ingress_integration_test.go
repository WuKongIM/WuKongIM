//go:build integration

package controller

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"go.etcd.io/raft/v3/raftpb"
)

// Ingress may arrive before Start, while durable Raft state opens, and across
// replacement after Stop. It must not read partially published resources,
// including the FSM pointers used by an already published sync endpoint.
func TestRuntimeIngressDuringVoterStartAndRestart(t *testing.T) {
	r, err := NewRuntime(RuntimeConfig{
		NodeID: 1, Addr: "n1", StateDir: t.TempDir(), ClusterID: "ingress-restart",
		Voters:         []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}},
		AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	var wg sync.WaitGroup
	var requests atomic.Uint64
	failures := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		if err := r.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				stepCtx, stepCancel := context.WithTimeout(ctx, 250*time.Millisecond)
				err := r.Step(stepCtx, raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: 1})
				stepCancel()
				if err != nil && !errors.Is(err, controllerraft.ErrNotStarted) && !errors.Is(err, controllerraft.ErrStopped) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					select {
					case failures <- err:
					default:
					}
					return
				}
				_, err = r.GetState(ctx, GetStateRequest{ClusterID: "ingress-restart"})
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					select {
					case failures <- err:
					default:
					}
					return
				}
				_ = r.LeaderID()
				// Task-result and control-write RPC also remain registered during
				// startup. With no quorum, these requests must fail closed; race
				// instrumentation verifies their service-pointer reads too.
				writeCtx, writeCancel := context.WithTimeout(ctx, 250*time.Millisecond)
				taskErr := r.CompleteTask(writeCtx, TaskResult{TaskID: "startup-task", SlotID: 1, TaskKind: TaskKindBootstrap, ConfigEpoch: 1, FinishedAt: time.Now().UTC()})
				_, healthErr := r.ReportNodeHealth(writeCtx, ReportNodeHealthRequest{NodeID: 1, Status: NodeStatusAlive, ReportSeq: 1})
				writeCancel()
				if taskErr == nil || healthErr == nil {
					select {
					case failures <- fmt.Errorf("write succeeded without Controller quorum: task=%v health=%v", taskErr, healthErr):
					default:
					}
					return
				}
				requests.Add(1)
				runtime.Gosched()
			}
		}()
	}
	for cycle := 1; cycle <= 8; cycle++ {
		if err := r.Start(ctx); err != nil {
			t.Fatalf("cycle %d Start: %v", cycle, err)
		}
		if _, err := r.ControllerRaftStatus(ctx); err != nil {
			t.Fatalf("cycle %d status: %v", cycle, err)
		}
		if err := r.Stop(ctx); err != nil {
			t.Fatalf("cycle %d Stop: %v", cycle, err)
		}
	}
	cancel()
	wg.Wait()
	select {
	case err := <-failures:
		t.Fatalf("ingress: %v", err)
	default:
	}
	if requests.Load() == 0 {
		t.Fatal("no ingress requests completed during lifecycle transitions")
	}
	t.Logf("voter restart cycles=8 ingress requests=%d hash_slots=256", requests.Load())
}
