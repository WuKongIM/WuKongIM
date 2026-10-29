//go:build integration

package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// The capability port changes real committed state after the operation's quorum
// cut. No private mutation or invented receipt is used to create the interleave.
func TestPresenceGenerationSemanticCASPreservesIndependentProgress(t *testing.T) {
	for _, mode := range []string{"ensure_unrelated_revision", "claim_other_slot_generation", "health_report_control", "participant_join_rejected"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var runtime *controller.Runtime
			var hook func(context.Context)
			cfg := controller.RuntimeConfig{NodeID: 1, Addr: "semantic-presence-1", StateDir: t.TempDir(), ClusterID: "semantic-presence", Role: controller.RuntimeRoleVoter, Voters: []controller.Voter{{NodeID: 1, Addr: "semantic-presence-1"}}, AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1, TickInterval: 5 * time.Millisecond,
				PresenceCapabilityProbe: func(ctx context.Context, id uint64, addr string) (controller.PresenceCapabilities, error) {
					if f := hook; f != nil {
						hook = nil
						f(ctx)
					}
					return controller.PresenceCapabilities{NodeID: id, BootID: 100 + id, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1}, nil
				},
			}
			var err error
			runtime, err = controller.NewRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = runtime.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				stop, done := context.WithTimeout(context.Background(), time.Second)
				defer done()
				if err := runtime.Stop(stop); err != nil {
					t.Error(err)
				}
			})
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				st, e := runtime.ControllerRaftStatus(ctx)
				if e == nil && st.LeaderID == 1 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-tick.C:
				}
			}
			owners, err := runtime.EnsurePresenceOwnerSet(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request := controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: owners.Epoch, ServingBoot: 101}
			var changed bool
			switch mode {
			case "ensure_unrelated_revision":
				hook = func(ctx context.Context) {
					st, e := runtime.LocalState(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if e = runtime.ReplaceOpsMCPState(ctx, st.Revision, controller.OpsMCPState{}); e != nil {
						t.Fatal(e)
					}
					changed = true
				}
				got, e := runtime.EnsurePresenceOwnerSet(ctx)
				if e != nil {
					t.Fatalf("unrelated revision rejected identical owner proof: %v", e)
				}
				if got.Epoch != owners.Epoch {
					t.Fatal("unchanged owners changed M")
				}
			case "claim_other_slot_generation":
				hook = func(ctx context.Context) {
					other := request
					other.Fence.HashSlot = 38
					g, e := runtime.ClaimPresenceGeneration(ctx, other)
					if e != nil {
						t.Fatal(e)
					}
					changed = g.Generation > 0
				}
				g, e := runtime.ClaimPresenceGeneration(ctx, request)
				if e != nil {
					t.Fatalf("other Hash Slot progress rejected independent predecessor: %v", e)
				}
				if g.Fence != request.Fence || g.PreviousGeneration != 0 || g.Generation != g.AppliedRaftIndex {
					t.Fatalf("claim=%+v", g)
				}
			case "health_report_control":
				hook = func(ctx context.Context) {
					_, e := runtime.ReportNodeHealth(ctx, controller.ReportNodeHealthRequest{NodeID: 1, Status: controller.NodeStatusAlive, RuntimeReady: true, ReportSeq: 1})
					if e != nil {
						t.Fatal(e)
					}
					changed = true
				}
				if _, e := runtime.ClaimPresenceGeneration(ctx, request); e != nil {
					t.Fatalf("health report rejected proof: %v", e)
				}
			case "participant_join_rejected":
				hook = func(ctx context.Context) {
					_, e := runtime.JoinNode(ctx, controller.JoinNodeRequest{NodeID: 2, Addr: "semantic-presence-2", Roles: []controller.NodeRole{controller.NodeRoleData}})
					if e != nil {
						t.Fatal(e)
					}
					changed = true
				}
				if _, e := runtime.ClaimPresenceGeneration(ctx, request); e == nil {
					t.Fatal("proof from old participant cut accepted after joining mirror appeared")
				}
			}
			if !changed {
				t.Fatal("controlled committed interleave did not occur")
			}
		})
	}
}
