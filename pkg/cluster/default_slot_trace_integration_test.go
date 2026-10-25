//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// The public Node barrier must inherit the existing RPC diagnostic enablement:
// default-off cannot collect Slot phases, and enabled composition must not lose
// the request context before the real Runtime's fresh quorum/apply fence.
func TestDefaultSlotReadBarrierTracingFollowsRPCDiagnostics(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			node := newDefaultSingleNode(t)
			node.cfg.Slots.HashSlotCount = 256
			if enabled {
				node.cfg.Transport.RPCTraceEvery = 1
			}
			startNodes(t, node)
			t.Cleanup(func() { stopNodes(t, node) })
			waitClusterReady(t, node)
			waitNodeWriteReady(t, node)
			r, _ := rpctrace.NewRecorder(1)
			trace := r.Start(context.Background(), rpctrace.SideServer, 13)
			ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 5*time.Second)
			defer cancel()
			if err := node.ReadSlotBarrier(ctx, node.SlotForKey("trace-wiring")); err != nil {
				t.Fatal(err)
			}
			sample := trace.Finish(rpctrace.ResultOK)
			if enabled {
				if sample.BarrierGroups != 1 || len(sample.Spans) < 5 {
					t.Fatalf("enabled Node lost runtime phases: %+v", sample)
				}
			} else if sample.BarrierGroups != 0 || len(sample.Spans) != 0 {
				t.Fatalf("disabled Node collected phases: %+v", sample)
			}
		})
	}
}
