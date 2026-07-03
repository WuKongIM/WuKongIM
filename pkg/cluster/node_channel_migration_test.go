package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

func TestClusterV2ChannelMigrationStoreReadsSlotLeaderState(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	id := channelv2.ChannelID{ID: "migration-remote-read", Type: 1}
	route := waitRouteKeyLeaderReady(t, nodes[0], id.ID)
	leader := clusterNodeByID(t, nodes, route.Leader)
	queryNode := firstNonLeaderNode(t, nodes, route.Leader)
	task := migrationNodeTestTask(id, "task-remote-read")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := leader.defaultSlotMetaDB.ForHashSlot(route.HashSlot).CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(leader seed): %v", err)
	}
	if _, ok, err := queryNode.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetActiveChannelMigrationTask(ctx, id.ID, int64(id.Type)); err != nil || ok {
		t.Fatalf("query node local active ok=%v err=%v, want no local seed", ok, err)
	}

	store := requireNodeMigrationStore(t, queryNode)
	active, ok, err := store.GetActive(ctx, id)
	if err != nil || !ok {
		t.Fatalf("GetActive(remote) ok=%v err=%v", ok, err)
	}
	if active.TaskID != task.TaskID {
		t.Fatalf("active task id = %q, want %q", active.TaskID, task.TaskID)
	}
	byID, ok, err := store.Get(ctx, id, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("Get(remote) ok=%v err=%v", ok, err)
	}
	if byID.TaskID != task.TaskID {
		t.Fatalf("task id = %q, want %q", byID.TaskID, task.TaskID)
	}
	list, err := store.ListActive(ctx, id, 10)
	if err != nil {
		t.Fatalf("ListActive(remote) error = %v", err)
	}
	if len(list) != 1 || list[0].TaskID != task.TaskID {
		t.Fatalf("active list = %+v, want seeded task", list)
	}
}

func TestClusterV2ChannelMigrationStoreCreateReadsRemoteRuntimeMeta(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	id := channelv2.ChannelID{ID: "migration-remote-create", Type: 1}
	route := waitRouteKeyLeaderReady(t, nodes[0], id.ID)
	leader := clusterNodeByID(t, nodes, route.Leader)
	queryNode := firstNonLeaderNode(t, nodes, route.Leader)
	meta := migrationNodeTestRuntimeMeta(id)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := leader.defaultSlotMetaDB.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(leader seed): %v", err)
	}

	store := requireNodeMigrationStore(t, queryNode)
	created, err := store.CreateLeaderTransfer(ctx, channelwrapper.CreateLeaderTransferRequest{
		ChannelID:     id,
		TaskID:        "task-remote-create",
		DesiredLeader: 2,
	})
	if err != nil {
		t.Fatalf("CreateLeaderTransfer(remote runtime meta) error = %v", err)
	}

	waitUntil(t, func() bool {
		got, err := leader.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelMigrationTask(ctx, id.ID, int64(id.Type), created.TaskID)
		return err == nil && got.TaskID == created.TaskID
	})

	_, err = store.CreateLeaderTransfer(ctx, channelwrapper.CreateLeaderTransferRequest{
		ChannelID:     id,
		TaskID:        "task-remote-create-duplicate",
		DesiredLeader: 2,
	})
	if !errors.Is(err, metadb.ErrStaleMeta) {
		t.Fatalf("CreateLeaderTransfer(remote duplicate) err = %v, want stale meta", err)
	}
}

func TestClusterV2ChannelMigrationLocalReadRequiresActualSlotLeader(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	id := channelv2.ChannelID{ID: "migration-stale-local-leader", Type: 1}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	meta := migrationNodeTestRuntimeMeta(id)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	node.defaultSlotProposer = fixedChannelMigrationSlotRuntime{localLeader: false}

	_, err := node.readChannelMigrationRuntimeMeta(ctx, route.HashSlot, id.ID, int64(id.Type))
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("readChannelMigrationRuntimeMeta() err = %v, want ErrNotLeader", err)
	}

	payload, err := encodeChannelMigrationMetaRPCRequest(channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpGetRuntime,
		HashSlot:    route.HashSlot,
		ChannelID:   id.ID,
		ChannelType: int64(id.Type),
	})
	if err != nil {
		t.Fatalf("encodeChannelMigrationMetaRPCRequest(): %v", err)
	}
	_, err = (channelMigrationMetaHandler{node: node}).HandleRPC(ctx, payload)
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("channelMigrationMetaHandler.HandleRPC() err = %v, want ErrNotLeader", err)
	}
}

func TestClusterV2ChannelMigrationReadUsesLocalActualSlotLeaderWhenRouteLeaderStale(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	id := channelv2.ChannelID{ID: "migration-stale-route-local-leader", Type: 1}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	meta := migrationNodeTestRuntimeMeta(id)
	task := migrationNodeTestTask(id, "task-stale-route-local")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	if err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).CreateChannelMigrationTask(ctx, task); err != nil {
		t.Fatalf("CreateChannelMigrationTask(): %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: route.SlotID, Leader: 2, LeaderTerm: route.LeaderTerm + 1}})
	node.defaultSlotProposer = fixedChannelMigrationSlotRuntime{localLeader: true}

	staleRoute, err := node.RouteKey(id.ID)
	if err != nil {
		t.Fatalf("RouteKey() error = %v", err)
	}
	if staleRoute.Leader != 2 {
		t.Fatalf("RouteKey() leader = %d, want stale remote leader 2", staleRoute.Leader)
	}

	activeInSlot, err := node.ActiveChannelMigrationInHashSlot(ctx, route.HashSlot, id)
	if err != nil || !activeInSlot {
		t.Fatalf("ActiveChannelMigrationInHashSlot(local actual leader) ok=%v err=%v", activeInSlot, err)
	}

	store := requireNodeMigrationStore(t, node)
	active, ok, err := store.GetActive(ctx, id)
	if err != nil || !ok {
		t.Fatalf("GetActive(local actual leader) ok=%v err=%v", ok, err)
	}
	if active.TaskID != task.TaskID {
		t.Fatalf("active task id = %q, want %q", active.TaskID, task.TaskID)
	}
	gotMeta, err := node.readChannelMigrationRuntimeMeta(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil {
		t.Fatalf("readChannelMigrationRuntimeMeta(local actual leader) error = %v", err)
	}
	if gotMeta.ChannelID != id.ID || gotMeta.ChannelType != int64(id.Type) {
		t.Fatalf("runtime meta = %+v, want %s/%d", gotMeta, id.ID, id.Type)
	}
}

func TestChannelMigrationRemoteErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "stale meta", err: transportv2.RemoteError{Code: "remote_error", Message: metadb.ErrStaleMeta.Error()}, want: metadb.ErrStaleMeta},
		{name: "not leader", err: transportv2.RemoteError{Code: "remote_error", Message: ErrNotLeader.Error()}, want: ErrNotLeader},
		{name: "invalid argument", err: transportv2.RemoteError{Code: "remote_error", Message: metadb.ErrInvalidArgument.Error()}, want: metadb.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mapChannelMigrationRemoteError(tc.err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("mapChannelMigrationRemoteError() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestClusterV2ChannelMigrationStoreDuplicateCreateReturnsStaleMeta(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	id := channelv2.ChannelID{ID: "migration-duplicate-stale", Type: 1}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	meta := migrationNodeTestRuntimeMeta(id)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}

	store := requireNodeMigrationStore(t, node)
	if _, err := store.CreateReplicaReplace(ctx, channelwrapper.CreateReplicaReplaceRequest{
		ChannelID:  id,
		TaskID:     "task-duplicate-a",
		SourceNode: 1,
		TargetNode: 4,
	}); err != nil {
		t.Fatalf("CreateReplicaReplace(first) error = %v", err)
	}
	_, err := store.CreateReplicaReplace(ctx, channelwrapper.CreateReplicaReplaceRequest{
		ChannelID:  id,
		TaskID:     "task-duplicate-b",
		SourceNode: 1,
		TargetNode: 5,
	})
	if !errors.Is(err, metadb.ErrStaleMeta) {
		t.Fatalf("CreateReplicaReplace(duplicate) err = %v, want stale meta", err)
	}
}

func requireNodeMigrationStore(t testing.TB, node *Node) *channelwrapper.MigrationStore {
	t.Helper()
	service, ok := node.channels.(*channelwrapper.Service)
	if !ok {
		t.Fatalf("node %d channels = %T, want *channels.Service", node.NodeID(), node.channels)
	}
	store := service.MigrationStore()
	if store == nil {
		t.Fatal("MigrationStore() = nil")
	}
	return store
}

func clusterNodeByID(t testing.TB, nodes []*Node, nodeID uint64) *Node {
	t.Helper()
	for _, node := range nodes {
		if node.NodeID() == nodeID {
			return node
		}
	}
	t.Fatalf("node %d not found", nodeID)
	return nil
}

func migrationNodeTestRuntimeMeta(id channelv2.ChannelID) metadb.ChannelRuntimeMeta {
	return metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID:       id.ID,
		ChannelType:     int64(id.Type),
		ChannelEpoch:    10,
		LeaderEpoch:     20,
		RouteGeneration: 30,
		Replicas:        []uint64{1, 2, 3},
		ISR:             []uint64{1, 2, 3},
		Leader:          1,
		MinISR:          2,
		Status:          uint8(channelv2.StatusActive),
		LeaseUntilMS:    1750001000000,
	})
}

func migrationNodeTestTask(id channelv2.ChannelID, taskID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       1,
		TargetNode:       4,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

type fixedChannelMigrationSlotRuntime struct {
	localLeader bool
}

func (r fixedChannelMigrationSlotRuntime) IsLocalLeader(uint32) bool {
	return r.localLeader
}

func (r fixedChannelMigrationSlotRuntime) Propose(context.Context, uint32, []byte) error {
	return nil
}
