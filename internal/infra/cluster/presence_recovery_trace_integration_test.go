//go:build integration

package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type recoveryTraceWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func TestPresenceRecoveryTraceKeepsOwnerChildBudgetAndOutcome(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, outcome := range []string{"ok", "deadline", "parent_cancel"} {
			if remote && outcome == "deadline" {
				// The existing presence wire maps a remote child deadline to its
				// retryable authority status, not a context deadline. Do not invent it.
				continue
			}
			t.Run(fmt.Sprintf("remote=%t/%s", remote, outcome), func(t *testing.T) {
				node, target := recoveryTraceNode()
				recorder, err := rpctrace.NewRecorder(1)
				if err != nil {
					t.Fatal(err)
				}
				trace := recorder.Start(context.Background(), rpctrace.SideServer, 13)
				ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), time.Second)
				defer cancel()
				parentDeadline, _ := ctx.Deadline()
				ownerID := uint64(1)
				if remote {
					ownerID = 2
					node.snapshot.Nodes = []control.Node{{NodeID: ownerID, JoinState: control.NodeJoinStateActive}}
				}
				reader := recoveryReaderFunc(func(readCtx context.Context, _ presence.RouteTarget, _ []string) (presence.OwnerRouteSnapshot, error) {
					if rpctrace.From(readCtx) != trace {
						t.Error("owner child lost current observation context")
					}
					if deadline, ok := readCtx.Deadline(); !ok || !deadline.Before(parentDeadline) {
						t.Error("owner child lost its independent shorter budget")
					}
					if outcome == "parent_cancel" {
						cancel()
					}
					if outcome != "ok" {
						<-readCtx.Done()
						return presence.OwnerRouteSnapshot{}, readCtx.Err()
					}
					return presence.OwnerRouteSnapshot{OwnerNodeID: ownerID, OwnerBootID: 11}, nil
				})
				if remote {
					node.rpcByNode = map[uint64]cluster.NodeRPCHandler{ownerID: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}}
				}
				_, err = NewPresenceRecoveryOwners(node, reader).RecoverRoutes(ctx, target, []string{"child-proof"})
				want := rpctrace.ResultOK
				switch outcome {
				case "ok":
					if err != nil {
						t.Fatal("valid owner proof rejected")
					}
				case "deadline":
					want = rpctrace.ResultTimeout
					if !errors.Is(err, authority.ErrRouteNotReady) || ctx.Err() != nil {
						t.Fatal("child expiry changed parent retry contract")
					}
				case "parent_cancel":
					want = rpctrace.ResultCanceled
					if !errors.Is(err, context.Canceled) {
						t.Fatal("parent cancellation was not preserved")
					}
				}
				sample := trace.Finish(want)
				if len(sample.Spans) != 2 || sample.Spans[0].Stage != rpctrace.StageRecoveryOwners || sample.Spans[0].Result != rpctrace.ResultOK || sample.Spans[1].Stage != rpctrace.StageRecoveryOwner || sample.Spans[1].Result != want || sample.Spans[1].Incomplete {
					t.Fatalf("owner child evidence did not preserve its outcome: %+v", sample.Spans)
				}
				if sample.Spans[0].Offset+sample.Spans[0].Duration > sample.Spans[1].Offset {
					t.Fatal("owner I/O was included in owner admission wait")
				}
			})
		}
	}
}

func (c *recoveryTraceWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func recoveryTraceNode() (*recoveryCluster, presence.RouteTarget) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	node := &recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1, route: cluster.Route{HashSlot: 101, SlotID: 5, Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}}}
	node.snapshot.Nodes = []control.Node{{NodeID: 1, JoinState: control.NodeJoinStateActive}}
	return node, target
}

func TestPresenceRecoveryTraceSeparatesCanceledOwnerAdmission(t *testing.T) {
	node, target := recoveryTraceNode()
	entered, release := make(chan struct{}, 16), make(chan struct{})
	var calls atomic.Int32
	reader := recoveryReaderFunc(func(ctx context.Context, _ presence.RouteTarget, _ []string) (presence.OwnerRouteSnapshot, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return presence.OwnerRouteSnapshot{OwnerNodeID: 1, OwnerBootID: 11}, nil
		case <-ctx.Done():
			return presence.OwnerRouteSnapshot{}, ctx.Err()
		}
	})
	source := NewPresenceRecoveryOwners(node, reader)
	groups := []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"owner-proof"}}}
	heldCtx, stopHeld := context.WithCancel(context.Background())
	heldDone := make(chan struct{}, 16)
	t.Cleanup(func() {
		close(release)
		stopHeld()
		for i := 0; i < 16; i++ {
			select {
			case <-heldDone:
			case <-time.After(time.Second):
				t.Error("held owner round did not join")
			}
		}
	})
	for i := 0; i < 16; i++ {
		go func() { source.RecoverRoutesByTargets(heldCtx, groups); heldDone <- struct{}{} }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("owner round did not reach controlled reader")
		}
	}
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), time.Second)
	defer cancel()
	waiting := &recoveryTraceWaitContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan []presence.EndpointLookupResult, 1)
	go func() { done <- source.RecoverRoutesByTargets(waiting, groups) }()
	select {
	case <-waiting.waiting:
	case <-time.After(time.Second):
		t.Fatal("owner admission did not reach cancellable wait")
	}
	cancel()
	select {
	case results := <-done:
		if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
			t.Fatal("owner admission changed cancellation outcome")
		}
	case <-time.After(time.Second):
		t.Fatal("owner admission cancellation did not join")
	}
	if calls.Load() != 16 {
		t.Fatal("canceled owner admission called an owner")
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	if len(sample.Spans) != 1 || sample.Spans[0].Stage != rpctrace.StageRecoveryOwners || sample.Spans[0].Result != rpctrace.ResultCanceled || sample.Spans[0].Incomplete {
		t.Fatalf("owner admission cancellation must not claim owner execution: %+v", sample.Spans)
	}
}
