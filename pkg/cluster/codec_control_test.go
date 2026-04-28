package cluster

import (
	"encoding/binary"
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
				CurrentVoters:       []uint64{1, 2},
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
			CurrentVoters:       []uint64{1, 2},
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

func TestControllerCodecNodeOnboardingRoundTrip(t *testing.T) {
	reqBody, err := encodeControllerRequest(controllerRPCRequest{
		Kind: controllerRPCCreateOnboardingPlan,
		OnboardingPlan: &nodeOnboardingPlanRequest{
			TargetNodeID: 4,
			RetryOfJobID: "failed-job",
		},
	})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}
	req, err := decodeControllerRequest(reqBody)
	if err != nil {
		t.Fatalf("decodeControllerRequest() error = %v", err)
	}
	if req.Kind != controllerRPCCreateOnboardingPlan || req.OnboardingPlan == nil ||
		req.OnboardingPlan.TargetNodeID != 4 || req.OnboardingPlan.RetryOfJobID != "failed-job" {
		t.Fatalf("decoded onboarding plan request = %+v", req.OnboardingPlan)
	}

	job := sampleClusterOnboardingJob("onboard-20260426-000001", controllermeta.OnboardingJobStatusPlanned)
	respBody, err := encodeControllerResponse(controllerRPCListOnboardingJobs, controllerRPCResponse{
		OnboardingJobs:      []controllermeta.NodeOnboardingJob{job},
		OnboardingCursor:    "cursor",
		OnboardingHasMore:   true,
		OnboardingErrorCode: onboardingErrorPlanStale,
	})
	if err != nil {
		t.Fatalf("encodeControllerResponse() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCListOnboardingJobs, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if len(resp.OnboardingJobs) != 1 || resp.OnboardingJobs[0].JobID != job.JobID ||
		len(resp.OnboardingJobs[0].Plan.Moves) != 1 || resp.OnboardingCursor != "cursor" ||
		!resp.OnboardingHasMore || resp.OnboardingErrorCode != onboardingErrorPlanStale {
		t.Fatalf("decoded onboarding response = %+v", resp)
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
					SlotID:                      3,
					DesiredPeers:                []uint64{2, 4, 6},
					PreferredLeader:             4,
					LeaderTransferCooldownUntil: time.Unix(1710002220, 10),
					ConfigEpoch:                 7,
					BalanceVersion:              8,
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
					CurrentVoters:       []uint64{1, 3},
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
					CurrentVoters:       []uint64{1, 2},
					LeaderID:            2,
					HealthyVoters:       3,
					HasQuorum:           true,
					ObservedConfigEpoch: 8,
					LastReportAt:        reportedAt,
				},
				{
					SlotID:              2,
					CurrentPeers:        []uint64{2, 3, 4},
					CurrentVoters:       []uint64{2, 3},
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
				CurrentVoters:       []uint64{1, 2},
				LeaderID:            2,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        reportedAt,
			},
			{
				SlotID:              2,
				CurrentPeers:        []uint64{2, 3, 4},
				CurrentVoters:       []uint64{2, 3},
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

func TestControllerCodecAssignmentRoundTripPreferredLeader(t *testing.T) {
	cooldown := time.Unix(10, 20)
	body := appendAssignment(nil, controllermeta.SlotAssignment{
		SlotID:                      1,
		DesiredPeers:                []uint64{1, 2, 3},
		PreferredLeader:             2,
		LeaderTransferCooldownUntil: cooldown,
		ConfigEpoch:                 4,
		BalanceVersion:              5,
	})

	got, rest, err := consumeAssignment(body)
	if err != nil {
		t.Fatalf("consumeAssignment() error = %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("consumeAssignment() rest len = %d, want 0", len(rest))
	}
	if got.PreferredLeader != 2 {
		t.Fatalf("PreferredLeader = %d, want 2", got.PreferredLeader)
	}
	if !got.LeaderTransferCooldownUntil.Equal(cooldown) {
		t.Fatalf("LeaderTransferCooldownUntil = %v, want %v", got.LeaderTransferCooldownUntil, cooldown)
	}
}

func TestControllerCodecRuntimeViewRoundTripCurrentVoters(t *testing.T) {
	now := time.Unix(10, 20)
	body := appendRuntimeView(nil, controllermeta.SlotRuntimeView{
		SlotID:              1,
		CurrentPeers:        []uint64{1, 2, 3, 4},
		CurrentVoters:       []uint64{1, 2, 3},
		LeaderID:            2,
		HealthyVoters:       3,
		HasQuorum:           true,
		ObservedConfigEpoch: 7,
		LastReportAt:        now,
	})

	got, rest, err := consumeRuntimeView(body)
	if err != nil {
		t.Fatalf("consumeRuntimeView() error = %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("consumeRuntimeView() rest len = %d, want 0", len(rest))
	}
	if want := []uint64{1, 2, 3}; !reflect.DeepEqual(got.CurrentVoters, want) {
		t.Fatalf("CurrentVoters = %v, want %v", got.CurrentVoters, want)
	}
}

func TestControllerCodecDecodeLegacyAssignmentRecordDefaultsPreferredLeader(t *testing.T) {
	legacy := make([]byte, 0, 48)
	legacy = binary.BigEndian.AppendUint32(legacy, 1)
	legacy = appendUint64Slice(legacy, []uint64{1, 2, 3})
	legacy = binary.BigEndian.AppendUint64(legacy, 4)
	legacy = binary.BigEndian.AppendUint64(legacy, 5)

	got, err := decodeAssignmentRecord(legacy)
	if err != nil {
		t.Fatalf("decodeAssignmentRecord() error = %v", err)
	}
	if got.PreferredLeader != 0 {
		t.Fatalf("PreferredLeader = %d, want 0", got.PreferredLeader)
	}
	if !got.LeaderTransferCooldownUntil.IsZero() {
		t.Fatalf("LeaderTransferCooldownUntil = %v, want zero", got.LeaderTransferCooldownUntil)
	}
}

func TestControllerCodecDecodeLegacyRuntimeViewRecordDefaultsCurrentVoters(t *testing.T) {
	legacy := make([]byte, 0, 64)
	legacy = binary.BigEndian.AppendUint32(legacy, 1)
	legacy = appendUint64Slice(legacy, []uint64{1, 2, 3})
	legacy = binary.BigEndian.AppendUint64(legacy, 2)
	legacy = binary.BigEndian.AppendUint32(legacy, 3)
	legacy = append(legacy, 1)
	legacy = binary.BigEndian.AppendUint64(legacy, 7)
	legacy = appendInt64(legacy, time.Unix(10, 20).UnixNano())

	got, err := decodeRuntimeViewRecord(legacy)
	if err != nil {
		t.Fatalf("decodeRuntimeViewRecord() error = %v", err)
	}
	if got.CurrentVoters != nil {
		t.Fatalf("CurrentVoters = %v, want nil", got.CurrentVoters)
	}
}
