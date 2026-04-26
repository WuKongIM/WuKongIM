package cluster

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestControllerHandlerAcceptsBinaryRequestBeforeHostChecks(t *testing.T) {
	handler := &controllerHandler{cluster: &Cluster{}}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCHeartbeat,
		Report: &slotcontroller.AgentReport{
			NodeID:         1,
			Addr:           "127.0.0.1:1111",
			ObservedAt:     time.Unix(1710000000, 0),
			CapacityWeight: 1,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	_, err = handler.Handle(context.Background(), body)
	if err != ErrNotStarted {
		t.Fatalf("controllerHandler.Handle() error = %v, want %v", err, ErrNotStarted)
	}
}

func TestControllerHandlerHeartbeatRedirectsFollower(t *testing.T) {
	cluster, _, _ := newTestLocalControllerCluster(t, false)
	handler := &controllerHandler{cluster: cluster}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCHeartbeat,
		Report: &slotcontroller.AgentReport{
			NodeID:     2,
			Addr:       "127.0.0.1:2222",
			ObservedAt: time.Unix(1710000100, 0),
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCHeartbeat, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if !resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = false, want true")
	}
}

func TestControllerHandlerRuntimeReportRedirectsFollower(t *testing.T) {
	cluster, _, _ := newTestLocalControllerCluster(t, false)
	handler := &controllerHandler{cluster: cluster}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCRuntimeReport,
		RuntimeReport: &runtimeObservationReport{
			NodeID:     2,
			ObservedAt: time.Unix(1710000200, 0),
			FullSync:   true,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCRuntimeReport, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if !resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = false, want true")
	}
}

func TestControllerHandlerRuntimeReportUpdatesLeaderObservationWithoutProposal(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}
	before, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(before) error = %v", err)
	}
	report := runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(1710000200, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              7,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            2,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 9,
			LastReportAt:        time.Unix(1710000201, 0),
		}},
	}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind:          controllerRPCRuntimeReport,
		RuntimeReport: &report,
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCRuntimeReport, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = true, want false")
	}

	after, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(after) error = %v", err)
	}
	if after != before {
		t.Fatalf("LastIndex() after runtime report = %d, want unchanged %d", after, before)
	}

	snapshot := host.snapshotObservations()
	if len(snapshot.Nodes) != 0 {
		t.Fatalf("snapshot.Nodes = %#v", snapshot.Nodes)
	}
	if len(snapshot.RuntimeViews) != 1 || snapshot.RuntimeViews[0].SlotID != report.Views[0].SlotID {
		t.Fatalf("snapshot.RuntimeViews = %#v", snapshot.RuntimeViews)
	}
	views, err := host.meta.ListRuntimeViews(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeViews() error = %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("ListRuntimeViews() = %#v, want runtime view to stay out of durable store", views)
	}
}

func TestControllerHandlerHeartbeatUpdatesNodeObservationOnly(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}
	report := slotcontroller.AgentReport{
		NodeID:               2,
		Addr:                 "127.0.0.1:2222",
		ObservedAt:           time.Unix(1710000200, 0),
		CapacityWeight:       7,
		HashSlotTableVersion: 0,
		Runtime: &controllermeta.SlotRuntimeView{
			SlotID:              7,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            2,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 9,
			LastReportAt:        time.Unix(1710000201, 0),
		},
	}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind:   controllerRPCHeartbeat,
		Report: &report,
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCHeartbeat, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = true, want false")
	}
	if resp.HashSlotTableVersion == 0 {
		t.Fatal("controllerHandler.Handle() HashSlotTableVersion = 0, want non-zero")
	}

	snapshot := host.snapshotObservations()
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].NodeID != report.NodeID {
		t.Fatalf("snapshot.Nodes = %#v", snapshot.Nodes)
	}
	if len(snapshot.RuntimeViews) != 0 {
		t.Fatalf("snapshot.RuntimeViews = %#v, want no runtime views from heartbeat", snapshot.RuntimeViews)
	}

	nodes, err := host.meta.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != report.NodeID || nodes[0].Status != controllermeta.NodeStatusAlive {
		t.Fatalf("ListNodes() = %#v, want one alive durable status edge", nodes)
	}
}

func TestControllerHandlerHeartbeatPreservesJoinedAdvertiseAddr(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}
	advertiseAddr := "wk-node-2.example:7000"
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          2,
		Name:            "worker-2",
		Addr:            advertiseAddr,
		Role:            controllermeta.NodeRoleData,
		JoinState:       controllermeta.NodeJoinStateJoining,
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        time.Unix(1710000300, 0),
		LastHeartbeatAt: time.Unix(1710000300, 0),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}

	reporterServer := newStartedTestServer(t)
	reporter := &Cluster{
		cfg: Config{
			NodeID:        2,
			ListenAddr:    "0.0.0.0:0",
			AdvertiseAddr: advertiseAddr,
		},
		transportResources: transportResources{server: reporterServer},
	}
	report := slotcontrollerReport(reporter, time.Unix(1710000400, 0), nil)
	if report.Addr != advertiseAddr {
		t.Fatalf("heartbeat report Addr = %q, want advertise addr %q", report.Addr, advertiseAddr)
	}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind:   controllerRPCHeartbeat,
		Report: &report,
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	if _, err := handler.Handle(context.Background(), body); err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}

	node, err := host.meta.GetNode(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.Addr != advertiseAddr {
		t.Fatalf("node Addr after heartbeat = %q, want %q", node.Addr, advertiseAddr)
	}
}

func TestControllerHandlerJoinClusterLeaderProposesNodeJoin(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.JoinToken = "join-secret"
	discovery := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: cluster.cfg.NodeID, Addr: cluster.cfg.Nodes[0].Addr}})
	defer discovery.Stop()
	cluster.discovery = discovery
	handler := &controllerHandler{cluster: cluster}

	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCJoinCluster,
		Join: &joinClusterRequest{
			NodeID:         2,
			Name:           "worker-2",
			Addr:           "127.0.0.1:2222",
			CapacityWeight: 5,
			Token:          "join-secret",
			Version:        supportedJoinProtocolVersion,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCJoinCluster, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = true, want false")
	}
	if resp.Join == nil {
		t.Fatal("Join = nil, want payload")
	}
	if resp.Join.JoinErrorCode != joinErrorNone {
		t.Fatalf("JoinErrorCode = %q, want empty", resp.Join.JoinErrorCode)
	}
	if resp.Join.HashSlotTableVersion == 0 || len(resp.Join.HashSlotTable) == 0 {
		t.Fatalf("hash slot sync = version %d bytes %d, want payload", resp.Join.HashSlotTableVersion, len(resp.Join.HashSlotTable))
	}
	if len(resp.Join.Nodes) != 1 || resp.Join.Nodes[0].NodeID != 2 {
		t.Fatalf("Join.Nodes = %#v, want joined node", resp.Join.Nodes)
	}

	node, err := host.meta.GetNode(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.Name != "worker-2" || node.Addr != "127.0.0.1:2222" {
		t.Fatalf("joined node identity = %+v", node)
	}
	if node.JoinState != controllermeta.NodeJoinStateJoining || node.Role != controllermeta.NodeRoleData {
		t.Fatalf("joined node membership = role %d state %d", node.Role, node.JoinState)
	}
	addr, err := discovery.Resolve(2)
	if err != nil {
		t.Fatalf("Resolve(joined node) error = %v", err)
	}
	if addr != "127.0.0.1:2222" {
		t.Fatalf("Resolve(joined node) = %q, want %q", addr, "127.0.0.1:2222")
	}
}

func TestControllerHandlerJoinClusterRejectsInvalidTokenWithoutProposal(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.JoinToken = "join-secret"
	handler := &controllerHandler{cluster: cluster}

	before, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(before) error = %v", err)
	}
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCJoinCluster,
		Join: &joinClusterRequest{
			NodeID:         2,
			Addr:           "127.0.0.1:2222",
			Token:          "wrong",
			Version:        supportedJoinProtocolVersion,
			CapacityWeight: 1,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCJoinCluster, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.Join == nil || resp.Join.JoinErrorCode != joinErrorInvalidToken {
		t.Fatalf("join response = %+v, want invalid token", resp.Join)
	}
	if _, err := host.meta.GetNode(context.Background(), 2); err != controllermeta.ErrNotFound {
		t.Fatalf("GetNode() error = %v, want not found", err)
	}
	after, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(after) error = %v", err)
	}
	if after != before {
		t.Fatalf("LastIndex() after invalid token = %d, want unchanged %d", after, before)
	}
}

func TestControllerHandlerJoinClusterRejectsConflictsWithExplicitCodes(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.JoinToken = "join-secret"
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:2222",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000500, 0),
		CapacityWeight:  1,
	}))

	for _, tc := range []struct {
		name string
		req  joinClusterRequest
		code joinErrorCode
	}{
		{
			name: "node id conflict",
			req: joinClusterRequest{
				NodeID: 2, Addr: "127.0.0.1:3333", Token: "join-secret", Version: supportedJoinProtocolVersion,
			},
			code: joinErrorNodeIDConflict,
		},
		{
			name: "address conflict",
			req: joinClusterRequest{
				NodeID: 3, Addr: "127.0.0.1:2222", Token: "join-secret", Version: supportedJoinProtocolVersion,
			},
			code: joinErrorAddrConflict,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCJoinCluster, Join: &req})
			if err != nil {
				t.Fatalf("encodeControllerRequest() error = %v", err)
			}
			respBody, err := handler.Handle(context.Background(), body)
			if err != nil {
				t.Fatalf("controllerHandler.Handle() error = %v", err)
			}
			resp, err := decodeControllerResponse(controllerRPCJoinCluster, respBody)
			if err != nil {
				t.Fatalf("decodeControllerResponse() error = %v", err)
			}
			if resp.Join == nil || resp.Join.JoinErrorCode != tc.code {
				t.Fatalf("join response = %+v, want code %q", resp.Join, tc.code)
			}
		})
	}
}

func TestCommittedJoinRejectionClassifiesPostCommitConflicts(t *testing.T) {
	tests := []struct {
		name  string
		req   joinClusterRequest
		nodes []controllermeta.ClusterNode
		code  joinErrorCode
	}{
		{
			name:  "committed membership",
			req:   joinClusterRequest{NodeID: 4, Addr: "wk-node-4:7000"},
			nodes: []controllermeta.ClusterNode{{NodeID: 4, Addr: "wk-node-4:7000", JoinState: controllermeta.NodeJoinStateJoining}},
			code:  joinErrorNone,
		},
		{
			name:  "node id conflict",
			req:   joinClusterRequest{NodeID: 4, Addr: "wk-node-4-new:7000"},
			nodes: []controllermeta.ClusterNode{{NodeID: 4, Addr: "wk-node-4-old:7000", JoinState: controllermeta.NodeJoinStateJoining}},
			code:  joinErrorNodeIDConflict,
		},
		{
			name:  "address conflict",
			req:   joinClusterRequest{NodeID: 5, Addr: "wk-node-4:7000"},
			nodes: []controllermeta.ClusterNode{{NodeID: 4, Addr: "wk-node-4:7000", JoinState: controllermeta.NodeJoinStateJoining}},
			code:  joinErrorAddrConflict,
		},
		{
			name:  "missing membership",
			req:   joinClusterRequest{NodeID: 5, Addr: "wk-node-5:7000"},
			nodes: []controllermeta.ClusterNode{{NodeID: 4, Addr: "wk-node-4:7000", JoinState: controllermeta.NodeJoinStateJoining}},
			code:  joinErrorTemporary,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := joinRejectionFromCommittedNodes(tc.nodes, tc.req)
			if tc.code == joinErrorNone {
				if resp != nil {
					t.Fatalf("joinRejectionFromCommittedNodes() = %+v, want nil", resp)
				}
				return
			}
			if resp == nil || resp.JoinErrorCode != tc.code {
				t.Fatalf("joinRejectionFromCommittedNodes() = %+v, want code %q", resp, tc.code)
			}
		})
	}
}

func TestControllerHandlerDynamicJoinRejectsUnknownObservationReports(t *testing.T) {
	cluster, _, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.JoinToken = "join-secret"
	handler := &controllerHandler{cluster: cluster}

	heartbeatBody, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCHeartbeat,
		Report: &slotcontroller.AgentReport{
			NodeID:     2,
			Addr:       "127.0.0.1:2222",
			ObservedAt: time.Unix(1710000600, 0),
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	if _, err := handler.Handle(context.Background(), heartbeatBody); err != ErrInvalidConfig {
		t.Fatalf("heartbeat Handle() error = %v, want %v", err, ErrInvalidConfig)
	}

	runtimeBody, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCRuntimeReport,
		RuntimeReport: &runtimeObservationReport{
			NodeID:     2,
			ObservedAt: time.Unix(1710000601, 0),
			FullSync:   true,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	if _, err := handler.Handle(context.Background(), runtimeBody); err != ErrInvalidConfig {
		t.Fatalf("runtime Handle() error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestControllerHandlerRuntimeFullSyncActivatesJoiningNode(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.JoinToken = "join-secret"
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:2222",
		Role:            controllermeta.NodeRoleData,
		JoinState:       controllermeta.NodeJoinStateJoining,
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        time.Unix(1710000600, 0),
		LastHeartbeatAt: time.Unix(1710000600, 0),
		CapacityWeight:  1,
	}))
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCRuntimeReport,
		RuntimeReport: &runtimeObservationReport{
			NodeID:     2,
			ObservedAt: time.Unix(1710000700, 0),
			FullSync:   true,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCRuntimeReport, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.NotLeader {
		t.Fatal("controllerHandler.Handle() NotLeader = true, want false")
	}
	node, err := host.meta.GetNode(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if node.JoinState != controllermeta.NodeJoinStateActive {
		t.Fatalf("JoinState = %d, want active", node.JoinState)
	}
}

func TestControllerHandlerHeartbeatUsesHashSlotSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	snapshot := NewHashSlotTable(8, 2)
	host.storeHashSlotTableSnapshot(snapshot)

	stored := snapshot.Clone()
	stored.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), stored))

	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCHeartbeat,
		Report: &slotcontroller.AgentReport{
			NodeID:               2,
			Addr:                 "127.0.0.1:2222",
			ObservedAt:           time.Unix(1710000300, 0),
			HashSlotTableVersion: 0,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCHeartbeat, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.HashSlotTableVersion != snapshot.Version() {
		t.Fatalf("HashSlotTableVersion = %d, want snapshot version %d", resp.HashSlotTableVersion, snapshot.Version())
	}
}

func TestControllerHandlerListAssignmentsUsesHashSlotSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertAssignment(context.Background(), controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{{
			SlotID:       1,
			DesiredPeers: []uint64{1, 2, 3},
			ConfigEpoch:  9,
		}},
		AssignmentsBySlot: map[uint32]controllermeta.SlotAssignment{
			1: {
				SlotID:       1,
				DesiredPeers: []uint64{1, 2, 3},
				ConfigEpoch:  9,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	snapshot := NewHashSlotTable(8, 2)
	host.storeHashSlotTableSnapshot(snapshot)

	stored := snapshot.Clone()
	stored.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), stored))

	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListAssignments})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCListAssignments, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if len(resp.Assignments) != 1 || resp.Assignments[0].SlotID != 1 {
		t.Fatalf("Assignments = %#v, want slot 1 assignment", resp.Assignments)
	}
	if resp.Assignments[0].ConfigEpoch != 9 {
		t.Fatalf("Assignments[0].ConfigEpoch = %d, want metadata snapshot epoch 9", resp.Assignments[0].ConfigEpoch)
	}
	if resp.HashSlotTableVersion != snapshot.Version() {
		t.Fatalf("HashSlotTableVersion = %d, want snapshot version %d", resp.HashSlotTableVersion, snapshot.Version())
	}
	table, err := DecodeHashSlotTable(resp.HashSlotTable)
	if err != nil {
		t.Fatalf("DecodeHashSlotTable() error = %v", err)
	}
	if table.Version() != snapshot.Version() {
		t.Fatalf("decoded table version = %d, want snapshot version %d", table.Version(), snapshot.Version())
	}
}

func TestControllerHandlerListNodesUsesMetadataSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710011000, 0),
		CapacityWeight:  1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Nodes: []controllermeta.ClusterNode{{
			NodeID:          1,
			Addr:            "127.0.0.1:7001",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(1710011001, 0),
			CapacityWeight:  8,
		}},
		NodesByID: map[uint64]controllermeta.ClusterNode{
			1: {
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: time.Unix(1710011001, 0),
				CapacityWeight:  8,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListNodes})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCListNodes, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].CapacityWeight != 8 {
		t.Fatalf("Nodes = %#v, want metadata snapshot node capacity 8", resp.Nodes)
	}
}

func TestControllerHandlerListTasksUsesMetadataSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 5,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 5,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListTasks})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCListTasks, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].Attempt != 5 || resp.Tasks[0].Kind != controllermeta.TaskKindRepair {
		t.Fatalf("Tasks = %#v, want metadata snapshot task attempt 5 repair", resp.Tasks)
	}
}

func TestControllerHandlerGetTaskUsesMetadataSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 6,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 6,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCGetTask, SlotID: 1})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCGetTask, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.Task == nil || resp.Task.Attempt != 6 || resp.Task.Kind != controllermeta.TaskKindRepair {
		t.Fatalf("Task = %#v, want metadata snapshot task attempt 6 repair", resp.Task)
	}
}

func TestControllerHandlerMetadataReadsFallBackToStoreWhenSnapshotDirty(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710011100, 0),
		CapacityWeight:  2,
	}))
	requireNoErr(t, host.meta.UpsertAssignment(context.Background(), controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 4},
		ConfigEpoch:  4,
	}))
	requireNoErr(t, host.meta.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 2,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Nodes: []controllermeta.ClusterNode{{
			NodeID:          1,
			Addr:            "127.0.0.1:7001",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(1710011101, 0),
			CapacityWeight:  99,
		}},
		NodesByID: map[uint64]controllermeta.ClusterNode{
			1: {
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: time.Unix(1710011101, 0),
				CapacityWeight:  99,
			},
		},
		Assignments: []controllermeta.SlotAssignment{{
			SlotID:       1,
			DesiredPeers: []uint64{9, 9, 9},
			ConfigEpoch:  99,
		}},
		AssignmentsBySlot: map[uint32]controllermeta.SlotAssignment{
			1: {
				SlotID:       1,
				DesiredPeers: []uint64{9, 9, 9},
				ConfigEpoch:  99,
			},
		},
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 99,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 99,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      true,
	})

	assignmentsBody, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListAssignments})
	if err != nil {
		t.Fatalf("encodeControllerRequest(list_assignments) error = %v", err)
	}
	assignmentsRespBody, err := handler.Handle(context.Background(), assignmentsBody)
	if err != nil {
		t.Fatalf("controllerHandler.Handle(list_assignments) error = %v", err)
	}
	assignmentsResp, err := decodeControllerResponse(controllerRPCListAssignments, assignmentsRespBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse(list_assignments) error = %v", err)
	}
	if len(assignmentsResp.Assignments) != 1 || assignmentsResp.Assignments[0].ConfigEpoch != 4 {
		t.Fatalf("Assignments = %#v, want store fallback config epoch 4", assignmentsResp.Assignments)
	}

	nodesBody, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListNodes})
	if err != nil {
		t.Fatalf("encodeControllerRequest(list_nodes) error = %v", err)
	}
	nodesRespBody, err := handler.Handle(context.Background(), nodesBody)
	if err != nil {
		t.Fatalf("controllerHandler.Handle(list_nodes) error = %v", err)
	}
	nodesResp, err := decodeControllerResponse(controllerRPCListNodes, nodesRespBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse(list_nodes) error = %v", err)
	}
	if len(nodesResp.Nodes) != 1 || nodesResp.Nodes[0].CapacityWeight != 2 {
		t.Fatalf("Nodes = %#v, want store fallback capacity 2", nodesResp.Nodes)
	}

	tasksBody, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCListTasks})
	if err != nil {
		t.Fatalf("encodeControllerRequest(list_tasks) error = %v", err)
	}
	tasksRespBody, err := handler.Handle(context.Background(), tasksBody)
	if err != nil {
		t.Fatalf("controllerHandler.Handle(list_tasks) error = %v", err)
	}
	tasksResp, err := decodeControllerResponse(controllerRPCListTasks, tasksRespBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse(list_tasks) error = %v", err)
	}
	if len(tasksResp.Tasks) != 1 || tasksResp.Tasks[0].Attempt != 2 {
		t.Fatalf("Tasks = %#v, want store fallback attempt 2", tasksResp.Tasks)
	}

	getTaskBody, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCGetTask, SlotID: 1})
	if err != nil {
		t.Fatalf("encodeControllerRequest(get_task) error = %v", err)
	}
	getTaskRespBody, err := handler.Handle(context.Background(), getTaskBody)
	if err != nil {
		t.Fatalf("controllerHandler.Handle(get_task) error = %v", err)
	}
	getTaskResp, err := decodeControllerResponse(controllerRPCGetTask, getTaskRespBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse(get_task) error = %v", err)
	}
	if getTaskResp.Task == nil || getTaskResp.Task.Attempt != 2 || getTaskResp.Task.Kind != controllermeta.TaskKindBootstrap {
		t.Fatalf("Task = %#v, want store fallback task attempt 2 bootstrap", getTaskResp.Task)
	}
}

func TestControllerHandlerFetchObservationDeltaReturnsIncrementalState(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	host.syncState.reset()
	host.syncState.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
		Tasks:       []controllermeta.ReconcileTask{testObservationTask(1, 1)},
		Nodes:       []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
	})
	host.syncState.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(1710007000, 0)),
	})
	before := host.syncState.currentRevisions()

	host.syncState.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{testObservationAssignment(1, 2)},
		Tasks:       []controllermeta.ReconcileTask{testObservationTask(1, 2)},
		Nodes:       []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
	})
	host.syncState.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 2, []uint64{1, 2, 3}, 2, time.Unix(1710007010, 0)),
	})

	host.warmupMu.RLock()
	leaderGeneration := host.warmupGeneration
	host.warmupMu.RUnlock()

	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCFetchObservationDelta,
		ObservationDelta: &observationDeltaRequest{
			LeaderID:         uint64(host.localNode),
			LeaderGeneration: leaderGeneration,
			Revisions:        before,
			RequestedSlots:   []uint32{1},
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCFetchObservationDelta, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.ObservationDelta == nil {
		t.Fatal("ObservationDelta = nil, want payload")
	}
	if resp.ObservationDelta.FullSync {
		t.Fatal("ObservationDelta.FullSync = true, want incremental delta")
	}
	if got, want := len(resp.ObservationDelta.Assignments), 1; got != want {
		t.Fatalf("len(ObservationDelta.Assignments) = %d, want %d", got, want)
	}
	if got, want := len(resp.ObservationDelta.Tasks), 1; got != want {
		t.Fatalf("len(ObservationDelta.Tasks) = %d, want %d", got, want)
	}
	if got, want := len(resp.ObservationDelta.RuntimeViews), 1; got != want {
		t.Fatalf("len(ObservationDelta.RuntimeViews) = %d, want %d", got, want)
	}
	if got, want := len(resp.ObservationDelta.Nodes), 0; got != want {
		t.Fatalf("len(ObservationDelta.Nodes) = %d, want %d", got, want)
	}
}

func TestControllerHandlerFetchObservationDeltaForcesFullSyncOnLeaderGenerationMismatch(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	host.syncState.reset()
	host.syncState.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
		Tasks:       []controllermeta.ReconcileTask{testObservationTask(1, 1)},
		Nodes:       []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
	})
	host.syncState.replaceRuntimeViews([]controllermeta.SlotRuntimeView{
		testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(1710007020, 0)),
	})

	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCFetchObservationDelta,
		ObservationDelta: &observationDeltaRequest{
			LeaderID:         uint64(host.localNode),
			LeaderGeneration: 0,
			Revisions: observationRevisions{
				Assignments: host.syncState.currentRevisions().Assignments,
				Tasks:       host.syncState.currentRevisions().Tasks,
				Nodes:       host.syncState.currentRevisions().Nodes,
				Runtime:     host.syncState.currentRevisions().Runtime,
			},
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCFetchObservationDelta, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.ObservationDelta == nil {
		t.Fatal("ObservationDelta = nil, want payload")
	}
	if !resp.ObservationDelta.FullSync {
		t.Fatal("ObservationDelta.FullSync = false, want full sync")
	}
	if got, want := len(resp.ObservationDelta.Assignments), 1; got != want {
		t.Fatalf("len(ObservationDelta.Assignments) = %d, want %d", got, want)
	}
	if got, want := resp.ObservationDelta.LeaderGeneration, uint64(1); got != want {
		t.Fatalf("ObservationDelta.LeaderGeneration = %d, want %d", got, want)
	}
}

func TestControllerHandlerHeartbeatBackfillsHashSlotSnapshotOnColdMiss(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	handler := &controllerHandler{cluster: cluster}

	stored := NewHashSlotTable(8, 2)
	stored.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), stored))

	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCHeartbeat,
		Report: &slotcontroller.AgentReport{
			NodeID:               2,
			Addr:                 "127.0.0.1:2222",
			ObservedAt:           time.Unix(1710000400, 0),
			HashSlotTableVersion: 0,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("controllerHandler.Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCHeartbeat, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.HashSlotTableVersion != stored.Version() {
		t.Fatalf("HashSlotTableVersion = %d, want stored version %d", resp.HashSlotTableVersion, stored.Version())
	}

	snapshot, ok := host.hashSlotTableSnapshot()
	if !ok {
		t.Fatal("hashSlotTableSnapshot() ok = false, want true")
	}
	if snapshot.Version() != stored.Version() {
		t.Fatalf("hashSlotTableSnapshot().Version() = %d, want %d", snapshot.Version(), stored.Version())
	}
}

func newTestLocalControllerCluster(t *testing.T, start bool) (*Cluster, *controllerHost, *transportLayer) {
	t.Helper()

	cfg := validTestConfig()
	cfg.ControllerReplicaN = 1
	cfg.Nodes = []NodeConfig{{NodeID: cfg.NodeID, Addr: "127.0.0.1:0"}}
	cfg.ControllerMetaPath = filepath.Join(t.TempDir(), "controller-meta")
	cfg.ControllerRaftPath = filepath.Join(t.TempDir(), "controller-raft")

	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)
	requireNoErr(t, layer.Start(
		"127.0.0.1:0",
		func([]byte) {},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	))
	t.Cleanup(layer.Stop)

	cfg.Nodes[0].Addr = layer.server.Listener().Addr().String()
	host, err := newControllerHost(cfg, layer)
	if err != nil {
		t.Fatalf("newControllerHost() error = %v", err)
	}
	t.Cleanup(host.Stop)

	if start {
		requireNoErr(t, host.Start(context.Background()))
		waitForTestControllerLeader(t, host, cfg.NodeID)
	}

	cluster := &Cluster{
		cfg:    cfg,
		router: NewRouter(NewHashSlotTable(cfg.effectiveHashSlotCount(), int(cfg.effectiveInitialSlotCount())), cfg.NodeID, nil),
		transportResources: transportResources{
			server:    layer.server,
			discovery: layer.discovery,
		},
		controllerResources: controllerResources{
			controllerHost: host,
			controllerMeta: host.meta,
			controller:     host.service,
		},
	}
	return cluster, host, layer
}

func waitForTestControllerLeader(t *testing.T, host *controllerHost, want multiraft.NodeID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for host.LeaderID() != want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if host.LeaderID() != want {
		t.Fatalf("controllerHost.LeaderID() = %d, want %d", host.LeaderID(), want)
	}
}
