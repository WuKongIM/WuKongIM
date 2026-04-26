package cluster

import (
	"reflect"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

func TestControllerCodecRoundTripRequest(t *testing.T) {
	reportedAt := time.Unix(1710000000, 1234)
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind:   controllerRPCHeartbeat,
		SlotID: 7,
		Report: &slotcontroller.AgentReport{
			NodeID:         9,
			Addr:           "10.0.0.9:1111",
			ObservedAt:     reportedAt,
			CapacityWeight: 3,
			Runtime: &controllermeta.SlotRuntimeView{
				SlotID:              7,
				CurrentPeers:        []uint64{1, 2, 3},
				LeaderID:            2,
				HealthyVoters:       2,
				HasQuorum:           true,
				ObservedConfigEpoch: 11,
				LastReportAt:        reportedAt.Add(time.Second),
			},
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	req, err := decodeControllerRequest(body)
	if err != nil {
		t.Fatalf("decodeControllerRequest() error = %v", err)
	}
	if req.Kind != controllerRPCHeartbeat || req.SlotID != 7 {
		t.Fatalf("decoded request header = %+v", req)
	}
	if req.Report == nil || !reflect.DeepEqual(*req.Report, slotcontroller.AgentReport{
		NodeID:         9,
		Addr:           "10.0.0.9:1111",
		ObservedAt:     reportedAt,
		CapacityWeight: 3,
		Runtime: &controllermeta.SlotRuntimeView{
			SlotID:              7,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            2,
			HealthyVoters:       2,
			HasQuorum:           true,
			ObservedConfigEpoch: 11,
			LastReportAt:        reportedAt.Add(time.Second),
		},
	}) {
		t.Fatalf("decoded report = %+v", req.Report)
	}
}

func TestControllerCodecJoinClusterRoundTrip(t *testing.T) {
	joinedAt := time.Unix(1710001234, 99)
	reqBody, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCJoinCluster,
		Join: &joinClusterRequest{
			NodeID:         4,
			Name:           "worker-4",
			Addr:           "10.0.0.4:1111",
			CapacityWeight: 7,
			Token:          "join-secret",
			Version:        supportedJoinProtocolVersion,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	req, err := decodeControllerRequest(reqBody)
	if err != nil {
		t.Fatalf("decodeControllerRequest() error = %v", err)
	}
	if req.Kind != controllerRPCJoinCluster {
		t.Fatalf("req.Kind = %q, want %q", req.Kind, controllerRPCJoinCluster)
	}
	if req.Join == nil || !reflect.DeepEqual(*req.Join, joinClusterRequest{
		NodeID:         4,
		Name:           "worker-4",
		Addr:           "10.0.0.4:1111",
		CapacityWeight: 7,
		Token:          "join-secret",
		Version:        supportedJoinProtocolVersion,
	}) {
		t.Fatalf("decoded join request = %+v", req.Join)
	}

	table := NewHashSlotTable(8, 2)
	respBody, err := encodeControllerResponse(controllerRPCJoinCluster, controllerRPCResponse{
		LeaderID:   1,
		LeaderAddr: "10.0.0.1:1111",
		Join: &joinClusterResponse{
			Nodes: []controllermeta.ClusterNode{{
				NodeID:          4,
				Name:            "worker-4",
				Addr:            "10.0.0.4:1111",
				Role:            controllermeta.NodeRoleData,
				JoinState:       controllermeta.NodeJoinStateJoining,
				Status:          controllermeta.NodeStatusAlive,
				JoinedAt:        joinedAt,
				LastHeartbeatAt: joinedAt.Add(time.Second),
				CapacityWeight:  7,
			}},
			HashSlotTableVersion: table.Version(),
			HashSlotTable:        table.Encode(),
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerResponse() error = %v", err)
	}

	resp, err := decodeControllerResponse(controllerRPCJoinCluster, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.LeaderID != 1 || resp.LeaderAddr != "10.0.0.1:1111" {
		t.Fatalf("decoded leader hint = id %d addr %q", resp.LeaderID, resp.LeaderAddr)
	}
	if resp.Join == nil {
		t.Fatal("resp.Join = nil, want payload")
	}
	if !reflect.DeepEqual(resp.Join.Nodes, []controllermeta.ClusterNode{{
		NodeID:          4,
		Name:            "worker-4",
		Addr:            "10.0.0.4:1111",
		Role:            controllermeta.NodeRoleData,
		JoinState:       controllermeta.NodeJoinStateJoining,
		Status:          controllermeta.NodeStatusAlive,
		JoinedAt:        joinedAt,
		LastHeartbeatAt: joinedAt.Add(time.Second),
		CapacityWeight:  7,
	}}) {
		t.Fatalf("decoded join nodes = %+v", resp.Join.Nodes)
	}
	if resp.Join.HashSlotTableVersion != table.Version() || !reflect.DeepEqual(resp.Join.HashSlotTable, table.Encode()) {
		t.Fatalf("decoded hash slot sync = version %d bytes %d", resp.Join.HashSlotTableVersion, len(resp.Join.HashSlotTable))
	}
}

func TestControllerCodecJoinClusterRejectionRoundTrip(t *testing.T) {
	body, err := encodeControllerResponse(controllerRPCJoinCluster, controllerRPCResponse{
		Join: &joinClusterResponse{
			JoinErrorCode:    joinErrorInvalidToken,
			JoinErrorMessage: "invalid join token",
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerResponse() error = %v", err)
	}

	resp, err := decodeControllerResponse(controllerRPCJoinCluster, body)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.Join == nil {
		t.Fatal("resp.Join = nil, want payload")
	}
	if resp.Join.JoinErrorCode != joinErrorInvalidToken || resp.Join.JoinErrorMessage != "invalid join token" {
		t.Fatalf("decoded join rejection = %+v", resp.Join)
	}
}

func TestControllerCodecClusterNodeMembershipFieldsRoundTrip(t *testing.T) {
	joinedAt := time.Unix(1710002345, 0)
	nodes := []controllermeta.ClusterNode{{
		NodeID:          6,
		Name:            "worker-6",
		Addr:            "10.0.0.6:1111",
		Role:            controllermeta.NodeRoleControllerVoter,
		JoinState:       controllermeta.NodeJoinStateRejected,
		Status:          controllermeta.NodeStatusSuspect,
		JoinedAt:        joinedAt,
		LastHeartbeatAt: joinedAt.Add(time.Second),
		CapacityWeight:  11,
	}}

	got, err := decodeClusterNodes(encodeClusterNodes(nodes))
	if err != nil {
		t.Fatalf("decodeClusterNodes() error = %v", err)
	}
	if !reflect.DeepEqual(got, nodes) {
		t.Fatalf("decoded nodes = %+v, want %+v", got, nodes)
	}
}

func TestControllerCodecRoundTripResponsePayloads(t *testing.T) {
	nextRunAt := time.Unix(1710001111, 9876)
	cases := []struct {
		name string
		kind string
		resp controllerRPCResponse
	}{
		{
			name: "assignments",
			kind: controllerRPCListAssignments,
			resp: controllerRPCResponse{
				Assignments: []controllermeta.SlotAssignment{{
					SlotID:         3,
					DesiredPeers:   []uint64{2, 4, 6},
					ConfigEpoch:    7,
					BalanceVersion: 8,
				}},
			},
		},
		{
			name: "nodes",
			kind: controllerRPCListNodes,
			resp: controllerRPCResponse{
				Nodes: []controllermeta.ClusterNode{{
					NodeID:          4,
					Addr:            "10.0.0.4:1111",
					Status:          controllermeta.NodeStatusDraining,
					LastHeartbeatAt: time.Unix(1710002222, 10),
					CapacityWeight:  5,
				}},
			},
		},
		{
			name: "runtime views",
			kind: controllerRPCListRuntimeViews,
			resp: controllerRPCResponse{
				RuntimeViews: []controllermeta.SlotRuntimeView{{
					SlotID:              8,
					CurrentPeers:        []uint64{1, 3, 8},
					LeaderID:            3,
					HealthyVoters:       2,
					HasQuorum:           true,
					ObservedConfigEpoch: 21,
					LastReportAt:        time.Unix(1710003333, 20),
				}},
			},
		},
		{
			name: "task",
			kind: controllerRPCGetTask,
			resp: controllerRPCResponse{
				Task: &controllermeta.ReconcileTask{
					SlotID:     9,
					Kind:       controllermeta.TaskKindRepair,
					Step:       controllermeta.TaskStepTransferLeader,
					SourceNode: 1,
					TargetNode: 2,
					Attempt:    3,
					NextRunAt:  nextRunAt,
					Status:     controllermeta.TaskStatusRetrying,
					LastError:  "retry me",
				},
			},
		},
		{
			name: "tasks",
			kind: controllerRPCListTasks,
			resp: controllerRPCResponse{
				Tasks: []controllermeta.ReconcileTask{{
					SlotID:     11,
					Kind:       controllermeta.TaskKindRebalance,
					Step:       controllermeta.TaskStepPromote,
					SourceNode: 8,
					TargetNode: 3,
					Attempt:    4,
					NextRunAt:  nextRunAt,
					Status:     controllermeta.TaskStatusPending,
					LastError:  "",
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := encodeControllerResponse(tc.kind, tc.resp)
			if err != nil {
				t.Fatalf("encodeControllerResponse() error = %v", err)
			}

			got, err := decodeControllerResponse(tc.kind, body)
			if err != nil {
				t.Fatalf("decodeControllerResponse() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.resp) {
				t.Fatalf("decoded response = %+v, want %+v", got, tc.resp)
			}
		})
	}
}

func TestControllerCodecResponseFlags(t *testing.T) {
	body, err := encodeControllerResponse(controllerRPCGetTask, controllerRPCResponse{
		NotLeader: true,
		NotFound:  true,
		LeaderID:  12,
	})
	if err != nil {
		t.Fatalf("encodeControllerResponse() error = %v", err)
	}

	resp, err := decodeControllerResponse(controllerRPCGetTask, body)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if !resp.NotLeader || !resp.NotFound || resp.LeaderID != 12 {
		t.Fatalf("decoded response flags = %+v", resp)
	}
}

func TestControllerCodecRuntimeObservationReportRoundTrip(t *testing.T) {
	reportedAt := time.Unix(1710004444, 55)
	body, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCRuntimeReport,
		RuntimeReport: &runtimeObservationReport{
			NodeID:     9,
			ObservedAt: reportedAt,
			FullSync:   true,
			Views: []controllermeta.SlotRuntimeView{
				{
					SlotID:              1,
					CurrentPeers:        []uint64{1, 2, 3},
					LeaderID:            2,
					HealthyVoters:       3,
					HasQuorum:           true,
					ObservedConfigEpoch: 8,
					LastReportAt:        reportedAt,
				},
				{
					SlotID:              2,
					CurrentPeers:        []uint64{2, 3, 4},
					LeaderID:            3,
					HealthyVoters:       2,
					HasQuorum:           true,
					ObservedConfigEpoch: 9,
					LastReportAt:        reportedAt.Add(time.Second),
				},
			},
			ClosedSlots: []uint32{7, 8},
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	req, err := decodeControllerRequest(body)
	if err != nil {
		t.Fatalf("decodeControllerRequest() error = %v", err)
	}
	if req.Kind != controllerRPCRuntimeReport {
		t.Fatalf("req.Kind = %q, want %q", req.Kind, controllerRPCRuntimeReport)
	}
	if req.RuntimeReport == nil {
		t.Fatal("req.RuntimeReport = nil, want payload")
	}
	if !reflect.DeepEqual(*req.RuntimeReport, runtimeObservationReport{
		NodeID:     9,
		ObservedAt: reportedAt,
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{
			{
				SlotID:              1,
				CurrentPeers:        []uint64{1, 2, 3},
				LeaderID:            2,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        reportedAt,
			},
			{
				SlotID:              2,
				CurrentPeers:        []uint64{2, 3, 4},
				LeaderID:            3,
				HealthyVoters:       2,
				HasQuorum:           true,
				ObservedConfigEpoch: 9,
				LastReportAt:        reportedAt.Add(time.Second),
			},
		},
		ClosedSlots: []uint32{7, 8},
	}) {
		t.Fatalf("decoded runtime report = %+v", req.RuntimeReport)
	}
}

func TestControllerCodecObservationDeltaRoundTrip(t *testing.T) {
	reportedAt := time.Unix(1710006666, 0)
	reqBody, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCFetchObservationDelta,
		ObservationDelta: &observationDeltaRequest{
			LeaderID:         9,
			LeaderGeneration: 3,
			Revisions: observationRevisions{
				Assignments: 1,
				Tasks:       2,
				Nodes:       3,
				Runtime:     4,
			},
			RequestedSlots: []uint32{7, 8},
			ForceFullSync:  true,
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	req, err := decodeControllerRequest(reqBody)
	if err != nil {
		t.Fatalf("decodeControllerRequest() error = %v", err)
	}
	if req.Kind != controllerRPCFetchObservationDelta {
		t.Fatalf("req.Kind = %q, want %q", req.Kind, controllerRPCFetchObservationDelta)
	}
	if req.ObservationDelta == nil {
		t.Fatal("req.ObservationDelta = nil, want payload")
	}
	if !reflect.DeepEqual(*req.ObservationDelta, observationDeltaRequest{
		LeaderID:         9,
		LeaderGeneration: 3,
		Revisions: observationRevisions{
			Assignments: 1,
			Tasks:       2,
			Nodes:       3,
			Runtime:     4,
		},
		RequestedSlots: []uint32{7, 8},
		ForceFullSync:  true,
	}) {
		t.Fatalf("decoded observation delta request = %+v", req.ObservationDelta)
	}

	respBody, err := encodeControllerResponse(controllerRPCFetchObservationDelta, controllerRPCResponse{
		ObservationDelta: &observationDeltaResponse{
			LeaderID:         9,
			LeaderGeneration: 3,
			Revisions: observationRevisions{
				Assignments: 5,
				Tasks:       6,
				Nodes:       7,
				Runtime:     8,
			},
			FullSync: true,
			Assignments: []controllermeta.SlotAssignment{
				testObservationAssignment(7, 9),
			},
			Tasks: []controllermeta.ReconcileTask{
				testObservationTask(7, 2),
			},
			Nodes: []controllermeta.ClusterNode{
				testObservationNode(3, controllermeta.NodeStatusSuspect),
			},
			RuntimeViews: []controllermeta.SlotRuntimeView{
				testObservationRuntimeView(7, 3, []uint64{3, 5, 7}, 9, reportedAt),
			},
			DeletedTasks:        []uint32{1},
			DeletedRuntimeSlots: []uint32{2},
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerResponse() error = %v", err)
	}

	resp, err := decodeControllerResponse(controllerRPCFetchObservationDelta, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.ObservationDelta == nil {
		t.Fatal("resp.ObservationDelta = nil, want payload")
	}
	if !reflect.DeepEqual(*resp.ObservationDelta, observationDeltaResponse{
		LeaderID:         9,
		LeaderGeneration: 3,
		Revisions: observationRevisions{
			Assignments: 5,
			Tasks:       6,
			Nodes:       7,
			Runtime:     8,
		},
		FullSync: true,
		Assignments: []controllermeta.SlotAssignment{
			testObservationAssignment(7, 9),
		},
		Tasks: []controllermeta.ReconcileTask{
			testObservationTask(7, 2),
		},
		Nodes: []controllermeta.ClusterNode{
			testObservationNode(3, controllermeta.NodeStatusSuspect),
		},
		RuntimeViews: []controllermeta.SlotRuntimeView{
			testObservationRuntimeView(7, 3, []uint64{3, 5, 7}, 9, reportedAt),
		},
		DeletedTasks:        []uint32{1},
		DeletedRuntimeSlots: []uint32{2},
	}) {
		t.Fatalf("decoded observation delta response = %+v", resp.ObservationDelta)
	}
}
