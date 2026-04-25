package cluster_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestManagedClusterRoutesWritesByHashSlotTable(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 2, 8, 3)
	defer stopNodes(nodes)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "managed-hashslot")
	hashSlot := nodes[0].cluster.HashSlotForKey(uid)
	require.Equal(t, multiraft.SlotID(2), nodes[0].cluster.SlotForKey(uid))
	require.NotEqual(t, uint16(2), hashSlot)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, nodes[0].store.UpsertUser(ctx, metadb.User{
		UID:   uid,
		Token: "managed-route",
	}))

	require.Eventually(t, func() bool {
		got, err := nodes[0].db.ForHashSlot(hashSlot).GetUser(ctx, uid)
		if err != nil {
			return false
		}
		if got.Token != "managed-route" {
			return false
		}
		_, err = nodes[0].db.ForSlot(2).GetUser(ctx, uid)
		return errors.Is(err, metadb.ErrNotFound)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestRestartNodePreservesConfiguredHashSlotCount(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 2, 8, 3)
	defer stopNodes(nodes)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "managed-restart")
	originalHashSlot := nodes[0].cluster.HashSlotForKey(uid)
	require.Equal(t, multiraft.SlotID(2), nodes[0].cluster.SlotForKey(uid))
	require.NotEqual(t, uint16(2), originalHashSlot)

	restarted := restartNode(t, nodes, 0)

	require.Equal(t, originalHashSlot, restarted.cluster.HashSlotForKey(uid))
}

func TestManagedClusterRefreshesHashSlotTableOnControllerFinalizeMigration(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 2, 8, 3)
	defer stopNodes(nodes)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "managed-refresh")
	hashSlot := nodes[0].cluster.HashSlotForKey(uid)
	sourceSlot := nodes[0].cluster.SlotForKey(uid)
	require.Equal(t, multiraft.SlotID(2), sourceSlot)

	targetSlot := multiraft.SlotID(1)
	initialVersion := nodes[0].cluster.HashSlotTableVersion()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, nodes[0].cluster.StartHashSlotMigration(ctx, hashSlot, targetSlot))
	require.NoError(t, finalizeHashSlotMigrationOnController(ctx, nodes, hashSlot, sourceSlot, targetSlot))

	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if node == nil || node.cluster == nil {
				return false
			}
			if node.cluster.HashSlotTableVersion() <= initialVersion {
				return false
			}
			if node.cluster.SlotForKey(uid) != targetSlot {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond)

	require.NoError(t, nodes[0].store.UpsertUser(ctx, metadb.User{
		UID:   uid,
		Token: "managed-refresh",
	}))

	require.Eventually(t, func() bool {
		got, err := nodes[0].db.ForHashSlot(hashSlot).GetUser(ctx, uid)
		if err != nil || got.Token != "managed-refresh" {
			return false
		}
		routed, err := nodes[0].store.GetUser(ctx, uid)
		return err == nil && routed.Token == "managed-refresh"
	}, 10*time.Second, 100*time.Millisecond)
}

func TestManagedClusterAddSlotRebalancesHashSlotsWithoutLosingData(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 2, 8, 3)
	defer stopNodes(nodes)

	initialAssignments := snapshotAssignments(t, nodes, 2)
	require.Len(t, initialAssignments, 2)
	for _, assignment := range initialAssignments {
		require.NotEmpty(t, assignment.DesiredPeers)
		for _, peer := range assignment.DesiredPeers {
			require.NotZero(t, peer)
		}
	}

	fixtures := seedUsersPerHashSlot(t, nodes[0], 3, "managed-add-slot")

	slotID := addSlotOnControllerLeader(t, nodes)
	require.Equal(t, multiraft.SlotID(3), slotID)

	waitForManagedSlotsSettled(t, nodes, 3)
	waitForStableLeader(t, nodes, uint64(slotID))
	waitForNoActiveMigrations(t, nodes, 15*time.Second)

	table := nodes[0].cluster.GetHashSlotTable()
	require.NotNil(t, table)
	require.Len(t, table.HashSlotsOf(1), 3)
	require.Len(t, table.HashSlotsOf(2), 3)
	require.Len(t, table.HashSlotsOf(3), 2)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	moved := 0
	for _, fixture := range fixtures {
		wantSlot := table.Lookup(fixture.hashSlot)
		require.NotZero(t, wantSlot)
		if wantSlot != fixture.initialSlot {
			moved++
		}

		require.Eventually(t, func() bool {
			got, err := nodes[0].store.GetUser(ctx, fixture.uid)
			if err != nil || got.Token != fixture.token {
				return false
			}
			return nodes[0].cluster.SlotForKey(fixture.uid) == wantSlot
		}, 10*time.Second, 100*time.Millisecond)
	}
	require.Greater(t, moved, 0)
}

func TestManagedClusterRemoveSlotRebalancesHashSlotsAndRemovesAssignment(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 3, 8, 3)
	defer stopNodes(nodes)

	initialAssignments := snapshotAssignments(t, nodes, 3)
	require.Len(t, initialAssignments, 3)
	for _, assignment := range initialAssignments {
		require.NotEmpty(t, assignment.DesiredPeers)
		for _, peer := range assignment.DesiredPeers {
			require.NotZero(t, peer)
		}
	}

	fixtures := seedUsersPerHashSlot(t, nodes[0], 3, "managed-remove-slot")

	removeSlotOnControllerLeader(t, nodes, 3)

	waitForManagedSlotsSettled(t, nodes, 2)
	waitForNoActiveMigrations(t, nodes, 15*time.Second)

	table := nodes[0].cluster.GetHashSlotTable()
	require.NotNil(t, table)
	require.Len(t, table.HashSlotsOf(1), 4)
	require.Len(t, table.HashSlotsOf(2), 4)
	require.Empty(t, table.HashSlotsOf(3))

	assignments := snapshotAssignments(t, nodes, 2)
	for _, assignment := range assignments {
		require.NotEqual(t, uint32(3), assignment.SlotID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	moved := 0
	for _, fixture := range fixtures {
		wantSlot := table.Lookup(fixture.hashSlot)
		require.NotZero(t, wantSlot)
		if fixture.initialSlot == 3 && wantSlot != fixture.initialSlot {
			moved++
		}

		require.Eventually(t, func() bool {
			got, err := nodes[0].store.GetUser(ctx, fixture.uid)
			if err != nil || got.Token != fixture.token {
				return false
			}
			return nodes[0].cluster.SlotForKey(fixture.uid) == wantSlot
		}, 10*time.Second, 100*time.Millisecond)
	}
	require.Greater(t, moved, 0)
}

func TestManagedClusterRebalanceRestoresBalancedHashSlotOwnership(t *testing.T) {
	nodes := startThreeNodesWithControllerAndHashSlots(t, 3, 8, 3)
	defer stopNodes(nodes)

	fixtures := seedUsersPerHashSlot(t, nodes[0], 3, "managed-rebalance")

	initialTable := nodes[0].cluster.GetHashSlotTable()
	require.NotNil(t, initialTable)
	slotTwoHashSlots := initialTable.HashSlotsOf(2)
	require.Len(t, slotTwoHashSlots, 3)

	for _, hashSlot := range slotTwoHashSlots[1:] {
		startHashSlotMigrationAndWait(t, nodes, hashSlot, 1)
	}

	skewed := nodes[0].cluster.GetHashSlotTable()
	require.NotNil(t, skewed)
	require.Len(t, skewed.HashSlotsOf(1), 5)
	require.Len(t, skewed.HashSlotsOf(2), 1)
	require.Len(t, skewed.HashSlotsOf(3), 2)

	plan := rebalanceOnControllerLeader(t, nodes)
	require.Len(t, plan, 2)

	targets := make(map[uint16]multiraft.SlotID, len(plan))
	for _, migration := range plan {
		targets[migration.HashSlot] = migration.To
	}
	waitForHashSlotTargets(t, nodes, targets, 15*time.Second)
	waitForNoActiveMigrations(t, nodes, 15*time.Second)

	table := nodes[0].cluster.GetHashSlotTable()
	require.NotNil(t, table)
	require.Len(t, table.HashSlotsOf(1), 3)
	require.Len(t, table.HashSlotsOf(2), 3)
	require.Len(t, table.HashSlotsOf(3), 2)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	changed := 0
	for _, fixture := range fixtures {
		wantSlot := table.Lookup(fixture.hashSlot)
		require.NotZero(t, wantSlot)
		if wantSlot != skewed.Lookup(fixture.hashSlot) {
			changed++
		}

		require.Eventually(t, func() bool {
			got, err := nodes[0].store.GetUser(ctx, fixture.uid)
			if err != nil || got.Token != fixture.token {
				return false
			}
			return nodes[0].cluster.SlotForKey(fixture.uid) == wantSlot
		}, 10*time.Second, 100*time.Millisecond)
	}
	require.Greater(t, changed, 0)
}

func finalizeHashSlotMigrationOnController(ctx context.Context, nodes []*testNode, hashSlot uint16, source, target multiraft.SlotID) error {
	controller, ok := currentControllerLeaderNode(nodes)
	if !ok {
		return errors.New("controller leader not found")
	}
	payload := make([]byte, 0, 1+1+4+1+19)
	payload = append(payload, 1 /* controller codec version */, 13 /* finalize_migration kind */)
	payload = binary.BigEndian.AppendUint32(payload, 0)
	payload = binary.AppendUvarint(payload, 19)
	payload = binary.BigEndian.AppendUint16(payload, hashSlot)
	payload = binary.BigEndian.AppendUint64(payload, uint64(source))
	payload = binary.BigEndian.AppendUint64(payload, uint64(target))
	payload = append(payload, 0)

	respBody, err := controller.cluster.RPCService(ctx, controller.nodeID, multiraft.SlotID(^uint32(0)), 14, payload)
	if err != nil {
		return err
	}
	if len(respBody) < 2 {
		return errors.New("invalid controller response")
	}
	resp := struct{ NotLeader bool }{NotLeader: respBody[1]&1 != 0}
	if resp.NotLeader {
		return errors.New("finalize migration redirected")
	}
	return nil
}

type managedUserFixture struct {
	uid         string
	token       string
	hashSlot    uint16
	initialSlot multiraft.SlotID
}

func seedUsersPerHashSlot(t *testing.T, node *testNode, perHashSlot int, prefix string) []managedUserFixture {
	t.Helper()

	table := node.cluster.GetHashSlotTable()
	require.NotNil(t, table)

	hashSlotCount := int(table.HashSlotCount())
	require.Positive(t, hashSlotCount)
	require.Positive(t, perHashSlot)

	seedDeadline := time.Now().Add(20 * time.Second)

	counts := make(map[uint16]int, hashSlotCount)
	fixtures := make([]managedUserFixture, 0, hashSlotCount*perHashSlot)
	for i := 0; len(fixtures) < hashSlotCount*perHashSlot; i++ {
		uid := fmt.Sprintf("%s-%d", prefix, i)
		hashSlot := node.cluster.HashSlotForKey(uid)
		if counts[hashSlot] >= perHashSlot {
			continue
		}

		token := fmt.Sprintf("token-%d", i)
		remaining := time.Until(seedDeadline)
		if remaining <= 0 {
			t.Fatalf("seedUsersPerHashSlot() timed out before populating all hash slots")
		}
		if remaining > 5*time.Second {
			remaining = 5 * time.Second
		}

		// Give each write its own budget so suite-wide load does not consume the entire seed window.
		writeCtx, cancel := context.WithTimeout(context.Background(), remaining)
		err := node.store.UpsertUser(writeCtx, metadb.User{
			UID:   uid,
			Token: token,
		})
		cancel()
		require.NoError(t, err)

		fixtures = append(fixtures, managedUserFixture{
			uid:         uid,
			token:       token,
			hashSlot:    hashSlot,
			initialSlot: node.cluster.SlotForKey(uid),
		})
		counts[hashSlot]++
	}
	return fixtures
}

func waitForNoActiveMigrations(t *testing.T, nodes []*testNode, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := true
		for _, node := range nodes {
			if node == nil || node.cluster == nil {
				continue
			}
			if len(node.cluster.GetMigrationStatus()) != 0 {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, node := range nodes {
		if node == nil || node.cluster == nil {
			continue
		}
		t.Logf("node=%d tableVersion=%d migrations=%+v", node.nodeID, node.cluster.HashSlotTableVersion(), node.cluster.GetMigrationStatus())
	}
	t.Fatalf("active migrations did not settle within %s", timeout)
}

func waitForHashSlotTargets(t *testing.T, nodes []*testNode, targets map[uint16]multiraft.SlotID, timeout time.Duration) {
	t.Helper()

	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if node == nil || node.cluster == nil {
				continue
			}
			table := node.cluster.GetHashSlotTable()
			if table == nil {
				return false
			}
			for hashSlot, target := range targets {
				if table.Lookup(hashSlot) != target {
					return false
				}
			}
		}
		return true
	}, timeout, 100*time.Millisecond)
}

func addSlotOnControllerLeader(t *testing.T, nodes []*testNode) multiraft.SlotID {
	t.Helper()

	var (
		slotID  multiraft.SlotID
		lastErr error
	)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			lastErr = errors.New("controller leader not found")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		slotID, lastErr = controller.cluster.AddSlot(context.Background())
		if lastErr == nil {
			return slotID
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return slotID
}

func removeSlotOnControllerLeader(t *testing.T, nodes []*testNode, slotID multiraft.SlotID) {
	t.Helper()

	var lastErr error
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			lastErr = errors.New("controller leader not found")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		lastErr = controller.cluster.RemoveSlot(context.Background(), slotID)
		if lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}

func rebalanceOnControllerLeader(t *testing.T, nodes []*testNode) []raftcluster.MigrationPlan {
	t.Helper()

	var (
		plan    []raftcluster.MigrationPlan
		lastErr error
	)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			lastErr = errors.New("controller leader not found")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		plan, lastErr = controller.cluster.Rebalance(context.Background())
		if lastErr == nil {
			return plan
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return nil
}

func startHashSlotMigrationAndWait(t *testing.T, nodes []*testNode, hashSlot uint16, targetSlot multiraft.SlotID) {
	t.Helper()

	var lastErr error
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			lastErr = errors.New("controller leader not found")
			time.Sleep(200 * time.Millisecond)
			continue
		}
		lastErr = controller.cluster.StartHashSlotMigration(context.Background(), hashSlot, targetSlot)
		if lastErr == nil {
			waitForHashSlotTargets(t, nodes, map[uint16]multiraft.SlotID{hashSlot: targetSlot}, 15*time.Second)
			waitForNoActiveMigrations(t, nodes, 15*time.Second)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}
