package cluster

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

const testControllerLeaderWaitTimeout = 25 * time.Millisecond

func TestRetryControllerCommandUsesClusterLeaderWaitTimeoutOverride(t *testing.T) {
	cluster := &Cluster{
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
		},
	}

	var observedRemaining time.Duration
	err := cluster.retryControllerCommand(context.Background(), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("retryControllerCommand() context has no deadline")
		}
		observedRemaining = time.Until(deadline)
		return nil
	})
	if err != nil {
		t.Fatalf("retryControllerCommand() error = %v", err)
	}
	if observedRemaining > 200*time.Millisecond {
		t.Fatalf("retryControllerCommand() deadline remaining = %v, want <= %v", observedRemaining, 200*time.Millisecond)
	}
}

func TestRetryControllerCommandUsesScaledRetryInterval(t *testing.T) {
	cluster := &Cluster{
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: 25 * time.Millisecond,
		},
	}

	attempts := 0
	err := cluster.retryControllerCommand(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 2 {
			return ErrNotLeader
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryControllerCommand() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("retryControllerCommand() attempts = %d, want 2", attempts)
	}
}

func TestRecoverSlotStrictUsesLeaderAssignments(t *testing.T) {
	managedSlotServer := transport.NewServer()
	managedSlotMux := transport.NewRPCMux()
	managedSlotMux.Handle(rpcServiceManagedSlot, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeManagedSlotRequest(body)
		require.NoError(t, err)
		require.Equal(t, managedSlotRPCStatus, req.Kind)
		require.Equal(t, uint32(1), req.SlotID)
		return encodeManagedSlotResponse(managedSlotRPCResponse{})
	})
	managedSlotServer.HandleRPCMux(managedSlotMux)
	require.NoError(t, managedSlotServer.Start("127.0.0.1:0"))
	t.Cleanup(managedSlotServer.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			2: managedSlotServer.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 1},
		transportResources: transportResources{
			fwdClient: client,
		},
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{2}},
				},
			},
		},
	}

	err := cluster.RecoverSlotStrict(context.Background(), 1, RecoverStrategyLatestLiveReplica)
	require.NoError(t, err)
}

func TestRecoverSlotStrictReturnsManualRecoveryRequiredWhenQuorumLost(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{NodeID: 1},
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{2, 3, 4}},
				},
			},
		},
	}

	err := cluster.RecoverSlotStrict(context.Background(), 1, RecoverStrategyLatestLiveReplica)
	require.ErrorIs(t, err, ErrManualRecoveryRequired)
}

func TestRecoverSlotStrictReturnsNotFoundWhenStrictAssignmentsMissSlot(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{NodeID: 1},
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 2, DesiredPeers: []uint64{2}},
				},
			},
		},
	}

	err := cluster.RecoverSlotStrict(context.Background(), 1, RecoverStrategyLatestLiveReplica)
	require.ErrorIs(t, err, ErrSlotNotFound)
}

func TestAddSlotChoosesNextSlotIDAndCurrentPeers(t *testing.T) {
	var got slotcontroller.AddSlotRequest
	cluster := &Cluster{
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
					{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}},
				},
				addSlotFn: func(_ context.Context, req slotcontroller.AddSlotRequest) error {
					got = req
					return nil
				},
			},
		},
	}

	slotID, err := cluster.AddSlot(context.Background())
	if err != nil {
		t.Fatalf("AddSlot() error = %v", err)
	}
	if slotID != 3 {
		t.Fatalf("AddSlot() slotID = %d, want 3", slotID)
	}
	if got.NewSlotID != 3 {
		t.Fatalf("submitted NewSlotID = %d, want 3", got.NewSlotID)
	}
	if len(got.Peers) != 3 || got.Peers[0] != 1 || got.Peers[1] != 2 || got.Peers[2] != 3 {
		t.Fatalf("submitted peers = %v, want [1 2 3]", got.Peers)
	}
}

func TestRemoveSlotSubmitsControllerCommand(t *testing.T) {
	var got slotcontroller.RemoveSlotRequest
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
					{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}},
				},
				removeSlotFn: func(_ context.Context, req slotcontroller.RemoveSlotRequest) error {
					got = req
					return nil
				},
			},
		},
	}

	if err := cluster.RemoveSlot(context.Background(), 2); err != nil {
		t.Fatalf("RemoveSlot() error = %v", err)
	}
	if got.SlotID != 2 {
		t.Fatalf("submitted SlotID = %d, want 2", got.SlotID)
	}
}

func TestRebalanceStartsMigrationsFromCurrentTable(t *testing.T) {
	table := NewHashSlotTable(12, 3)
	table.Reassign(4, 1)
	table.Reassign(5, 1)
	table.Reassign(8, 1)
	table.Reassign(9, 1)

	var started []slotcontroller.MigrationRequest
	cluster := &Cluster{
		router: NewRouter(table, 1, nil),
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: testControllerLeaderWaitTimeout,
			controllerClient: fakeControllerClient{
				startMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					started = append(started, req)
					return nil
				},
			},
		},
	}

	plan, err := cluster.Rebalance(context.Background())
	if err != nil {
		t.Fatalf("Rebalance() error = %v", err)
	}
	if len(plan) != 4 {
		t.Fatalf("len(plan) = %d, want 4", len(plan))
	}
	if len(started) != 4 {
		t.Fatalf("started migrations = %d, want 4", len(started))
	}
	for _, req := range started {
		if req.Source != 1 {
			t.Fatalf("migration source = %d, want 1", req.Source)
		}
		if req.Target != 2 && req.Target != 3 {
			t.Fatalf("migration target = %d, want 2 or 3", req.Target)
		}
	}
}
