//go:build integration

package controller_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// This exercises the public durable seam through real Controller Raft. The
// capability provider is a controlled external port, not wire capability proof.
func TestPresenceGenerationCommitsCASAcrossControllerRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := controller.RuntimeConfig{
		NodeID: 1, Addr: "presence-controller-1", StateDir: t.TempDir(),
		ClusterID: "presence-generation-cas-restart", Role: controller.RuntimeRoleVoter,
		Voters:         []controller.Voter{{NodeID: 1, Addr: "presence-controller-1"}},
		AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1,
		TickInterval: 5 * time.Millisecond,
		PresenceCapabilityProbe: func(_ context.Context, nodeID uint64, addr string) (controller.PresenceCapabilities, error) {
			if nodeID != 1 || addr != "presence-controller-1" {
				t.Errorf("capability target = (%d,%q), want exact member 1", nodeID, addr)
				return controller.PresenceCapabilities{}, errors.New("unexpected capability target")
			}
			return controller.PresenceCapabilities{
				NodeID: 1, BootID: 101, ControllerStateVersion: 2,
				ControllerCommandVersion: 2, PresenceProtocolVersion: 1,
			}, nil
		},
	}
	start := func() *controller.Runtime {
		t.Helper()
		runtime, err := controller.NewRuntime(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			defer stopCancel()
			if err := runtime.Stop(stopCtx); err != nil {
				t.Errorf("Stop() error = %v", err)
			}
		})

		// Restored Start preserves the existing contract: leadership may still
		// be electing. Wait for actual public Raft status before control RPCs.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			status, err := runtime.ControllerRaftStatus(ctx)
			if err == nil && status.LeaderID == 1 {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
		return runtime
	}
	runtime := start()
	owners, err := runtime.EnsurePresenceOwnerSet(ctx)
	if err != nil {
		t.Fatalf("EnsurePresenceOwnerSet() error = %v", err)
	}
	if owners.Epoch == 0 || len(owners.Owners) != 1 || owners.Owners[0].NodeID != 1 || owners.Owners[0].CapabilityGeneration != 101 {
		t.Fatalf("owner set = %+v, want one committed exact owner", owners)
	}
	request := controller.ClaimPresenceGenerationRequest{
		Fence:         controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 1},
		OwnerSetEpoch: owners.Epoch, ServingBoot: 101,
	}
	first, err := runtime.ClaimPresenceGeneration(ctx, request)
	if err != nil {
		t.Fatalf("first claim error = %v", err)
	}
	if first.Generation == 0 || first.Generation != first.AppliedRaftIndex || first.PreviousGeneration != 0 || first.Fence != request.Fence || first.OwnerSetEpoch != owners.Epoch || first.ServingBoot != 101 {
		t.Fatalf("first claim = %+v, want exact committed apply receipt", first)
	}
	retry, err := runtime.ClaimPresenceGeneration(ctx, request)
	if err != nil || retry != first {
		t.Fatalf("exact retry = (%+v,%v), want original receipt %+v", retry, err, first)
	}
	secondRequest := request
	secondRequest.ExpectedGeneration = first.Generation
	second, err := runtime.ClaimPresenceGeneration(ctx, secondRequest)
	if err != nil {
		t.Fatalf("same-fence successor error = %v", err)
	}
	if second.Generation <= first.Generation || second.Generation != second.AppliedRaftIndex || second.PreviousGeneration != first.Generation || second.Fence != first.Fence || second.ServingBoot != first.ServingBoot {
		t.Fatalf("same-fence successor = %+v, first = %+v", second, first)
	}
	if _, err := runtime.ClaimPresenceGeneration(ctx, request); !errors.Is(err, controller.ErrPresenceGenerationConflict) {
		t.Fatalf("obsolete predecessor error = %v, want generation conflict", err)
	}
	before, err := runtime.GetPresenceGeneration(ctx, 37)
	if err != nil || before.Generation != second || !reflect.DeepEqual(before.OwnerSet, owners) {
		t.Fatalf("Get before restart = (%+v,%v), want second receipt and unchanged M", before, err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatalf("Stop before restart: %v", err)
	}
	restarted := start()
	after, err := restarted.GetPresenceGeneration(ctx, 37)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("Get after restart = (%+v,%v), want persisted view %+v", after, err, before)
	}
	thirdRequest := request
	thirdRequest.ExpectedGeneration = second.Generation
	third, err := restarted.ClaimPresenceGeneration(ctx, thirdRequest)
	if err != nil || third.Generation <= second.Generation || third.Generation != third.AppliedRaftIndex || third.PreviousGeneration != second.Generation || third.OwnerSetEpoch != owners.Epoch {
		t.Fatalf("claim after restart = (%+v,%v), second = %+v", third, err, second)
	}
}
