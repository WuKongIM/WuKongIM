package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestManagerScaleInPlanRequiresWritePermissionAndReturnsPreview(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 12, 2, 0, 0, time.UTC)
	var seen managementusecase.NodeScaleInPlanRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "reader",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			},
			{
				Username: "admin",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			},
		}),
		Management: managerNodesStub{
			scaleInPlanReqSink: &seen,
			scaleInPlan: managementusecase.NodeScaleInPlanResponse{
				NodeID:        4,
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				Candidates: []managementusecase.NodeScaleInCandidate{{
					SlotID:       9,
					SourceNodeID: 4,
					TargetNodeID: 2,
					DesiredPeers: []uint64{1, 4, 3},
					TargetPeers:  []uint64{1, 2, 3},
					ConfigEpoch:  7,
				}},
			},
		},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/plan", strings.NewReader(`{"max_slot_moves":2}`))
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d body=%s, want %d", denied.Code, denied.Body.String(), http.StatusForbidden)
	}
	if seen.NodeID != 0 {
		t.Fatalf("denied request reached management: %#v", seen)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/plan", strings.NewReader(`{"max_slot_moves":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if seen.NodeID != 4 || seen.MaxSlotMoves != 2 {
		t.Fatalf("request = %#v, want node 4 max 2", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-24T12:02:00Z",
		"state_revision": 22,
		"node_id": 4,
		"candidates": [{
			"slot_id": 9,
			"source_node_id": 4,
			"target_node_id": 2,
			"desired_peers": [1,4,3],
			"target_peers": [1,2,3],
			"config_epoch": 7
		}],
		"blocked_by_status": false
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInStartMarksNodeLeaving(t *testing.T) {
	var seen managementusecase.MarkNodeLeavingRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			markNodeLeavingReqSink: &seen,
			markNodeLeaving: managementusecase.MarkNodeLeavingResponse{
				Changed:   true,
				NodeID:    4,
				Addr:      "127.0.0.1:11114",
				JoinState: "leaving",
				Revision:  22,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/start", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"changed": true,
		"node_id": 4,
		"addr": "127.0.0.1:11114",
		"join_state": "leaving",
		"revision": 22
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInStartReturnsOKWhenAlreadyLeaving(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			markNodeLeaving: managementusecase.MarkNodeLeavingResponse{
				Changed:   false,
				NodeID:    4,
				Addr:      "127.0.0.1:11114",
				JoinState: "leaving",
				Revision:  22,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/start", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"changed":false`) {
		t.Fatalf("body = %s, want unchanged response", rec.Body.String())
	}
}

func TestManagerScaleInDrainRequiresWritePermissionAndReturnsRuntimeCounters(t *testing.T) {
	var seen managementusecase.SetNodeDrainModeRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "reader",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			},
			{
				Username: "admin",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			},
		}),
		Management: managerNodesStub{
			scaleInDrainReqSink: &seen,
			scaleInDrain: managementusecase.SetNodeDrainModeResponse{
				NodeID:               4,
				Draining:             true,
				AcceptingNewSessions: false,
				GatewaySessions:      3,
				ActiveOnline:         1,
				ClosingOnline:        0,
				TotalOnline:          2,
				PendingActivations:   1,
			},
		},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/drain", strings.NewReader(`{"draining":true}`))
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d body=%s, want %d", denied.Code, denied.Body.String(), http.StatusForbidden)
	}
	if seen.NodeID != 0 {
		t.Fatalf("denied request reached management: %#v", seen)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/drain", strings.NewReader(`{"draining":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if seen != (managementusecase.SetNodeDrainModeRequest{NodeID: 4, Draining: true}) {
		t.Fatalf("request = %#v, want node 4 draining true", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 4,
		"draining": true,
		"accepting_new_sessions": false,
		"gateway_sessions": 3,
		"active_online": 1,
		"closing_online": 0,
		"total_online": 2,
		"pending_activations": 1,
		"unknown": false
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInStatusRequiresReadPermission(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	var seen managementusecase.NodeScaleInStatusRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			scaleInStatusReqSink: &seen,
			scaleInStatus: managementusecase.NodeScaleInStatusResponse{
				NodeID:                  4,
				JoinState:               "leaving",
				GeneratedAt:             generatedAt,
				StateRevision:           22,
				SafeToProceed:           false,
				SafeToRemove:            false,
				BlockedBySlots:          true,
				BlockedBySlotRuntime:    true,
				BlockedByChannels:       true,
				BlockedByRuntimeDrain:   true,
				UnknownRuntime:          true,
				RuntimeUnknown:          true,
				SlotReplicaCount:        1,
				SlotLeaderCount:         2,
				ActiveTaskCount:         3,
				FailedTaskCount:         4,
				UnknownControlRevision:  true,
				UnknownChannelInventory: true,
				ChannelLeaderCount:      5,
				ChannelReplicaCount:     6,
				ChannelISRCount:         7,
				GatewayDraining:         true,
				AcceptingNewSessions:    false,
				GatewaySessions:         8,
				ActiveOnline:            9,
				ClosingOnline:           10,
				TotalOnline:             11,
				PendingActivations:      12,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/scale-in/status", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 4,
		"join_state": "leaving",
		"generated_at": "2026-06-24T12:00:00Z",
		"state_revision": 22,
		"safe_to_proceed": false,
		"safe_to_remove": false,
		"blocked_by_missing_node": false,
		"blocked_by_join_state": false,
		"blocked_by_control_revision": false,
		"blocked_by_health": false,
		"blocked_by_stale_revision": false,
		"blocked_by_controller_role": false,
		"blocked_by_data_role": false,
		"blocked_by_slots": true,
		"blocked_by_slot_leadership": false,
		"blocked_by_slot_runtime": true,
		"blocked_by_tasks": false,
		"blocked_by_channels": true,
		"blocked_by_runtime_drain": true,
		"unknown_runtime": true,
		"runtime_unknown": true,
		"unknown_control_revision": true,
		"unknown_channel_inventory": true,
		"health_fresh": false,
		"health_status": "",
		"health_freshness": "",
		"health_report_age_ms": 0,
		"health_report_ttl_ms": 0,
		"observed_control_revision": 0,
		"required_control_revision": 0,
		"blocked_reasons": [],
		"slot_replica_count": 1,
		"slot_leader_count": 2,
		"active_task_count": 3,
		"failed_task_count": 4,
		"channel_leader_count": 5,
		"channel_replica_count": 6,
		"channel_isr_count": 7,
		"gateway_draining": true,
		"accepting_new_sessions": false,
		"gateway_sessions": 8,
		"active_online": 9,
		"closing_online": 10,
		"total_online": 11,
		"pending_activations": 12
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInStatusMapsHealthBlockers(t *testing.T) {
	body, err := json.Marshal(nodeScaleInStatusResponseDTO(managementusecase.NodeScaleInStatusResponse{
		NodeID:                  4,
		JoinState:               "leaving",
		StateRevision:           22,
		BlockedByHealth:         true,
		BlockedByStaleRevision:  true,
		HealthFresh:             false,
		HealthStatus:            "alive",
		HealthFreshness:         "stale",
		HealthReportAgeMS:       31000,
		HealthReportTTLMS:       30000,
		ObservedControlRevision: 21,
		RequiredControlRevision: 22,
		BlockedReasons:          []string{"target_health_stale", "eligible_node_health_revision_stale"},
	}))
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}

	if !jsonEqual(string(body), `{
		"node_id": 4,
		"join_state": "leaving",
		"generated_at": "",
		"state_revision": 22,
		"safe_to_proceed": false,
		"safe_to_remove": false,
		"blocked_by_missing_node": false,
		"blocked_by_join_state": false,
		"blocked_by_control_revision": false,
		"blocked_by_health": true,
		"blocked_by_stale_revision": true,
		"blocked_by_controller_role": false,
		"blocked_by_data_role": false,
		"blocked_by_slots": false,
		"blocked_by_slot_leadership": false,
		"blocked_by_slot_runtime": false,
		"blocked_by_tasks": false,
		"blocked_by_channels": false,
		"blocked_by_runtime_drain": false,
		"unknown_runtime": false,
		"runtime_unknown": false,
		"unknown_control_revision": false,
		"unknown_channel_inventory": false,
		"health_fresh": false,
		"health_status": "alive",
		"health_freshness": "stale",
		"health_report_age_ms": 31000,
		"health_report_ttl_ms": 30000,
		"observed_control_revision": 21,
		"required_control_revision": 22,
		"blocked_reasons": ["target_health_stale", "eligible_node_health_revision_stale"],
		"slot_replica_count": 0,
		"slot_leader_count": 0,
		"active_task_count": 0,
		"failed_task_count": 0,
		"channel_leader_count": 0,
		"channel_replica_count": 0,
		"channel_isr_count": 0,
		"gateway_draining": false,
		"accepting_new_sessions": false,
		"gateway_sessions": 0,
		"active_online": 0,
		"closing_online": 0,
		"total_online": 0,
		"pending_activations": 0
	}`) {
		t.Fatalf("body = %s", string(body))
	}
}

func TestManagerScaleInAdvanceReturnsAcceptedWhenCreated(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 12, 1, 0, 0, time.UTC)
	var seen managementusecase.NodeScaleInAdvanceRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			scaleInAdvanceReqSink: &seen,
			scaleInAdvance: managementusecase.NodeScaleInAdvanceResponse{
				NodeID:        4,
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				Created:       1,
				Skipped:       0,
				Candidates: []managementusecase.NodeScaleInCandidate{{
					SlotID:       9,
					SourceNodeID: 4,
					TargetNodeID: 2,
					DesiredPeers: []uint64{1, 4, 3},
					TargetPeers:  []uint64{1, 2, 3},
					ConfigEpoch:  7,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/advance", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	if seen.NodeID != 4 || seen.MaxSlotMoves != 1 {
		t.Fatalf("request = %#v, want node 4 max 1", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-24T12:01:00Z",
		"state_revision": 22,
		"node_id": 4,
		"created": 1,
		"skipped": 0,
		"candidates": [{
			"slot_id": 9,
			"source_node_id": 4,
			"target_node_id": 2,
			"desired_peers": [1,4,3],
			"target_peers": [1,2,3],
			"config_epoch": 7
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInAdvanceMapsConflict(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			scaleInAdvanceErr: managementusecase.ErrNodeScaleInConflict,
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/advance", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), `"error":"conflict"`) {
		t.Fatalf("body = %s, want conflict error", rec.Body.String())
	}
}

func TestManagerScaleInMapsClusterUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		stub   managerNodesStub
	}{
		{
			name:   "status",
			method: http.MethodGet,
			path:   "/manager/nodes/4/scale-in/status",
			stub:   managerNodesStub{scaleInStatusErr: cluster.ErrNotStarted},
		},
		{
			name:   "advance",
			method: http.MethodPost,
			path:   "/manager/nodes/4/scale-in/advance",
			body:   `{"max_slot_moves":1}`,
			stub:   managerNodesStub{scaleInAdvanceErr: cluster.ErrNotLeader},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "admin",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.node",
						Actions:  []string{"r", "w"},
					}},
				}}),
				Management: tc.stub,
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusServiceUnavailable)
			}
			if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"service_unavailable"}`) {
				t.Fatalf("body = %s, want stable service_unavailable", rec.Body.String())
			}
		})
	}
}

func TestManagerScaleInCancelIsNotRegistered(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/cancel", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusNotFound)
	}
}

func TestManagerNodeSlotMoveOutPlanAndAdvance(t *testing.T) {
	generatedAt := time.Date(2026, 7, 2, 8, 0, 0, 0, time.UTC)
	var seenPlan managementusecase.NodeSlotMoveOutPlanRequest
	var seenAdvance managementusecase.NodeSlotMoveOutAdvanceRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			slotMoveOutPlanReqSink:    &seenPlan,
			slotMoveOutAdvanceReqSink: &seenAdvance,
			slotMoveOutPlan: managementusecase.NodeSlotMoveOutPlanResponse{
				NodeID:        1,
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				Candidates: []managementusecase.NodeSlotMoveOutCandidate{{
					SlotID:       9,
					SourceNodeID: 1,
					TargetNodeID: 4,
					DesiredPeers: []uint64{1, 2, 3},
					TargetPeers:  []uint64{4, 2, 3},
					ConfigEpoch:  7,
				}},
			},
			slotMoveOutAdvance: managementusecase.NodeSlotMoveOutAdvanceResponse{
				NodeID:        1,
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				Created:       1,
				Candidates: []managementusecase.NodeSlotMoveOutCandidate{{
					SlotID:       9,
					SourceNodeID: 1,
					TargetNodeID: 4,
					DesiredPeers: []uint64{1, 2, 3},
					TargetPeers:  []uint64{4, 2, 3},
					ConfigEpoch:  7,
				}},
			},
		},
	})

	planRec := httptest.NewRecorder()
	planReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/slot-move-out/plan", strings.NewReader(`{"max_slot_moves":2}`))
	planReq.Header.Set("Content-Type", "application/json")
	planReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(planRec, planReq)
	if planRec.Code != http.StatusOK {
		t.Fatalf("plan status = %d body=%s, want %d", planRec.Code, planRec.Body.String(), http.StatusOK)
	}
	if seenPlan.NodeID != 1 || seenPlan.MaxSlotMoves != 2 {
		t.Fatalf("plan request = %#v, want node 1 max 2", seenPlan)
	}
	if !jsonEqual(planRec.Body.String(), `{
		"generated_at": "2026-07-02T08:00:00Z",
		"state_revision": 22,
		"node_id": 1,
		"candidates": [{
			"slot_id": 9,
			"source_node_id": 1,
			"target_node_id": 4,
			"desired_peers": [1,2,3],
			"target_peers": [4,2,3],
			"config_epoch": 7
		}],
		"blocked_by_status": false
	}`) {
		t.Fatalf("plan body = %s", planRec.Body.String())
	}

	advanceRec := httptest.NewRecorder()
	advanceReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/slot-move-out/advance", strings.NewReader(`{"max_slot_moves":1}`))
	advanceReq.Header.Set("Content-Type", "application/json")
	advanceReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(advanceRec, advanceReq)
	if advanceRec.Code != http.StatusAccepted {
		t.Fatalf("advance status = %d body=%s, want %d", advanceRec.Code, advanceRec.Body.String(), http.StatusAccepted)
	}
	if seenAdvance.NodeID != 1 || seenAdvance.MaxSlotMoves != 1 {
		t.Fatalf("advance request = %#v, want node 1 max 1", seenAdvance)
	}
	if !strings.Contains(advanceRec.Body.String(), `"created":1`) || !strings.Contains(advanceRec.Body.String(), `"source_node_id":1`) {
		t.Fatalf("advance body = %s, want created move-out response", advanceRec.Body.String())
	}
}
