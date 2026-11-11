//go:build integration

package controller_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// EnsureChannelAuthorityPromise must fail closed: an unreachable or older
// member keeps the cluster in barrier mode, and activation is committed once,
// durably, and never repeated (rollback after activation is unsupported).
func TestEnsureChannelAuthorityPromiseIsProvenDurableAndIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var version atomic.Uint32
	var probeErr atomic.Bool
	var probes atomic.Int32
	cfg := controller.RuntimeConfig{
		NodeID: 1, Addr: "promise-controller-1", StateDir: t.TempDir(),
		ClusterID: "channel-promise-activation", Role: controller.RuntimeRoleVoter,
		Voters:         []controller.Voter{{NodeID: 1, Addr: "promise-controller-1"}},
		AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1,
		TickInterval: 5 * time.Millisecond,
		ChannelPromiseCapabilityProbe: func(_ context.Context, nodeID uint64, addr string) (controller.ChannelReplicationCapability, error) {
			probes.Add(1)
			if nodeID != 1 || addr != "promise-controller-1" {
				t.Errorf("probe target = (%d,%q), want exact member 1", nodeID, addr)
			}
			if probeErr.Load() {
				return controller.ChannelReplicationCapability{}, errors.New("unknown control operation")
			}
			return controller.ChannelReplicationCapability{NodeID: nodeID, ProtocolVersion: version.Load()}, nil
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
			_ = runtime.Stop(stopCtx)
		})
		for {
			if status, err := runtime.ControllerRaftStatus(ctx); err == nil && status.LeaderID == 1 {
				return runtime
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	runtime := start()

	probeErr.Store(true)
	if active, err := runtime.EnsureChannelAuthorityPromise(ctx); err == nil || active {
		t.Fatalf("unreachable member: active=%v err=%v, want inactive error", active, err)
	}
	probeErr.Store(false)
	version.Store(controller.ChannelAuthorityPromiseProtocolVersion - 1)
	if active, err := runtime.EnsureChannelAuthorityPromise(ctx); err == nil || active {
		t.Fatalf("older member: active=%v err=%v, want inactive error", active, err)
	}
	if st, err := runtime.LocalState(ctx); err != nil || st.ChannelAuthorityPromise != nil {
		t.Fatalf("failed ensure wrote activation: %+v %v", st.ChannelAuthorityPromise, err)
	}

	version.Store(controller.ChannelAuthorityPromiseProtocolVersion)
	if active, err := runtime.EnsureChannelAuthorityPromise(ctx); err != nil || !active {
		t.Fatalf("supported cluster: active=%v err=%v", active, err)
	}
	before := probes.Load()
	if active, err := runtime.EnsureChannelAuthorityPromise(ctx); err != nil || !active {
		t.Fatalf("repeat ensure: active=%v err=%v", active, err)
	}
	if probes.Load() != before {
		t.Fatal("an active cluster probed members again")
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := start()
	st, err := restarted.LocalState(ctx)
	if err != nil || st.ChannelAuthorityPromise == nil || st.ChannelAuthorityPromise.ActivatedIndex == 0 {
		t.Fatalf("activation after restart = %+v %v", st.ChannelAuthorityPromise, err)
	}
}
