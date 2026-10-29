//go:build integration

package controller_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// The Runtime entrypoint must retain the Service read-only property; testing
// only ReadBarrier would miss an accidentally retained empty-write pre-read.
func TestPresenceGenerationGetDoesNotAppend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		NodeID: 1, Addr: "presence-read-1", StateDir: t.TempDir(),
		ClusterID: "presence-read-only", Role: controller.RuntimeRoleVoter,
		Voters:         []controller.Voter{{NodeID: 1, Addr: "presence-read-1"}},
		AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1,
		TickInterval: 5 * time.Millisecond,
		PresenceCapabilityProbe: func(_ context.Context, id uint64, addr string) (controller.PresenceCapabilities, error) {
			if id != 1 || addr != "presence-read-1" {
				t.Errorf("unexpected capability endpoint %d %q", id, addr)
			}
			return controller.PresenceCapabilities{NodeID: 1, BootID: 101, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1}, nil
		},
	})
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
	before, err := runtime.ControllerRaftStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		cut, err := runtime.GetPresenceGeneration(ctx, 37)
		if err != nil {
			t.Fatal(err)
		}
		if cut.OwnerSet.Epoch != owners.Epoch || cut.Generation.Generation != 0 {
			t.Fatalf("unexpected read cut: %+v", cut)
		}
		after, err := runtime.ControllerRaftStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.CommitIndex != before.CommitIndex || after.AppliedIndex != before.AppliedIndex || after.LastIndex != before.LastIndex {
			t.Fatalf("Runtime Get appended/applied an empty write: before=%+v after=%+v", before, after)
		}
	}
}

// Existing M is a read result only when a fresh committed roster and all exact
// capability responses agree. Initial activation, boot changes, and even a
// joining mirror that does not yet own ingress still require a real new M.
func TestPresenceGenerationUnchangedEnsureDoesNotAppend(t *testing.T) {
	for _, mode := range []string{"unchanged", "boot_change", "joining_roster", "late_old_boot", "canceled_capability"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			boot := uint64(101)
			var hook func(context.Context)
			runtime, err := controller.NewRuntime(controller.RuntimeConfig{
				NodeID: 1, Addr: "presence-ensure-1", StateDir: t.TempDir(), ClusterID: "presence-ensure-read", Role: controller.RuntimeRoleVoter,
				Voters: []controller.Voter{{NodeID: 1, Addr: "presence-ensure-1"}}, AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1, TickInterval: 5 * time.Millisecond,
				PresenceCapabilityProbe: func(ctx context.Context, id uint64, addr string) (controller.PresenceCapabilities, error) {
					observedBoot := boot
					if id == 2 {
						observedBoot = 202
					}
					if f := hook; f != nil {
						hook = nil
						f(ctx)
					}
					return controller.PresenceCapabilities{NodeID: id, BootID: observedBoot, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1}, nil
				},
			})
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
				s, e := runtime.ControllerRaftStatus(ctx)
				if e == nil && s.LeaderID == 1 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-tick.C:
				}
			}
			beforeActivation, err := runtime.ControllerRaftStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			first, err := runtime.EnsurePresenceOwnerSet(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if first.Epoch <= beforeActivation.CommitIndex {
				t.Fatal("initial M lacked a new committed receipt")
			}
			switch mode {
			case "unchanged":
				before, err := runtime.ControllerRaftStatus(ctx)
				if err != nil {
					t.Fatal(err)
				}
				got, err := runtime.EnsurePresenceOwnerSet(ctx)
				if err != nil {
					t.Fatal(err)
				}
				after, err := runtime.ControllerRaftStatus(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if got.Epoch != first.Epoch || after.LastIndex != before.LastIndex || after.CommitIndex != before.CommitIndex || after.AppliedIndex != before.AppliedIndex {
					t.Fatalf("unchanged Ensure appended a noop: M=%d/%d index=%d/%d", first.Epoch, got.Epoch, before.LastIndex, after.LastIndex)
				}
			case "boot_change":
				boot = 303
				got, err := runtime.EnsurePresenceOwnerSet(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if got.Epoch <= first.Epoch || got.Owners[0].CapabilityGeneration != 303 {
					t.Fatalf("new boot did not commit a new M: %+v", got)
				}
			case "joining_roster":
				if _, err := runtime.JoinNode(ctx, controller.JoinNodeRequest{NodeID: 2, Addr: "presence-ensure-2", Roles: []controller.NodeRole{controller.NodeRoleData}}); err != nil {
					t.Fatal(err)
				}
				got, err := runtime.EnsurePresenceOwnerSet(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if got.Epoch <= first.Epoch || len(got.Owners) != len(first.Owners) {
					t.Fatalf("joining mirror requires new M even before ingress ownership: %+v", got)
				}
			case "canceled_capability":
				operation, stop := context.WithCancel(ctx)
				hook = func(context.Context) { stop() }
				_, err := runtime.EnsurePresenceOwnerSet(operation)
				stop()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled capability cut returned success: %v", err)
				}
			case "late_old_boot":
				var newer controller.PresenceOwnerSet
				hook = func(ctx context.Context) {
					boot = 303
					var err error
					newer, err = runtime.EnsurePresenceOwnerSet(ctx)
					if err != nil {
						t.Fatal(err)
					}
				}
				old, oldErr := runtime.EnsurePresenceOwnerSet(ctx)
				current, err := runtime.GetPresenceGeneration(ctx, 37)
				if err != nil {
					t.Fatal(err)
				}
				if newer.Epoch <= first.Epoch || current.OwnerSet.Epoch != newer.Epoch || current.OwnerSet.Owners[0].CapabilityGeneration != 303 {
					t.Fatalf("late proof rolled back committed M: first=%+v newer=%+v current=%+v", first, newer, current)
				}
				if oldErr == nil && old.Epoch != first.Epoch {
					t.Fatalf("old cut invented a later M: %+v", old)
				}
				_, err = runtime.ClaimPresenceGeneration(ctx, controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: first.Epoch, ServingBoot: 101})
				if err == nil {
					t.Fatal("read-only old M granted a later generation")
				}
			}
		})
	}
}
