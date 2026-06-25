package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
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
			stub:   managerNodesStub{scaleInStatusErr: clusterv2.ErrNotStarted},
		},
		{
			name:   "advance",
			method: http.MethodPost,
			path:   "/manager/nodes/4/scale-in/advance",
			body:   `{"max_slot_moves":1}`,
			stub:   managerNodesStub{scaleInAdvanceErr: clusterv2.ErrNotLeader},
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
