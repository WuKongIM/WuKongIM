package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestManagerLoginIssuesJWTForAuthorizedUser(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body loginResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "admin", body.Username)
	require.Equal(t, "Bearer", body.TokenType)
	require.NotEmpty(t, body.AccessToken)
	require.Equal(t, int64(time.Hour/time.Second), body.ExpiresIn)
	require.WithinDuration(t, time.Now().Add(time.Hour), body.ExpiresAt, 2*time.Second)
	require.Equal(t, []permissionBody{{
		Resource: "cluster.node",
		Actions:  []string{"r"},
	}}, body.Permissions)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	_, hasLegacyToken := raw["token"]
	require.False(t, hasLegacyToken)
}

func TestManagerLoginRejectsInvalidCredentials(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"bad"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"invalid_credentials","message":"invalid credentials"}`, rec.Body.String())
}

func TestManagerCORSEchoesRequestOrigin(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/manager/nodes", nil)
	req.Header.Set("Origin", "http://localhost:5175")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "http://localhost:5175", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodGet)
}

func TestManagerNodesRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerNodesRejectsExpiredToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueExpiredTestToken(t, srv, "ghost"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerLoginRejectsUnknownUserEvenWithValidToken(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "ghost"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerNodesRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodesReturnsAggregatedInventory(t *testing.T) {
	generatedAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	lastHeartbeatAt := time.Date(2026, 4, 28, 9, 59, 58, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			nodes: nodeListAt(generatedAt, 1, managementusecase.Node{
				NodeID:          1,
				Name:            "node-1",
				Addr:            "127.0.0.1:7000",
				Status:          "alive",
				LastHeartbeatAt: lastHeartbeatAt,
				ControllerRole:  "leader",
				SlotCount:       3,
				LeaderSlotCount: 2,
				IsLocal:         true,
				CapacityWeight:  1,
				Membership: managementusecase.NodeMembership{
					Role:        "data",
					JoinState:   "active",
					Schedulable: true,
				},
				Health: managementusecase.NodeHealth{
					Status:          "alive",
					LastHeartbeatAt: lastHeartbeatAt,
				},
				Controller: managementusecase.NodeController{
					Role:          "leader",
					Voter:         true,
					LeaderID:      1,
					RaftHealth:    managementusecase.ControllerRaftHealthHealthy,
					FirstIndex:    10,
					AppliedIndex:  20,
					SnapshotIndex: 9,
				},
				Slots: managementusecase.NodeSlotSummary{
					ReplicaCount:  3,
					LeaderCount:   2,
					FollowerCount: 1,
				},
				Runtime: managementusecase.NodeRuntimeSummary{
					NodeID:               1,
					ActiveOnline:         4,
					ClosingOnline:        1,
					TotalOnline:          5,
					GatewaySessions:      6,
					SessionsByListener:   map[string]int{},
					AcceptingNewSessions: true,
				},
				Actions: managementusecase.NodeActions{
					CanDrain:   true,
					CanScaleIn: true,
				},
			}),
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-04-28T10:00:00Z",
		"controller_leader_id": 1,
		"total": 1,
		"items": [{
			"node_id": 1,
			"name": "node-1",
			"addr": "127.0.0.1:7000",
			"status": "alive",
			"last_heartbeat_at": "2026-04-28T09:59:58Z",
			"is_local": true,
			"capacity_weight": 1,
			"membership": {
				"role": "data",
				"join_state": "active",
				"schedulable": true
			},
			"health": {
				"status": "alive",
				"last_heartbeat_at": "2026-04-28T09:59:58Z"
			},
			"controller": {
				"role": "leader",
				"voter": true,
				"leader_id": 1,
				"raft_health": "healthy",
				"first_index": 10,
				"applied_index": 20,
				"snapshot_index": 9
			},
			"slot_stats": {
				"count": 3,
				"leader_count": 2
			},
			"slots": {
				"replica_count": 3,
				"leader_count": 2,
				"follower_count": 1,
				"quorum_lost_count": 0,
				"unreported_count": 0
			},
			"runtime": {
				"node_id": 1,
				"active_online": 4,
				"closing_online": 1,
				"total_online": 5,
				"gateway_sessions": 6,
				"sessions_by_listener": {},
				"accepting_new_sessions": true,
				"draining": false,
				"unknown": false
			},
			"actions": {
				"can_drain": true,
				"can_resume": false,
				"can_scale_in": true,
				"can_onboard": false
			}
		}]
	}`, rec.Body.String())
}

func TestManagerNodesReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{nodesErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerNodeDetailRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerNodeDetailReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{nodeDetailErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"node not found"}`, rec.Body.String())
}

func TestManagerNodeDetailReturnsAggregatedDetail(t *testing.T) {
	lastHeartbeatAt := time.Date(2026, 4, 21, 15, 4, 5, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			nodeDetail: managementusecase.NodeDetail{
				Node: managementusecase.Node{
					NodeID:          2,
					Name:            "node-2",
					Addr:            "127.0.0.1:7002",
					Status:          "draining",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 2,
					IsLocal:         true,
					CapacityWeight:  2,
					Membership: managementusecase.NodeMembership{
						Role:        "data",
						JoinState:   "active",
						Schedulable: false,
					},
					Health: managementusecase.NodeHealth{
						Status:          "draining",
						LastHeartbeatAt: lastHeartbeatAt,
					},
					Controller: managementusecase.NodeController{
						Role:          "follower",
						Voter:         true,
						LeaderID:      1,
						RaftHealth:    managementusecase.ControllerRaftHealthAppendCatchup,
						FirstIndex:    12,
						AppliedIndex:  18,
						SnapshotIndex: 11,
					},
					Slots: managementusecase.NodeSlotSummary{
						ReplicaCount:    3,
						LeaderCount:     2,
						FollowerCount:   1,
						QuorumLostCount: 1,
					},
					Runtime: managementusecase.NodeRuntimeSummary{
						NodeID:             2,
						ActiveOnline:       4,
						TotalOnline:        4,
						GatewaySessions:    5,
						SessionsByListener: map[string]int{"tcp": 5},
						Draining:           true,
					},
					Actions: managementusecase.NodeActions{
						CanResume: true,
					},
				},
				Slots: managementusecase.NodeSlots{
					HostedIDs: []uint32{2, 4, 7},
					LeaderIDs: []uint32{2, 4},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"node_id": 2,
		"name": "node-2",
		"addr": "127.0.0.1:7002",
		"status": "draining",
		"last_heartbeat_at": "2026-04-21T15:04:05Z",
		"is_local": true,
		"capacity_weight": 2,
		"membership": {
			"role": "data",
			"join_state": "active",
			"schedulable": false
		},
		"health": {
			"status": "draining",
			"last_heartbeat_at": "2026-04-21T15:04:05Z"
		},
		"controller": {
			"role": "follower",
			"voter": true,
			"leader_id": 1,
			"raft_health": "append_catchup",
			"first_index": 12,
			"applied_index": 18,
			"snapshot_index": 11
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 2
		},
		"runtime": {
			"node_id": 2,
			"active_online": 4,
			"closing_online": 0,
			"total_online": 4,
			"gateway_sessions": 5,
			"sessions_by_listener": {"tcp": 5},
			"accepting_new_sessions": false,
			"draining": true,
			"unknown": false
		},
		"actions": {
			"can_drain": false,
			"can_resume": true,
			"can_scale_in": false,
			"can_onboard": false
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": [2, 4],
			"replica_count": 3,
			"leader_count": 2,
			"follower_count": 1,
			"quorum_lost_count": 1,
			"unreported_count": 0
		}
	}`, rec.Body.String())
}

func TestManagerNodeDetailReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{nodeDetailErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerNodeDrainingRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/draining", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerNodeDrainingRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodeDrainingRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/bad/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerNodeDrainingReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{nodeDrainingErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"node not found"}`, rec.Body.String())
}

func TestManagerNodeDrainingReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{nodeDrainingErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader unavailable"}`, rec.Body.String())
}

func TestManagerNodeDrainingReturnsUpdatedNodeDetail(t *testing.T) {
	lastHeartbeatAt := time.Date(2026, 4, 22, 2, 4, 5, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			nodeDraining: managementusecase.NodeDetail{
				Node: managementusecase.Node{
					NodeID:          2,
					Name:            "node-2",
					Addr:            "127.0.0.1:7002",
					Status:          "draining",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 0,
					IsLocal:         false,
					CapacityWeight:  2,
					Membership: managementusecase.NodeMembership{
						Role:        "data",
						JoinState:   "active",
						Schedulable: false,
					},
					Health: managementusecase.NodeHealth{
						Status:          "draining",
						LastHeartbeatAt: lastHeartbeatAt,
					},
					Controller: managementusecase.NodeController{
						Role:     "follower",
						Voter:    true,
						LeaderID: 1,
					},
					Slots: managementusecase.NodeSlotSummary{
						ReplicaCount:  3,
						FollowerCount: 3,
					},
					Runtime: managementusecase.NodeRuntimeSummary{
						NodeID:             2,
						SessionsByListener: map[string]int{},
						Draining:           true,
					},
					Actions: managementusecase.NodeActions{
						CanResume: true,
					},
				},
				Slots: managementusecase.NodeSlots{
					HostedIDs: []uint32{2, 4, 7},
					LeaderIDs: []uint32{},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"node_id": 2,
		"name": "node-2",
		"addr": "127.0.0.1:7002",
		"status": "draining",
		"last_heartbeat_at": "2026-04-22T02:04:05Z",
		"is_local": false,
		"capacity_weight": 2,
		"membership": {
			"role": "data",
			"join_state": "active",
			"schedulable": false
		},
		"health": {
			"status": "draining",
			"last_heartbeat_at": "2026-04-22T02:04:05Z"
		},
		"controller": {
			"role": "follower",
			"voter": true,
			"leader_id": 1,
			"raft_health": "",
			"first_index": 0,
			"applied_index": 0,
			"snapshot_index": 0
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 0
		},
		"runtime": {
			"node_id": 2,
			"active_online": 0,
			"closing_online": 0,
			"total_online": 0,
			"gateway_sessions": 0,
			"sessions_by_listener": {},
			"accepting_new_sessions": false,
			"draining": true,
			"unknown": false
		},
		"actions": {
			"can_drain": false,
			"can_resume": true,
			"can_scale_in": false,
			"can_onboard": false
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": [],
			"replica_count": 3,
			"leader_count": 0,
			"follower_count": 3,
			"quorum_lost_count": 0,
			"unreported_count": 0
		}
	}`, rec.Body.String())
}

func TestManagerNodeResumeRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/resume", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerNodeResumeRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/resume", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodeResumeRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/bad/resume", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerNodeResumeReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{nodeResumeErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/resume", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"node not found"}`, rec.Body.String())
}

func TestManagerNodeResumeReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{nodeResumeErr: raftcluster.ErrNotLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/resume", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader unavailable"}`, rec.Body.String())
}

func TestManagerNodeResumeReturnsUpdatedNodeDetail(t *testing.T) {
	lastHeartbeatAt := time.Date(2026, 4, 22, 2, 5, 6, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			nodeResume: managementusecase.NodeDetail{
				Node: managementusecase.Node{
					NodeID:          2,
					Name:            "node-2",
					Addr:            "127.0.0.1:7002",
					Status:          "alive",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 0,
					IsLocal:         false,
					CapacityWeight:  2,
					Membership: managementusecase.NodeMembership{
						Role:        "data",
						JoinState:   "active",
						Schedulable: true,
					},
					Health: managementusecase.NodeHealth{
						Status:          "alive",
						LastHeartbeatAt: lastHeartbeatAt,
					},
					Controller: managementusecase.NodeController{
						Role:     "follower",
						Voter:    true,
						LeaderID: 1,
					},
					Slots: managementusecase.NodeSlotSummary{
						ReplicaCount:  3,
						FollowerCount: 3,
					},
					Runtime: managementusecase.NodeRuntimeSummary{
						NodeID:               2,
						SessionsByListener:   map[string]int{},
						AcceptingNewSessions: true,
					},
					Actions: managementusecase.NodeActions{
						CanDrain:   true,
						CanScaleIn: true,
					},
				},
				Slots: managementusecase.NodeSlots{
					HostedIDs: []uint32{2, 4, 7},
					LeaderIDs: []uint32{},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/resume", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"node_id": 2,
		"name": "node-2",
		"addr": "127.0.0.1:7002",
		"status": "alive",
		"last_heartbeat_at": "2026-04-22T02:05:06Z",
		"is_local": false,
		"capacity_weight": 2,
		"membership": {
			"role": "data",
			"join_state": "active",
			"schedulable": true
		},
		"health": {
			"status": "alive",
			"last_heartbeat_at": "2026-04-22T02:05:06Z"
		},
		"controller": {
			"role": "follower",
			"voter": true,
			"leader_id": 1,
			"raft_health": "",
			"first_index": 0,
			"applied_index": 0,
			"snapshot_index": 0
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 0
		},
		"runtime": {
			"node_id": 2,
			"active_online": 0,
			"closing_online": 0,
			"total_online": 0,
			"gateway_sessions": 0,
			"sessions_by_listener": {},
			"accepting_new_sessions": true,
			"draining": false,
			"unknown": false
		},
		"actions": {
			"can_drain": true,
			"can_resume": false,
			"can_scale_in": true,
			"can_onboard": false
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": [],
			"replica_count": 3,
			"leader_count": 0,
			"follower_count": 3,
			"quorum_lost_count": 0,
			"unreported_count": 0
		}
	}`, rec.Body.String())
}

func TestManagerSlotsRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotsRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotsReturnsAggregatedList(t *testing.T) {
	lastReportAt := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			slots: []managementusecase.Slot{{
				SlotID: 2,
				HashSlots: &managementusecase.SlotHashSlots{
					Count: 4,
					Items: []uint16{4, 5, 6, 7},
				},
				State: managementusecase.SlotState{
					Quorum:                "ready",
					Sync:                  "matched",
					LeaderMatch:           false,
					LeaderTransferPending: true,
				},
				Assignment: managementusecase.SlotAssignment{
					DesiredPeers:    []uint64{1, 2, 3},
					PreferredLeader: 2,
					ConfigEpoch:     8,
					BalanceVersion:  3,
				},
				Runtime: managementusecase.SlotRuntime{
					CurrentPeers:        []uint64{1, 2, 3},
					CurrentVoters:       []uint64{1, 2, 3},
					LeaderID:            1,
					HealthyVoters:       3,
					HasQuorum:           true,
					ObservedConfigEpoch: 8,
					LastReportAt:        lastReportAt,
				},
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"slot_id": 2,
			"hash_slots": {
				"count": 4,
				"items": [4, 5, 6, 7]
			},
			"state": {
				"quorum": "ready",
				"sync": "matched",
				"leader_match": false,
				"leader_transfer_pending": true
			},
			"assignment": {
				"desired_peers": [1, 2, 3],
				"preferred_leader_id": 2,
				"config_epoch": 8,
				"balance_version": 3
			},
			"runtime": {
				"current_peers": [1, 2, 3],
				"current_voters": [1, 2, 3],
				"leader_id": 1,
				"healthy_voters": 3,
				"has_quorum": true,
				"observed_config_epoch": 8,
				"last_report_at": "2026-04-21T16:00:00Z"
			}
		}]
	}`, rec.Body.String())
}

func TestManagerSlotsFiltersByNodeAndReturnsNodeLogStatus(t *testing.T) {
	lastReportAt := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	var listOptions managementusecase.ListSlotsOptions
	stub := managementStub{
		slots: []managementusecase.Slot{{
			SlotID: 2,
			State: managementusecase.SlotState{
				Quorum: "ready",
				Sync:   "matched",
			},
			Assignment: managementusecase.SlotAssignment{
				DesiredPeers: []uint64{1, 2, 3},
				ConfigEpoch:  8,
			},
			Runtime: managementusecase.SlotRuntime{
				CurrentPeers:        []uint64{1, 2, 3},
				CurrentVoters:       []uint64{1, 2, 3},
				LeaderID:            1,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        lastReportAt,
			},
			NodeLog: &managementusecase.SlotNodeLogStatus{
				NodeID:       2,
				LeaderID:     1,
				CommitIndex:  93,
				AppliedIndex: 91,
			},
		}},
		listSlotsOptionsSink: &listOptions,
	}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: stub,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListSlotsOptions{NodeID: 2}, listOptions)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"slot_id": 2,
			"state": {
				"quorum": "ready",
				"sync": "matched",
				"leader_match": false,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": [1, 2, 3],
				"preferred_leader_id": 0,
				"config_epoch": 8,
				"balance_version": 0
			},
			"runtime": {
				"current_peers": [1, 2, 3],
				"current_voters": [1, 2, 3],
				"leader_id": 1,
				"healthy_voters": 3,
				"has_quorum": true,
				"observed_config_epoch": 8,
				"last_report_at": "2026-04-21T16:00:00Z"
			},
			"node_log": {
				"node_id": 2,
				"leader_id": 1,
				"commit_index": 93,
				"applied_index": 91
			}
		}]
	}`, rec.Body.String())
}

func TestManagerControllerLogsReturnsNodeScopedEntries(t *testing.T) {
	var reqSink managementusecase.ListControllerLogEntriesRequest
	stub := managementStub{
		controllerLogEntriesReqSink: &reqSink,
		controllerLogEntriesPage: managementusecase.ControllerLogEntriesResponse{
			NodeID:       2,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []managementusecase.ControllerLogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				DataSize:     12,
				DecodeStatus: "ok",
				DecodedType:  "add_slot",
				Decoded: map[string]any{
					"command":     "add_slot",
					"new_slot_id": uint64(9),
				},
			}},
		},
	}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: stub,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/logs?node_id=2&limit=2&cursor=5", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListControllerLogEntriesRequest{NodeID: 2, Limit: 2, Cursor: 5}, reqSink)
	require.JSONEq(t, `{
		"node_id": 2,
		"first_index": 1,
		"last_index": 4,
		"commit_index": 4,
		"applied_index": 3,
		"next_cursor": 3,
		"items": [{
			"index": 4,
			"term": 2,
			"type": "normal",
			"data_size": 12,
			"decode_status": "ok",
			"decoded_type": "add_slot",
			"decoded": {"command": "add_slot", "new_slot_id": 9}
		}]
	}`, rec.Body.String())
}

func TestManagerNodeControllerRaftReturnsStatus(t *testing.T) {
	compactedAt := time.Date(2026, 5, 6, 8, 1, 0, 0, time.UTC)
	checkedAt := time.Date(2026, 5, 6, 8, 2, 0, 0, time.UTC)
	compactionErrorAt := time.Date(2026, 5, 6, 8, 3, 0, 0, time.UTC)
	restoredAt := time.Date(2026, 5, 6, 8, 4, 0, 0, time.UTC)
	restoreErrorAt := time.Date(2026, 5, 6, 8, 5, 0, 0, time.UTC)
	var nodeIDSink uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}, {
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			controllerRaftStatusNodeIDSink: &nodeIDSink,
			controllerRaftStatus: managementusecase.ControllerRaftStatusResponse{
				NodeID:        2,
				Role:          "leader",
				LeaderID:      2,
				Term:          7,
				Health:        managementusecase.ControllerRaftHealthRestoreFailed,
				FirstIndex:    10,
				LastIndex:     42,
				CommitIndex:   40,
				AppliedIndex:  39,
				SnapshotIndex: 9,
				SnapshotTerm:  3,
				Compaction: managementusecase.ControllerRaftCompactionStatus{
					Enabled:           true,
					TriggerEntries:    100,
					CheckInterval:     2 * time.Second,
					LastSnapshotIndex: 9,
					LastSnapshotAt:    compactedAt,
					LastCheckAt:       checkedAt,
					LastError:         "disk full",
					LastErrorAt:       compactionErrorAt,
					Degraded:          true,
				},
				Restore: managementusecase.ControllerRaftRestoreStatus{
					LastSnapshotIndex: 8,
					LastSnapshotTerm:  2,
					LastRestoredAt:    restoredAt,
					LastError:         "crc mismatch",
					LastErrorAt:       restoreErrorAt,
					Failed:            true,
				},
				Peers: []managementusecase.ControllerRaftPeerProgress{{
					NodeID:               3,
					Match:                21,
					Next:                 22,
					State:                "snapshot",
					PendingSnapshot:      9,
					RecentActive:         true,
					NeedsSnapshot:        true,
					SnapshotTransferring: true,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2), nodeIDSink)
	require.JSONEq(t, `{
		"node_id": 2,
		"role": "leader",
		"leader_id": 2,
		"term": 7,
		"health": "restore_failed",
		"first_index": 10,
		"last_index": 42,
		"commit_index": 40,
		"applied_index": 39,
		"snapshot_index": 9,
		"snapshot_term": 3,
		"compaction": {
			"enabled": true,
			"trigger_entries": 100,
			"check_interval_ms": 2000,
			"last_snapshot_index": 9,
			"last_snapshot_at": "2026-05-06T08:01:00Z",
			"last_check_at": "2026-05-06T08:02:00Z",
			"last_error": "disk full",
			"last_error_at": "2026-05-06T08:03:00Z",
			"degraded": true
		},
		"restore": {
			"last_snapshot_index": 8,
			"last_snapshot_term": 2,
			"last_restored_at": "2026-05-06T08:04:00Z",
			"last_error": "crc mismatch",
			"last_error_at": "2026-05-06T08:05:00Z",
			"failed": true
		},
		"peers": [{
			"node_id": 3,
			"match": 21,
			"next": 22,
			"state": "snapshot",
			"pending_snapshot": 9,
			"recent_active": true,
			"needs_snapshot": true,
			"snapshot_transferring": true
		}]
	}`, rec.Body.String())
}

func TestManagerNodeControllerRaftRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}, {
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/bad/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerNodeControllerRaftReturnsServiceUnavailableWhenManagementMissing(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}, {
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"management not configured"}`, rec.Body.String())
}

func TestManagerNodeControllerRaftReturnsServiceUnavailableWhenStatusUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "no leader", err: raftcluster.ErrNoLeader},
		{name: "target not found", err: transport.ErrNodeNotFound},
		{name: "target stopped", err: transport.ErrStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "admin",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.node",
						Actions:  []string{"r"},
					}, {
						Resource: "cluster.controller",
						Actions:  []string{"r"},
					}},
				}}),
				Management: managementStub{controllerRaftStatusErr: tc.err},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.JSONEq(t, `{"error":"service_unavailable","message":"controller raft status unavailable"}`, rec.Body.String())
		})
	}
}

func TestManagerNodeControllerRaftRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerControllerRaftCompactReturnsFanoutResult(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			controllerRaftCompaction: managementusecase.CompactControllerRaftLogsResponse{
				GeneratedAt: generatedAt,
				Total:       2,
				Succeeded:   1,
				Failed:      1,
				Items: []managementusecase.ControllerRaftCompactionNodeResult{{
					NodeID:              1,
					Success:             true,
					AppliedIndex:        42,
					BeforeSnapshotIndex: 30,
					AfterSnapshotIndex:  42,
					Compacted:           true,
				}, {
					NodeID:  2,
					Success: false,
					Error:   "node stopped",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-05-07T10:00:00Z",
		"total": 2,
		"succeeded": 1,
		"failed": 1,
		"items": [{
			"node_id": 1,
			"success": true,
			"applied_index": 42,
			"before_snapshot_index": 30,
			"after_snapshot_index": 42,
			"compacted": true,
			"skipped_reason": "",
			"error": ""
		}, {
			"node_id": 2,
			"success": false,
			"applied_index": 0,
			"before_snapshot_index": 0,
			"after_snapshot_index": 0,
			"compacted": false,
			"skipped_reason": "",
			"error": "node stopped"
		}]
	}`, rec.Body.String())
}

func TestManagerControllerRaftCompactRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodeControllerRaftCompactReturnsNodeResult(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 3, 0, 0, time.UTC)
	var gotNodeID uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			controllerRaftCompactionNodeIDSink: &gotNodeID,
			controllerRaftNodeCompaction: managementusecase.CompactControllerRaftLogsResponse{
				GeneratedAt: generatedAt,
				Total:       1,
				Succeeded:   1,
				Failed:      0,
				Items: []managementusecase.ControllerRaftCompactionNodeResult{{
					NodeID:              2,
					Success:             true,
					AppliedIndex:        50,
					BeforeSnapshotIndex: 40,
					AfterSnapshotIndex:  50,
					Compacted:           true,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, uint64(2), gotNodeID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-05-07T10:03:00Z",
		"total": 1,
		"succeeded": 1,
		"failed": 0,
		"items": [{
			"node_id": 2,
			"success": true,
			"applied_index": 50,
			"before_snapshot_index": 40,
			"after_snapshot_index": 50,
			"compacted": true,
			"skipped_reason": "",
			"error": ""
		}]
	}`, rec.Body.String())
}

func TestManagerNodeControllerRaftCompactRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodeSlotRaftCompactReturnsNodeSlotResult(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 11, 3, 0, 0, time.UTC)
	var gotNodeID uint64
	var gotSlotID uint32
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}, {
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			slotRaftCompactionNodeIDSink: &gotNodeID,
			slotRaftCompactionSlotIDSink: &gotSlotID,
			slotRaftCompaction: managementusecase.CompactSlotRaftLogResponse{
				GeneratedAt: generatedAt,
				Total:       1,
				Succeeded:   1,
				Items: []managementusecase.SlotRaftCompactionNodeResult{{
					NodeID:              2,
					SlotID:              9,
					Success:             true,
					AppliedIndex:        50,
					BeforeSnapshotIndex: 40,
					AfterSnapshotIndex:  50,
					Compacted:           true,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/slots/9/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, uint64(2), gotNodeID)
	require.Equal(t, uint32(9), gotSlotID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-05-07T11:03:00Z",
		"total": 1,
		"succeeded": 1,
		"failed": 0,
		"items": [{
			"node_id": 2,
			"slot_id": 9,
			"success": true,
			"applied_index": 50,
			"before_snapshot_index": 40,
			"after_snapshot_index": 50,
			"compacted": true,
			"skipped_reason": "",
			"error": ""
		}]
	}`, rec.Body.String())
}

func TestManagerNodeSlotRaftCompactRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/slots/9/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotLogsReturnsNodeScopedEntries(t *testing.T) {
	var reqSink managementusecase.ListSlotLogEntriesRequest
	stub := managementStub{
		slotLogEntriesReqSink: &reqSink,
		slotLogEntriesPage: managementusecase.SlotLogEntriesResponse{
			NodeID:       2,
			SlotID:       9,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []managementusecase.SlotLogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				DataSize:     12,
				DecodeStatus: "ok",
				DecodedType:  "upsert_user",
				Decoded: map[string]any{
					"command":      "upsert_user",
					"uid":          "u1",
					"token":        "***",
					"device_flag":  int64(3),
					"device_level": int64(7),
				},
			}, {
				Index:    3,
				Term:     2,
				Type:     "conf_change",
				DataSize: 8,
			}},
		},
	}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: stub,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/9/logs?node_id=2&limit=2&cursor=5", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListSlotLogEntriesRequest{
		NodeID: 2,
		SlotID: 9,
		Limit:  2,
		Cursor: 5,
	}, reqSink)
	require.JSONEq(t, `{
		"node_id": 2,
		"slot_id": 9,
		"first_index": 1,
		"last_index": 4,
		"commit_index": 4,
		"applied_index": 3,
		"next_cursor": 3,
		"items": [{
			"index": 4,
			"term": 2,
			"type": "normal",
			"data_size": 12,
			"decode_status": "ok",
			"decoded_type": "upsert_user",
			"decoded": {
				"command": "upsert_user",
				"uid": "u1",
				"token": "***",
				"device_flag": 3,
				"device_level": 7
			}
		}, {
			"index": 3,
			"term": 2,
			"type": "conf_change",
			"data_size": 8
		}]
	}`, rec.Body.String())
}

func TestManagerSlotsRejectsInvalidNodeFilter(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots?node_id=bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerSlotsReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{slotsErr: context.DeadlineExceeded},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerSlotDetailRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotDetailRejectsInvalidSlotID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid slot_id"}`, rec.Body.String())
}

func TestManagerSlotDetailRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotDetailReturnsDetailWithTaskSummary(t *testing.T) {
	nextRunAt := time.Date(2026, 4, 21, 16, 5, 0, 0, time.UTC)
	lastReportAt := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			slotDetail: managementusecase.SlotDetail{
				Slot: managementusecase.Slot{
					SlotID: 2,
					State: managementusecase.SlotState{
						Quorum:      "ready",
						Sync:        "matched",
						LeaderMatch: true,
					},
					Assignment: managementusecase.SlotAssignment{
						DesiredPeers:    []uint64{2, 3, 5},
						PreferredLeader: 2,
						ConfigEpoch:     8,
						BalanceVersion:  3,
					},
					Runtime: managementusecase.SlotRuntime{
						CurrentPeers:        []uint64{2, 3, 5},
						CurrentVoters:       []uint64{2, 3, 5},
						LeaderID:            2,
						HealthyVoters:       3,
						HasQuorum:           true,
						ObservedConfigEpoch: 8,
						LastReportAt:        lastReportAt,
					},
				},
				Task: &managementusecase.Task{
					SlotID:     2,
					Kind:       "repair",
					Step:       "catch_up",
					Status:     "retrying",
					SourceNode: 3,
					TargetNode: 5,
					Attempt:    1,
					NextRunAt:  &nextRunAt,
					LastError:  "learner catch-up timeout",
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"slot_id": 2,
		"state": {
			"quorum": "ready",
			"sync": "matched",
			"leader_match": true,
			"leader_transfer_pending": false
		},
		"assignment": {
			"desired_peers": [2, 3, 5],
			"preferred_leader_id": 2,
			"config_epoch": 8,
			"balance_version": 3
		},
		"runtime": {
			"current_peers": [2, 3, 5],
			"current_voters": [2, 3, 5],
			"leader_id": 2,
			"healthy_voters": 3,
			"has_quorum": true,
			"observed_config_epoch": 8,
			"last_report_at": "2026-04-21T16:00:00Z"
		},
		"task": {
			"kind": "repair",
			"step": "catch_up",
			"status": "retrying",
			"source_node": 3,
			"target_node": 5,
			"attempt": 1,
			"next_run_at": "2026-04-21T16:05:00Z",
			"last_error": "learner catch-up timeout"
		}
	}`, rec.Body.String())
}

func TestManagerSlotDetailReturnsDetailWithNullTask(t *testing.T) {
	lastReportAt := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			slotDetail: managementusecase.SlotDetail{
				Slot: managementusecase.Slot{
					SlotID: 7,
					State: managementusecase.SlotState{
						Quorum: "lost",
						Sync:   "unreported",
					},
					Runtime: managementusecase.SlotRuntime{
						CurrentPeers:        []uint64{7, 8, 9},
						CurrentVoters:       []uint64{7, 8, 9},
						LeaderID:            8,
						HealthyVoters:       2,
						HasQuorum:           false,
						ObservedConfigEpoch: 0,
						LastReportAt:        lastReportAt,
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/7", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"slot_id": 7,
			"state": {
				"quorum": "lost",
				"sync": "unreported",
				"leader_match": false,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": null,
				"preferred_leader_id": 0,
				"config_epoch": 0,
				"balance_version": 0
			},
			"runtime": {
				"current_peers": [7, 8, 9],
				"current_voters": [7, 8, 9],
				"leader_id": 8,
				"healthy_voters": 2,
				"has_quorum": false,
			"observed_config_epoch": 0,
			"last_report_at": "2026-04-21T16:00:00Z"
		},
		"task": null
	}`, rec.Body.String())
}

func TestManagerSlotDetailReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{slotDetailErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"slot not found"}`, rec.Body.String())
}

func TestManagerSlotDetailReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{slotDetailErr: context.DeadlineExceeded},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerTasksRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerTasksRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerTasksReturnsAggregatedList(t *testing.T) {
	nextRunAt := time.Date(2026, 4, 21, 16, 5, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			tasks: []managementusecase.Task{{
				SlotID:     2,
				Kind:       "repair",
				Step:       "catch_up",
				Status:     "retrying",
				SourceNode: 3,
				TargetNode: 5,
				Attempt:    1,
				NextRunAt:  &nextRunAt,
				LastError:  "learner catch-up timeout",
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"slot_id": 2,
			"kind": "repair",
			"step": "catch_up",
			"status": "retrying",
			"source_node": 3,
			"target_node": 5,
			"attempt": 1,
			"next_run_at": "2026-04-21T16:05:00Z",
			"last_error": "learner catch-up timeout"
		}]
	}`, rec.Body.String())
}

func TestManagerTasksReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{tasksErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerTaskDetailRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerTaskDetailRejectsInvalidSlotID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid slot_id"}`, rec.Body.String())
}

func TestManagerTaskDetailRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerTaskDetailReturnsTaskWithSlotContext(t *testing.T) {
	nextRunAt := time.Date(2026, 4, 21, 16, 5, 0, 0, time.UTC)
	lastReportAt := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			task: managementusecase.TaskDetail{
				Task: managementusecase.Task{
					SlotID:     2,
					Kind:       "repair",
					Step:       "catch_up",
					Status:     "retrying",
					SourceNode: 3,
					TargetNode: 5,
					Attempt:    1,
					NextRunAt:  &nextRunAt,
					LastError:  "learner catch-up timeout",
				},
				Slot: managementusecase.Slot{
					SlotID: 2,
					State: managementusecase.SlotState{
						Quorum:      "ready",
						Sync:        "matched",
						LeaderMatch: true,
					},
					Assignment: managementusecase.SlotAssignment{
						DesiredPeers:    []uint64{2, 3, 5},
						PreferredLeader: 2,
						ConfigEpoch:     8,
						BalanceVersion:  3,
					},
					Runtime: managementusecase.SlotRuntime{
						CurrentPeers:        []uint64{2, 3, 5},
						CurrentVoters:       []uint64{2, 3, 5},
						LeaderID:            2,
						HealthyVoters:       3,
						HasQuorum:           true,
						ObservedConfigEpoch: 8,
						LastReportAt:        lastReportAt,
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"slot_id": 2,
		"kind": "repair",
		"step": "catch_up",
		"status": "retrying",
		"source_node": 3,
		"target_node": 5,
		"attempt": 1,
		"next_run_at": "2026-04-21T16:05:00Z",
		"last_error": "learner catch-up timeout",
		"slot": {
			"state": {
				"quorum": "ready",
				"sync": "matched",
				"leader_match": true,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": [2, 3, 5],
				"preferred_leader_id": 2,
				"config_epoch": 8,
				"balance_version": 3
			},
			"runtime": {
				"current_peers": [2, 3, 5],
				"current_voters": [2, 3, 5],
				"leader_id": 2,
				"healthy_voters": 3,
				"has_quorum": true,
				"observed_config_epoch": 8,
				"last_report_at": "2026-04-21T16:00:00Z"
			}
		}
	}`, rec.Body.String())
}

func TestManagerTaskDetailReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{taskErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"task not found"}`, rec.Body.String())
}

func TestManagerTaskDetailReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{taskErr: context.DeadlineExceeded},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/tasks/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerDistributedTasksRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerDistributedTasksRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerDistributedTasksReturnsFilteredList(t *testing.T) {
	updated := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	var query managementusecase.DistributedTaskQuery
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			distributedTasksReqSink: &query,
			distributedTasks: managementusecase.DistributedTaskListResult{
				Total: 1,
				Items: []managementusecase.DistributedTask{{
					ID:         "slot-reconcile:1",
					Domain:     managementusecase.DistributedTaskDomainSlotReconcile,
					Kind:       "repair",
					Status:     managementusecase.DistributedTaskStatusRetrying,
					Phase:      "catch_up",
					Scope:      managementusecase.DistributedTaskScope{Type: managementusecase.DistributedTaskScopeSlot, ID: "1", SlotID: 1},
					TargetNode: 3,
					UpdatedAt:  &updated,
					Links:      map[string]string{"slot": "/slots?slot_id=1"},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks?domain=slot_reconcile&status=retrying&node_id=3&scope=slot&keyword=repair&limit=25", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.DistributedTaskDomainSlotReconcile, query.Domain)
	require.Equal(t, managementusecase.DistributedTaskStatusRetrying, query.Status)
	require.Equal(t, uint64(3), query.NodeID)
	require.Equal(t, managementusecase.DistributedTaskScopeSlot, query.Scope)
	require.Equal(t, "repair", query.Keyword)
	require.Equal(t, 25, query.Limit)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"id": "slot-reconcile:1",
			"domain": "slot_reconcile",
			"kind": "repair",
			"status": "retrying",
			"phase": "catch_up",
			"scope": {"type":"slot","id":"1","slot_id":1,"channel_id":"","channel_type":0,"node_id":0},
			"source_node": 0,
			"target_node": 3,
			"owner_node": 0,
			"attempt": 0,
			"next_run_at": null,
			"created_at": null,
			"updated_at": "2026-05-14T10:00:00Z",
			"last_error": "",
			"summary": "",
			"links": {"slot":"/slots?slot_id=1"}
		}],
		"next_cursor": "",
		"has_more": false,
		"partial": false,
		"warnings": []
	}`, rec.Body.String())
}

func TestManagerDistributedTasksSummaryReturnsCounts(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			distributedTaskSummary: managementusecase.DistributedTaskSummary{
				Total: 2,
				ByStatus: map[managementusecase.DistributedTaskStatus]int{
					managementusecase.DistributedTaskStatusRetrying: 1,
					managementusecase.DistributedTaskStatusRunning:  1,
				},
				ByDomain: map[managementusecase.DistributedTaskDomain]int{
					managementusecase.DistributedTaskDomainSlotReconcile:  1,
					managementusecase.DistributedTaskDomainNodeOnboarding: 1,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 2,
		"by_status": {"pending":0,"running":1,"retrying":1,"blocked":0,"failed":0,"completed":0,"cancelled":0,"unknown":0},
		"by_domain": {"slot_reconcile":1,"node_onboarding":1,"node_scale_in":0,"channel_migration":0},
		"partial": false,
		"warnings": []
	}`, rec.Body.String())
}

func TestManagerDistributedTaskDetailReturnsSourcePayload(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			distributedTask: managementusecase.DistributedTaskDetail{
				Task: managementusecase.DistributedTask{
					ID:     "slot-reconcile:2",
					Domain: managementusecase.DistributedTaskDomainSlotReconcile,
					Kind:   "repair",
					Status: managementusecase.DistributedTaskStatusRetrying,
					Scope:  managementusecase.DistributedTaskScope{Type: managementusecase.DistributedTaskScopeSlot, ID: "2", SlotID: 2},
				},
				Detail: managementusecase.DistributedTaskDetailPayload{
					Domain:    managementusecase.DistributedTaskDomainSlotReconcile,
					RawStatus: "retrying",
					Slot: &managementusecase.TaskDetail{
						Task: managementusecase.Task{SlotID: 2, Kind: "repair", Status: "retrying"},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks/slot_reconcile/slot-reconcile:2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"task": {
			"id":"slot-reconcile:2",
			"domain":"slot_reconcile",
			"kind":"repair",
			"status":"retrying",
			"phase":"",
			"scope":{"type":"slot","id":"2","slot_id":2,"channel_id":"","channel_type":0,"node_id":0},
			"source_node":0,
			"target_node":0,
			"owner_node":0,
			"attempt":0,
			"next_run_at":null,
			"created_at":null,
			"updated_at":null,
			"last_error":"",
			"summary":"",
			"links":{}
		},
		"detail": {
			"domain": "slot_reconcile",
			"raw_status": "retrying",
			"slot": {
				"slot_id":2,
				"kind":"repair",
				"step":"",
				"status":"retrying",
				"source_node":0,
				"target_node":0,
				"attempt":0,
				"next_run_at":null,
				"last_error":"",
				"slot":{"state":{"quorum":"","sync":"","leader_match":false,"leader_transfer_pending":false},"assignment":{"desired_peers":null,"preferred_leader_id":0,"config_epoch":0,"balance_version":0},"runtime":{"current_peers":null,"current_voters":null,"leader_id":0,"healthy_voters":0,"has_quorum":false,"observed_config_epoch":0,"last_report_at":"0001-01-01T00:00:00Z"}}
			}
		}
	}`, rec.Body.String())
}

func TestManagerDistributedTasksRejectsInvalidQuery(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks?status=bogus", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid distributed task query"}`, rec.Body.String())
}

func TestManagerDistributedTasksReturnsServiceUnavailableWhenAllSourcesFail(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.task",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{distributedTasksErr: managementusecase.ErrDistributedTasksUnavailable},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/distributed-tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"distributed task sources unavailable"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaRejectsInvalidLimit(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?limit=0", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid limit"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaRejectsInvalidCursor(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?cursor=not-base64", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid cursor"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaRejectsUnsupportedCursorVersion(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?cursor="+base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"slot_id":1}`)), nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid cursor"}`, rec.Body.String())
}

func TestEncodeChannelRuntimeMetaCursorWritesBinaryPayload(t *testing.T) {
	cursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 1}

	raw, err := encodeChannelRuntimeMetaCursor(cursor)
	require.NoError(t, err)
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.NotEmpty(t, payload)
	require.NotEqual(t, byte('{'), payload[0])

	decoded, err := decodeChannelRuntimeMetaCursor(raw)
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)
}

func TestDecodeChannelRuntimeMetaCursorAcceptsLegacyJSONPayload(t *testing.T) {
	cursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 1}

	decoded, err := decodeChannelRuntimeMetaCursor(mustEncodeLegacyChannelRuntimeMetaCursorForTest(t, cursor))
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)
}

func TestChannelRuntimeMetaCursorBinaryAllocationsStayBounded(t *testing.T) {
	cursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 1}
	raw, err := encodeChannelRuntimeMetaCursor(cursor)
	require.NoError(t, err)

	encodeAllocs := testing.AllocsPerRun(1000, func() {
		if _, err := encodeChannelRuntimeMetaCursor(cursor); err != nil {
			t.Fatal(err)
		}
	})
	decodeAllocs := testing.AllocsPerRun(1000, func() {
		decoded, err := decodeChannelRuntimeMetaCursor(raw)
		if err != nil {
			t.Fatal(err)
		}
		if decoded != cursor {
			t.Fatalf("decodeChannelRuntimeMetaCursor() = %+v, want %+v", decoded, cursor)
		}
	})

	require.LessOrEqual(t, encodeAllocs, float64(1))
	require.LessOrEqual(t, decodeAllocs, float64(1))
}

func TestManagerChannelRuntimeMetaReturnsPagedList(t *testing.T) {
	var received managementusecase.ListChannelRuntimeMetaRequest
	inputCursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 1}
	nextCursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 2, ChannelID: "g2", ChannelType: 2}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelRuntimeMetaReqSink: &received,
			channelRuntimeMetaPage: managementusecase.ListChannelRuntimeMetaResponse{
				Items: []managementusecase.ChannelRuntimeMeta{{
					ChannelID:     "g2",
					ChannelType:   2,
					SlotID:        2,
					ChannelEpoch:  11,
					LeaderEpoch:   5,
					Leader:        3,
					Replicas:      []uint64{3, 4},
					ISR:           []uint64{3},
					MinISR:        1,
					MaxMessageSeq: uint64Ptr(42),
					Status:        "active",
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?limit=2&cursor="+mustEncodeChannelRuntimeMetaCursorForTest(t, inputCursor), nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListChannelRuntimeMetaRequest{
		Limit:  2,
		Cursor: inputCursor,
	}, received)
	require.NotContains(t, rec.Body.String(), `"total"`)
	require.JSONEq(t, fmt.Sprintf(`{
		"items": [{
			"channel_id": "g2",
			"channel_type": 2,
			"slot_id": 2,
			"channel_epoch": 11,
			"leader_epoch": 5,
			"leader": 3,
			"replicas": [3, 4],
			"isr": [3],
			"min_isr": 1,
			"max_message_seq": 42,
			"status": "active"
		}],
		"has_more": true,
		"next_cursor": %q
	}`, mustEncodeChannelRuntimeMetaCursorForTest(t, nextCursor)), rec.Body.String())
}

func TestManagerChannelRuntimeMetaPassesChannelIDQuery(t *testing.T) {
	var received managementusecase.ListChannelRuntimeMetaRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{channelRuntimeMetaReqSink: &received},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?channel_id=%20room%20&limit=15", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListChannelRuntimeMetaRequest{
		Limit:          15,
		ChannelIDQuery: "room",
	}, received)
}

func TestManagerChannelRuntimeMetaPassesNodeFiltersAndIncludeMaxSeq(t *testing.T) {
	var received managementusecase.ListChannelRuntimeMetaRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{channelRuntimeMetaReqSink: &received},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?node_id=1&node_scope=replica&include_max_message_seq=true", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListChannelRuntimeMetaRequest{
		Limit:                defaultChannelRuntimeMetaLimit,
		NodeID:               1,
		NodeScope:            managementusecase.ChannelRuntimeMetaNodeScopeReplica,
		IncludeMaxMessageSeq: true,
	}, received)
}

func TestManagerChannelRuntimeMetaRejectsInvalidNodeFilters(t *testing.T) {
	tests := []string{
		"/manager/channel-runtime-meta?node_id=0",
		"/manager/channel-runtime-meta?node_id=bad",
		"/manager/channel-runtime-meta?node_id=1&node_scope=bad",
		"/manager/channel-runtime-meta?include_max_message_seq=maybe",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "admin",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.channel",
						Actions:  []string{"r"},
					}},
				}}),
				Management: managementStub{},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestManagerChannelRuntimeMetaOmitsMaxMessageSeqWhenNotIncluded(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelRuntimeMetaPage: managementusecase.ListChannelRuntimeMetaResponse{
				Items: []managementusecase.ChannelRuntimeMeta{{
					ChannelID:   "g2",
					ChannelType: 2,
					SlotID:      2,
					Status:      "active",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "max_message_seq")
}

func TestManagerChannelRuntimeMetaReturnsServiceUnavailableWhenAuthoritativeReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{channelRuntimeMetaErr: raftcluster.ErrSlotNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"slot leader authoritative read unavailable"}`, rec.Body.String())
}

func TestManagerChannelClusterSummaryReturnsAggregate(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelClusterSummary: managementusecase.ChannelClusterSummary{
				Total:           4,
				Healthy:         1,
				ISRInsufficient: 2,
				NoLeader:        1,
				AvgReplicas:     2,
				AvgISR:          1.5,
				LeaderDistribution: []managementusecase.ChannelLeaderDistribution{
					{NodeID: 1, Count: 3},
					{NodeID: 2, Count: 1},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 4,
		"healthy": 1,
		"isr_insufficient": 2,
		"no_leader": 1,
		"avg_replicas": 2,
		"avg_isr": 1.5,
		"leader_distribution": [
			{"node_id": 1, "count": 3},
			{"node_id": 2, "count": 1}
		]
	}`, rec.Body.String())
}

func TestManagerChannelClusterRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelClusterUnhealthyReturnsPagedList(t *testing.T) {
	var received managementusecase.ListChannelClusterUnhealthyRequest
	nextCursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 2, ChannelID: "g2", ChannelType: 2}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelClusterUnhealthyReqSink: &received,
			channelClusterUnhealthyPage: managementusecase.ListChannelClusterUnhealthyResponse{
				Items: []managementusecase.ChannelClusterUnhealthyItem{{
					ChannelRuntimeMeta: managementusecase.ChannelRuntimeMeta{
						ChannelID:     "g2",
						ChannelType:   2,
						SlotID:        2,
						ChannelEpoch:  11,
						LeaderEpoch:   5,
						Leader:        0,
						Replicas:      []uint64{3, 4},
						ISR:           []uint64{3},
						MinISR:        2,
						MaxMessageSeq: uint64Ptr(42),
						Status:        "active",
					},
					Reasons: []string{
						managementusecase.ChannelClusterUnhealthyReasonISRInsufficient,
						managementusecase.ChannelClusterUnhealthyReasonNoLeader,
					},
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/unhealthy?limit=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListChannelClusterUnhealthyRequest{Limit: 2}, received)
	require.JSONEq(t, fmt.Sprintf(`{
		"items": [{
			"channel_id": "g2",
			"channel_type": 2,
			"slot_id": 2,
			"channel_epoch": 11,
			"leader_epoch": 5,
			"leader": 0,
			"replicas": [3, 4],
			"isr": [3],
			"min_isr": 2,
			"max_message_seq": 42,
			"status": "active",
			"reasons": ["isr_insufficient", "no_leader"]
		}],
		"has_more": true,
		"next_cursor": %q
	}`, mustEncodeChannelRuntimeMetaCursorForTest(t, nextCursor)), rec.Body.String())
}

func TestManagerChannelClusterUnhealthyRejectsInvalidLimit(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/unhealthy?limit=0", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid limit"}`, rec.Body.String())
}

func TestManagerChannelClusterReplicasReturnsDetail(t *testing.T) {
	commit := uint64(42)
	minAvailable := uint64(1)
	retentionThrough := uint64(0)
	lag := uint64(0)
	var received channelRuntimeMetaDetailCall
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelClusterReplicaDetailReqSink: &received,
			channelClusterReplicaDetail: managementusecase.ChannelClusterReplicaDetail{
				Channel: managementusecase.ChannelRuntimeMetaDetail{
					ChannelRuntimeMeta: managementusecase.ChannelRuntimeMeta{
						ChannelID:     "room-1",
						ChannelType:   2,
						SlotID:        9,
						ChannelEpoch:  7,
						LeaderEpoch:   3,
						Leader:        1,
						Replicas:      []uint64{1, 2},
						ISR:           []uint64{1},
						MinISR:        1,
						MaxMessageSeq: uint64Ptr(42),
						Status:        "active",
					},
					HashSlot:     123,
					Features:     1,
					LeaseUntilMS: 1700000000000,
				},
				RuntimeReported:     true,
				CommitSeq:           &commit,
				MinAvailableSeq:     &minAvailable,
				RetentionThroughSeq: &retentionThrough,
				Replicas: []managementusecase.ChannelClusterReplicaStatus{
					{NodeID: 1, Role: "leader", IsLeader: true, InISR: true, Reported: true, CommitSeq: &commit, Lag: &lag},
					{NodeID: 2, Role: "follower", Reported: false},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/2/room-1/replicas", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, channelRuntimeMetaDetailCall{channelID: "room-1", channelType: 2}, received)
	require.JSONEq(t, `{
		"channel": {
			"channel_id": "room-1",
			"channel_type": 2,
			"slot_id": 9,
			"hash_slot": 123,
			"channel_epoch": 7,
			"leader_epoch": 3,
			"leader": 1,
			"replicas": [1, 2],
			"isr": [1],
			"min_isr": 1,
			"max_message_seq": 42,
			"status": "active",
			"features": 1,
			"lease_until_ms": 1700000000000
		},
		"runtime_reported": true,
		"commit_seq": 42,
		"min_available_seq": 1,
		"retention_through_seq": 0,
		"replicas": [
			{"node_id": 1, "role": "leader", "is_leader": true, "in_isr": true, "reported": true, "commit_seq": 42, "leo": null, "checkpoint_hw": null, "lag": 0},
			{"node_id": 2, "role": "follower", "is_leader": false, "in_isr": false, "reported": false, "commit_seq": null, "leo": null, "checkpoint_hw": null, "lag": null}
		]
	}`, rec.Body.String())
}

func TestManagerChannelClusterReplicasRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "viewer",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-cluster/2/room-1/replicas", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelClusterRepairReturnsChangedChannel(t *testing.T) {
	var received managementusecase.RepairChannelClusterLeaderRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
		}}),
		Management: managementStub{
			channelClusterRepairReqSink: &received,
			channelClusterRepair: managementusecase.RepairChannelClusterLeaderResponse{
				Changed: true,
				Channel: managementusecase.ChannelRuntimeMetaDetail{
					ChannelRuntimeMeta: managementusecase.ChannelRuntimeMeta{
						ChannelID: "room-1", ChannelType: 2, SlotID: 9, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{2}, MinISR: 1, Status: "active",
					},
					HashSlot: 123,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-cluster/2/room-1/repair", bytes.NewBufferString(`{"reason":"no_leader"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.RepairChannelClusterLeaderRequest{ChannelID: "room-1", ChannelType: 2, Reason: "no_leader"}, received)
	require.JSONEq(t, `{
		"changed": true,
		"channel": {
			"channel_id": "room-1",
			"channel_type": 2,
			"slot_id": 9,
			"hash_slot": 123,
			"channel_epoch": 0,
			"leader_epoch": 0,
			"leader": 2,
			"replicas": [1, 2],
			"isr": [2],
			"min_isr": 1,
			"status": "active",
			"features": 0,
			"lease_until_ms": 0
		}
	}`, rec.Body.String())
}

func TestManagerChannelClusterRepairRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "viewer",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-cluster/2/room-1/repair", bytes.NewBufferString(`{"reason":"no_leader"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelClusterLeaderTransferReturnsChangedChannel(t *testing.T) {
	var received managementusecase.TransferChannelClusterLeaderRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
		}}),
		Management: managementStub{
			channelClusterTransferReqSink: &received,
			channelClusterTransfer: managementusecase.TransferChannelClusterLeaderResponse{
				Changed: true,
				Channel: managementusecase.ChannelRuntimeMetaDetail{
					ChannelRuntimeMeta: managementusecase.ChannelRuntimeMeta{
						ChannelID: "room-1", ChannelType: 2, SlotID: 9, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 1, Status: "active",
					},
					HashSlot: 123,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-cluster/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.TransferChannelClusterLeaderRequest{ChannelID: "room-1", ChannelType: 2, TargetNodeID: 2}, received)
	require.JSONEq(t, `{
		"changed": true,
		"channel": {
			"channel_id": "room-1",
			"channel_type": 2,
			"slot_id": 9,
			"hash_slot": 123,
			"channel_epoch": 0,
			"leader_epoch": 0,
			"leader": 2,
			"replicas": [1, 2],
			"isr": [1, 2],
			"min_isr": 1,
			"status": "active",
			"features": 0,
			"lease_until_ms": 0
		}
	}`, rec.Body.String())
}

func TestManagerChannelClusterLeaderTransferRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "viewer",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-cluster/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelClusterOperationsMapErrors(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		stub   managementStub
		want   int
	}{
		{name: "invalid channel type", method: http.MethodGet, path: "/manager/channel-cluster/0/room-1/replicas", want: http.StatusBadRequest},
		{name: "replicas not found", method: http.MethodGet, path: "/manager/channel-cluster/2/room-1/replicas", stub: managementStub{channelClusterReplicaDetailErr: metadb.ErrNotFound}, want: http.StatusNotFound},
		{name: "repair no safe candidate", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/repair", body: `{"reason":"no_leader"}`, stub: managementStub{channelClusterRepairErr: channel.ErrNoSafeChannelLeader}, want: http.StatusConflict},
		{name: "repair leader unavailable", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/repair", body: `{"reason":"no_leader"}`, stub: managementStub{channelClusterRepairErr: raftcluster.ErrNoLeader}, want: http.StatusServiceUnavailable},
		{name: "transfer invalid channel type", method: http.MethodPost, path: "/manager/channel-cluster/0/room-1/leader/transfer", body: `{"target_node_id":2}`, want: http.StatusBadRequest},
		{name: "transfer invalid body", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{`, want: http.StatusBadRequest},
		{name: "transfer missing target", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{}`, want: http.StatusBadRequest},
		{name: "transfer target not replica", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: managementusecase.ErrChannelLeaderTransferTargetNotReplica}, want: http.StatusBadRequest},
		{name: "transfer target not isr", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: managementusecase.ErrChannelLeaderTransferTargetNotISR}, want: http.StatusConflict},
		{name: "transfer inactive", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: managementusecase.ErrChannelLeaderTransferInactiveChannel}, want: http.StatusConflict},
		{name: "transfer not found", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: metadb.ErrNotFound}, want: http.StatusNotFound},
		{name: "transfer no safe candidate", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: channel.ErrNoSafeChannelLeader}, want: http.StatusConflict},
		{name: "transfer leader unavailable", method: http.MethodPost, path: "/manager/channel-cluster/2/room-1/leader/transfer", body: `{"target_node_id":3}`, stub: managementStub{channelClusterTransferErr: raftcluster.ErrNoLeader}, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: tt.stub})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, tt.want, rec.Code)
		})
	}
}

func TestManagerChannelRuntimeMetaDetailRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/2/g1", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaDetailRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/2/g1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaDetailRejectsInvalidChannelType(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/0/g1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid channel_type"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaDetailReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{channelRuntimeMetaDetailErr: metadb.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/2/g1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"channel runtime meta not found"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaDetailReturnsServiceUnavailableWhenAuthoritativeReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{channelRuntimeMetaDetailErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/2/g1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"slot leader authoritative read unavailable"}`, rec.Body.String())
}

func TestManagerChannelRuntimeMetaDetailReturnsObject(t *testing.T) {
	var received channelRuntimeMetaDetailCall
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			channelRuntimeMetaDetailReqSink: &received,
			channelRuntimeMetaDetail: managementusecase.ChannelRuntimeMetaDetail{
				ChannelRuntimeMeta: managementusecase.ChannelRuntimeMeta{
					ChannelID:     "g1",
					ChannelType:   2,
					SlotID:        7,
					ChannelEpoch:  12,
					LeaderEpoch:   6,
					Leader:        3,
					Replicas:      []uint64{3, 5, 8},
					ISR:           []uint64{3, 5},
					MinISR:        2,
					MaxMessageSeq: uint64Ptr(99),
					Status:        "active",
				},
				HashSlot:     129,
				Features:     1,
				LeaseUntilMS: 1700000000000,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta/2/g1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, channelRuntimeMetaDetailCall{channelID: "g1", channelType: 2}, received)
	require.NotContains(t, rec.Body.String(), `"items"`)
	require.JSONEq(t, `{
		"channel_id": "g1",
		"channel_type": 2,
		"slot_id": 7,
		"hash_slot": 129,
		"channel_epoch": 12,
		"leader_epoch": 6,
		"leader": 3,
		"replicas": [3, 5, 8],
		"isr": [3, 5],
		"min_isr": 2,
		"max_message_seq": 99,
		"status": "active",
		"features": 1,
		"lease_until_ms": 1700000000000
	}`, rec.Body.String())
}

func TestManagerConnectionsRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerConnectionsAcceptsGlobalWildcardPermission(t *testing.T) {
	connectedAt := time.Date(2026, 4, 23, 8, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managementStub{
			connections: []managementusecase.Connection{{
				SessionID:   101,
				UID:         "u1",
				DeviceID:    "device-a",
				DeviceFlag:  "app",
				DeviceLevel: "master",
				SlotID:      9,
				State:       "active",
				Listener:    "tcp",
				ConnectedAt: connectedAt,
				RemoteAddr:  "10.0.0.1:5000",
				LocalAddr:   "127.0.0.1:7000",
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"session_id":101`)
}

func TestManagerConnectionsReturnsList(t *testing.T) {
	connectedAt := time.Date(2026, 4, 23, 8, 0, 0, 0, time.UTC)
	var received managementusecase.ListConnectionsRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			listConnectionsReqSink: &received,
			connections: []managementusecase.Connection{{
				NodeID:      2,
				SessionID:   101,
				UID:         "u1",
				DeviceID:    "device-a",
				DeviceFlag:  "app",
				DeviceLevel: "master",
				SlotID:      9,
				State:       "active",
				Listener:    "tcp",
				ConnectedAt: connectedAt,
				RemoteAddr:  "10.0.0.1:5000",
				LocalAddr:   "127.0.0.1:7000",
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListConnectionsRequest{NodeID: 2}, received)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"node_id": 2,
			"session_id": 101,
			"uid": "u1",
			"device_id": "device-a",
			"device_flag": "app",
			"device_level": "master",
			"slot_id": 9,
			"state": "active",
			"listener": "tcp",
			"connected_at": "2026-04-23T08:00:00Z",
			"remote_addr": "10.0.0.1:5000",
			"local_addr": "127.0.0.1:7000"
		}]
	}`, rec.Body.String())
}

func TestManagerConnectionsRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections?node_id=bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerConnectionsReturnsServiceUnavailableWhenManagementNotConfigured(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"management not configured"}`, rec.Body.String())
}

func TestManagerConnectionDetailReturnsItem(t *testing.T) {
	connectedAt := time.Date(2026, 4, 23, 8, 10, 0, 0, time.UTC)
	var received managementusecase.GetConnectionRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			connectionDetailReqSink: &received,
			connectionDetail: managementusecase.ConnectionDetail{
				NodeID:      2,
				SessionID:   202,
				UID:         "u2",
				DeviceID:    "device-b",
				DeviceFlag:  "web",
				DeviceLevel: "slave",
				SlotID:      3,
				State:       "closing",
				Listener:    "ws",
				ConnectedAt: connectedAt,
				RemoteAddr:  "10.0.0.2:6000",
				LocalAddr:   "127.0.0.1:7100",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/202?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.GetConnectionRequest{NodeID: 2, SessionID: 202}, received)
	require.JSONEq(t, `{
		"node_id": 2,
		"session_id": 202,
		"uid": "u2",
		"device_id": "device-b",
		"device_flag": "web",
		"device_level": "slave",
		"slot_id": 3,
		"state": "closing",
		"listener": "ws",
		"connected_at": "2026-04-23T08:10:00Z",
		"remote_addr": "10.0.0.2:6000",
		"local_addr": "127.0.0.1:7100"
	}`, rec.Body.String())
}

func TestManagerConnectionDetailRejectsInvalidSessionID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid session_id"}`, rec.Body.String())
}

func TestManagerConnectionDetailReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.connection",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{connectionDetailErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/99", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"connection not found"}`, rec.Body.String())
}

func TestManagerOverviewRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/overview", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerOverviewRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/overview", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerOverviewReturnsServiceUnavailableWhenLeaderConsistentReadUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.overview",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{overviewErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/overview", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"controller leader consistent read unavailable"}`, rec.Body.String())
}

func TestManagerOverviewReturnsAggregatedObject(t *testing.T) {
	generatedAt := time.Date(2026, 4, 21, 22, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.overview",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			overview: managementusecase.Overview{
				GeneratedAt: generatedAt,
				Cluster: managementusecase.OverviewCluster{
					ControllerLeaderID: 1,
				},
				Nodes: managementusecase.OverviewNodes{
					Total:    5,
					Alive:    4,
					Suspect:  0,
					Dead:     0,
					Draining: 1,
				},
				Slots: managementusecase.OverviewSlots{
					Total:         64,
					Ready:         60,
					QuorumLost:    1,
					LeaderMissing: 1,
					Unreported:    1,
					PeerMismatch:  1,
					EpochLag:      2,
				},
				Tasks: managementusecase.OverviewTasks{
					Total:    3,
					Pending:  1,
					Retrying: 1,
					Failed:   1,
				},
				Anomalies: managementusecase.OverviewAnomalies{
					Slots: managementusecase.OverviewSlotAnomalies{
						QuorumLost: managementusecase.OverviewSlotAnomalyGroup{
							Count: 1,
							Items: []managementusecase.OverviewSlotAnomalyItem{{
								SlotID:       7,
								Quorum:       "lost",
								Sync:         "matched",
								LeaderID:     0,
								DesiredPeers: []uint64{1, 2, 3},
								CurrentPeers: []uint64{1, 2, 3},
								LastReportAt: generatedAt.Add(-time.Minute),
							}},
						},
						LeaderMissing: managementusecase.OverviewSlotAnomalyGroup{
							Count: 1,
							Items: []managementusecase.OverviewSlotAnomalyItem{{
								SlotID:       9,
								Quorum:       "ready",
								Sync:         "matched",
								LeaderID:     0,
								DesiredPeers: []uint64{2, 3, 4},
								CurrentPeers: []uint64{2, 3, 4},
								LastReportAt: generatedAt.Add(-2 * time.Minute),
							}},
						},
						SyncMismatch: managementusecase.OverviewSlotAnomalyGroup{
							Count: 3,
							Items: []managementusecase.OverviewSlotAnomalyItem{{
								SlotID:       12,
								Quorum:       "ready",
								Sync:         "peer_mismatch",
								LeaderID:     2,
								DesiredPeers: []uint64{2, 3, 4},
								CurrentPeers: []uint64{2, 3},
								LastReportAt: generatedAt.Add(-3 * time.Minute),
							}},
						},
					},
					Tasks: managementusecase.OverviewTaskAnomalies{
						Failed: managementusecase.OverviewTaskAnomalyGroup{
							Count: 1,
							Items: []managementusecase.OverviewTaskAnomalyItem{{
								SlotID:     12,
								Kind:       "repair",
								Step:       "catch_up",
								Status:     "failed",
								SourceNode: 2,
								TargetNode: 4,
								Attempt:    3,
								LastError:  "learner catch-up timeout",
							}},
						},
						Retrying: managementusecase.OverviewTaskAnomalyGroup{
							Count: 1,
							Items: []managementusecase.OverviewTaskAnomalyItem{{
								SlotID:     18,
								Kind:       "rebalance",
								Step:       "transfer_leader",
								Status:     "retrying",
								SourceNode: 3,
								TargetNode: 5,
								Attempt:    2,
								NextRunAt:  timePtr(generatedAt.Add(2 * time.Minute)),
								LastError:  "transfer leader rejected",
							}},
						},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/overview", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"total": 0`)
	require.NotContains(t, rec.Body.String(), `"items":[{"channel_id"`)
	require.JSONEq(t, `{
		"generated_at": "2026-04-21T22:00:00Z",
		"cluster": {
			"controller_leader_id": 1
		},
		"nodes": {
			"total": 5,
			"alive": 4,
			"suspect": 0,
			"dead": 0,
			"draining": 1
		},
		"slots": {
			"total": 64,
			"ready": 60,
			"quorum_lost": 1,
			"leader_missing": 1,
			"unreported": 1,
			"peer_mismatch": 1,
			"epoch_lag": 2
		},
		"tasks": {
			"total": 3,
			"pending": 1,
			"retrying": 1,
			"failed": 1
		},
		"anomalies": {
			"slots": {
				"quorum_lost": {
					"count": 1,
					"items": [{
						"slot_id": 7,
						"quorum": "lost",
						"sync": "matched",
						"leader_id": 0,
						"desired_peers": [1, 2, 3],
						"current_peers": [1, 2, 3],
						"last_report_at": "2026-04-21T21:59:00Z"
					}]
				},
				"leader_missing": {
					"count": 1,
					"items": [{
						"slot_id": 9,
						"quorum": "ready",
						"sync": "matched",
						"leader_id": 0,
						"desired_peers": [2, 3, 4],
						"current_peers": [2, 3, 4],
						"last_report_at": "2026-04-21T21:58:00Z"
					}]
				},
				"sync_mismatch": {
					"count": 3,
					"items": [{
						"slot_id": 12,
						"quorum": "ready",
						"sync": "peer_mismatch",
						"leader_id": 2,
						"desired_peers": [2, 3, 4],
						"current_peers": [2, 3],
						"last_report_at": "2026-04-21T21:57:00Z"
					}]
				}
			},
			"tasks": {
				"failed": {
					"count": 1,
					"items": [{
						"slot_id": 12,
						"kind": "repair",
						"step": "catch_up",
						"status": "failed",
						"source_node": 2,
						"target_node": 4,
						"attempt": 3,
						"next_run_at": null,
						"last_error": "learner catch-up timeout"
					}]
				},
				"retrying": {
					"count": 1,
					"items": [{
						"slot_id": 18,
						"kind": "rebalance",
						"step": "transfer_leader",
						"status": "retrying",
						"source_node": 3,
						"target_node": 5,
						"attempt": 2,
						"next_run_at": "2026-04-21T22:02:00Z",
						"last_error": "transfer leader rejected"
					}]
				}
			}
		}
	}`, rec.Body.String())
}

func TestServerHandleMonitorMetricsReturnsTimestampedSeries(t *testing.T) {
	generatedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	firstPointAt := generatedAt.Add(-10 * time.Second)
	secondPointAt := generatedAt.Add(-5 * time.Second)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.overview",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			monitorMetrics: managementusecase.MonitorMetricsResult{
				GeneratedAt:   generatedAt,
				WindowSeconds: 10,
				StepSeconds:   5,
				Points:        2,
				Scope: managementusecase.MonitorMetricsScope{
					View:        "local_node",
					LocalNodeID: 1,
				},
				Capabilities: managementusecase.MonitorMetricsCapabilities{
					NodeFilter: false,
				},
				Nodes: []managementusecase.MonitorMetricsNode{{
					NodeID:    1,
					Name:      "node-1",
					IsLocal:   true,
					Available: true,
				}},
				Metrics: map[string]managementusecase.MonitorMetricSeries{
					"send_rate": {
						Key:    "send_rate",
						Unit:   "msg/s",
						Latest: 8,
						Peak:   8,
						Avg:    6,
						Points: []managementusecase.MonitorMetricPoint{
							{At: firstPointAt, Value: 4},
							{At: secondPointAt, Value: 8},
						},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/monitor/metrics?window=5m&step=5s", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-05-15T08:30:00Z",
		"window_seconds": 10,
		"step_seconds": 5,
		"points": 2,
		"scope": {
			"view": "local_node",
			"local_node_id": 1
		},
		"capabilities": {
			"node_filter": false
		},
		"nodes": [{
			"node_id": 1,
			"name": "node-1",
			"is_local": true,
			"available": true
		}],
		"metrics": {
			"send_rate": {
				"key": "send_rate",
				"unit": "msg/s",
				"latest": 8,
				"peak": 8,
				"avg": 6,
				"points": [
					{"at": "2026-05-15T08:29:50Z", "value": 4},
					{"at": "2026-05-15T08:29:55Z", "value": 8}
				]
			}
		}
	}`, rec.Body.String())
}

func TestServerHandleMonitorMetricsPassesNodeID(t *testing.T) {
	var received uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.overview",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			monitorMetricsNodeIDSink: &received,
			monitorMetrics: managementusecase.MonitorMetricsResult{
				GeneratedAt:   time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC),
				WindowSeconds: 10,
				StepSeconds:   5,
				Points:        2,
				Scope: managementusecase.MonitorMetricsScope{
					View:        "node",
					LocalNodeID: 1,
					NodeID:      2,
				},
				Capabilities: managementusecase.MonitorMetricsCapabilities{NodeFilter: true},
				Nodes:        []managementusecase.MonitorMetricsNode{{NodeID: 2, Name: "node-2", Available: true}},
				Metrics:      map[string]managementusecase.MonitorMetricSeries{},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/monitor/metrics?window=5m&step=5s&node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2), received)
	require.Contains(t, rec.Body.String(), `"node_id":2`)
}

func testAuthConfig(users []UserConfig) AuthConfig {
	return AuthConfig{
		On:        true,
		JWTSecret: "test-secret",
		JWTIssuer: "wukongim-manager",
		JWTExpire: time.Hour,
		Users:     users,
	}
}

func mustIssueTestToken(t *testing.T, srv *Server, username string) string {
	t.Helper()
	token, err := srv.issueToken(username, time.Now())
	require.NoError(t, err)
	return token
}

func mustIssueExpiredTestToken(t *testing.T, srv *Server, username string) string {
	t.Helper()
	token, err := srv.issueToken(username, time.Now().Add(-2*srv.auth.jwtExpire))
	require.NoError(t, err)
	return token
}

func mustEncodeChannelRuntimeMetaCursorForTest(t *testing.T, cursor managementusecase.ChannelRuntimeMetaListCursor) string {
	t.Helper()
	raw, err := encodeChannelRuntimeMetaCursor(cursor)
	require.NoError(t, err)
	return raw
}

func mustEncodeLegacyChannelRuntimeMetaCursorForTest(t *testing.T, cursor managementusecase.ChannelRuntimeMetaListCursor) string {
	t.Helper()
	payload, err := json.Marshal(channelRuntimeMetaCursorPayload{
		Version:     1,
		SlotID:      cursor.SlotID,
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	})
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(payload)
}

type managementStub struct {
	nodes                              managementusecase.NodeList
	nodesErr                           error
	nodeDetail                         managementusecase.NodeDetail
	nodeDetailErr                      error
	nodeDraining                       managementusecase.NodeDetail
	nodeDrainingErr                    error
	nodeResume                         managementusecase.NodeDetail
	nodeResumeErr                      error
	nodeScaleInReport                  managementusecase.NodeScaleInReport
	nodeScaleInErr                     error
	nodeScaleInNodeIDSink              *uint64
	nodeScaleInPlanReqSink             *managementusecase.NodeScaleInPlanRequest
	nodeScaleInAdvanceReqSink          *managementusecase.AdvanceNodeScaleInRequest
	slotLeaderTransfer                 managementusecase.SlotDetail
	slotLeaderTransferErr              error
	slotAdd                            managementusecase.SlotDetail
	slotAddErr                         error
	slotRemove                         managementusecase.SlotRemoveResult
	slotRemoveErr                      error
	slotRecover                        managementusecase.SlotRecoverResult
	slotRecoverErr                     error
	slotRebalance                      managementusecase.SlotRebalanceResult
	slotRebalanceErr                   error
	slots                              []managementusecase.Slot
	slotsErr                           error
	listSlotsOptionsSink               *managementusecase.ListSlotsOptions
	slotLogEntriesReqSink              *managementusecase.ListSlotLogEntriesRequest
	slotLogEntriesPage                 managementusecase.SlotLogEntriesResponse
	slotLogEntriesErr                  error
	controllerLogEntriesReqSink        *managementusecase.ListControllerLogEntriesRequest
	controllerLogEntriesPage           managementusecase.ControllerLogEntriesResponse
	controllerLogEntriesErr            error
	controllerRaftStatusNodeIDSink     *uint64
	controllerRaftStatus               managementusecase.ControllerRaftStatusResponse
	controllerRaftStatusErr            error
	controllerRaftCompaction           managementusecase.CompactControllerRaftLogsResponse
	controllerRaftCompactionErr        error
	controllerRaftCompactionNodeIDSink *uint64
	controllerRaftNodeCompaction       managementusecase.CompactControllerRaftLogsResponse
	controllerRaftNodeCompactionErr    error
	slotRaftCompactionNodeIDSink       *uint64
	slotRaftCompactionSlotIDSink       *uint32
	slotRaftCompaction                 managementusecase.CompactSlotRaftLogResponse
	slotRaftCompactionErr              error
	slotDetail                         managementusecase.SlotDetail
	slotDetailErr                      error
	tasks                              []managementusecase.Task
	tasksErr                           error
	task                               managementusecase.TaskDetail
	taskErr                            error
	distributedTaskSummary             managementusecase.DistributedTaskSummary
	distributedTaskSummaryErr          error
	distributedTasksReqSink            *managementusecase.DistributedTaskQuery
	distributedTasks                   managementusecase.DistributedTaskListResult
	distributedTasksErr                error
	distributedTask                    managementusecase.DistributedTaskDetail
	distributedTaskErr                 error
	connections                        []managementusecase.Connection
	connectionsErr                     error
	listConnectionsReqSink             *managementusecase.ListConnectionsRequest
	connectionDetailReqSink            *managementusecase.GetConnectionRequest
	connectionDetail                   managementusecase.ConnectionDetail
	connectionDetailErr                error
	usersReqSink                       *managementusecase.ListUsersRequest
	usersPage                          managementusecase.ListUsersResponse
	usersErr                           error
	userDetail                         managementusecase.UserDetail
	userDetailErr                      error
	kickUserReqSink                    *managementusecase.KickUserRequest
	kickUserResponse                   managementusecase.KickUserResponse
	kickUserErr                        error
	resetUserTokenReqSink              *managementusecase.ResetUserTokenRequest
	resetUserTokenResponse             managementusecase.ResetUserTokenResponse
	resetUserTokenErr                  error
	systemUsers                        managementusecase.ListSystemUsersResponse
	systemUsersErr                     error
	systemUsersAddReqSink              *managementusecase.MutateSystemUsersRequest
	systemUsersRemoveReqSink           *managementusecase.MutateSystemUsersRequest
	systemUsersMutation                managementusecase.MutateSystemUsersResponse
	systemUsersMutationErr             error
	businessChannelsReqSink            *managementusecase.ListBusinessChannelsRequest
	businessChannelsPage               managementusecase.ListBusinessChannelsResponse
	businessChannelsErr                error
	businessChannelDetail              managementusecase.BusinessChannelDetail
	businessChannelDetailErr           error
	businessChannelUpsertReqSink       *managementusecase.UpsertBusinessChannelRequest
	businessChannelMembersReqSink      *managementusecase.ListBusinessChannelMembersRequest
	businessChannelMembersPage         managementusecase.ListBusinessChannelMembersResponse
	businessChannelMembersErr          error
	businessChannelMutateReqSink       *managementusecase.MutateBusinessChannelMembersRequest
	businessChannelSecondMutateReqSink *managementusecase.MutateBusinessChannelMembersRequest
	businessChannelMutateResponse      managementusecase.MutateBusinessChannelMembersResponse
	businessChannelMutateErr           error
	channelRuntimeMetaReqSink          *managementusecase.ListChannelRuntimeMetaRequest
	channelRuntimeMetaPage             managementusecase.ListChannelRuntimeMetaResponse
	channelRuntimeMetaErr              error
	channelRuntimeMetaDetailReqSink    *channelRuntimeMetaDetailCall
	channelRuntimeMetaDetail           managementusecase.ChannelRuntimeMetaDetail
	channelRuntimeMetaDetailErr        error
	channelClusterSummary              managementusecase.ChannelClusterSummary
	channelClusterSummaryErr           error
	channelClusterUnhealthyReqSink     *managementusecase.ListChannelClusterUnhealthyRequest
	channelClusterUnhealthyPage        managementusecase.ListChannelClusterUnhealthyResponse
	channelClusterUnhealthyErr         error
	channelClusterReplicaDetailReqSink *channelRuntimeMetaDetailCall
	channelClusterReplicaDetail        managementusecase.ChannelClusterReplicaDetail
	channelClusterReplicaDetailErr     error
	channelClusterRepairReqSink        *managementusecase.RepairChannelClusterLeaderRequest
	channelClusterRepair               managementusecase.RepairChannelClusterLeaderResponse
	channelClusterRepairErr            error
	channelClusterTransferReqSink      *managementusecase.TransferChannelClusterLeaderRequest
	channelClusterTransfer             managementusecase.TransferChannelClusterLeaderResponse
	channelClusterTransferErr          error
	messagesReqSink                    *managementusecase.ListMessagesRequest
	messagesPage                       managementusecase.ListMessagesResponse
	messagesErr                        error
	recentConversationsReqSink         *managementusecase.RecentConversationsRequest
	recentConversations                managementusecase.RecentConversationsResponse
	recentConversationsErr             error
	retentionReqSink                   *managementusecase.AdvanceMessageRetentionRequest
	retentionResult                    managementusecase.AdvanceMessageRetentionResponse
	retentionErr                       error
	overview                           managementusecase.Overview
	overviewErr                        error
	networkSummary                     managementusecase.NetworkSummary
	networkSummaryErr                  error
	monitorMetrics                     managementusecase.MonitorMetricsResult
	monitorMetricsErr                  error
	monitorMetricsNodeIDSink           *uint64
	pluginList                         pluginusecase.LocalPluginList
	pluginListErr                      error
	pluginDetail                       pluginusecase.LocalPluginDetail
	pluginDetailErr                    error
	pluginUpdateReqSink                *managementusecase.UpdatePluginConfigRequest
	pluginRestartReqSink               *managementusecase.PluginBindingMutationRequest
	pluginUninstallReqSink             *managementusecase.PluginBindingMutationRequest
	pluginMutationErr                  error
	pluginBindingListReqSink           *managementusecase.PluginBindingListRequest
	pluginBindingList                  managementusecase.PluginBindingListResponse
	pluginBindingListErr               error
	pluginBindingMutationReqSink       *managementusecase.PluginBindingMutationRequest
	pluginBindingUnbindReqSink         *managementusecase.PluginBindingMutationRequest
	pluginBindingMutation              managementusecase.PluginBindingMutationResponse
	pluginBindingMutationErr           error
	nodeOnboardingCandidates           managementusecase.NodeOnboardingCandidatesResponse
	nodeOnboardingCandidatesErr        error
	nodeOnboardingJob                  managementusecase.NodeOnboardingJobResponse
	nodeOnboardingJobs                 managementusecase.NodeOnboardingJobsResponse
	nodeOnboardingPlanReqSink          *managementusecase.CreateNodeOnboardingPlanRequest
	nodeOnboardingJobErr               error
	diagnosticsReqSink                 *managementusecase.DiagnosticsQueryRequest
	diagnosticsResponse                managementusecase.DiagnosticsQueryResponse
	diagnosticsErr                     error
}

func nodeListAt(t time.Time, leader uint64, items ...managementusecase.Node) managementusecase.NodeList {
	return managementusecase.NodeList{
		GeneratedAt:        t,
		ControllerLeaderID: leader,
		Items:              append([]managementusecase.Node(nil), items...),
	}
}

func (s managementStub) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return managementusecase.NodeList{
		GeneratedAt:        s.nodes.GeneratedAt,
		ControllerLeaderID: s.nodes.ControllerLeaderID,
		Items:              append([]managementusecase.Node(nil), s.nodes.Items...),
	}, s.nodesErr
}

func (s managementStub) GetNode(context.Context, uint64) (managementusecase.NodeDetail, error) {
	return s.nodeDetail, s.nodeDetailErr
}

func (s managementStub) MarkNodeDraining(context.Context, uint64) (managementusecase.NodeDetail, error) {
	return s.nodeDraining, s.nodeDrainingErr
}

func (s managementStub) ResumeNode(context.Context, uint64) (managementusecase.NodeDetail, error) {
	return s.nodeResume, s.nodeResumeErr
}

func (s managementStub) PlanNodeScaleIn(_ context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error) {
	if s.nodeScaleInNodeIDSink != nil {
		*s.nodeScaleInNodeIDSink = nodeID
	}
	if s.nodeScaleInPlanReqSink != nil {
		*s.nodeScaleInPlanReqSink = req
	}
	return s.nodeScaleInReport, s.nodeScaleInErr
}

func (s managementStub) StartNodeScaleIn(_ context.Context, nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error) {
	if s.nodeScaleInNodeIDSink != nil {
		*s.nodeScaleInNodeIDSink = nodeID
	}
	if s.nodeScaleInPlanReqSink != nil {
		*s.nodeScaleInPlanReqSink = req
	}
	return s.nodeScaleInReport, s.nodeScaleInErr
}

func (s managementStub) GetNodeScaleInStatus(_ context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error) {
	if s.nodeScaleInNodeIDSink != nil {
		*s.nodeScaleInNodeIDSink = nodeID
	}
	return s.nodeScaleInReport, s.nodeScaleInErr
}

func (s managementStub) AdvanceNodeScaleIn(_ context.Context, nodeID uint64, req managementusecase.AdvanceNodeScaleInRequest) (managementusecase.NodeScaleInReport, error) {
	if s.nodeScaleInNodeIDSink != nil {
		*s.nodeScaleInNodeIDSink = nodeID
	}
	if s.nodeScaleInAdvanceReqSink != nil {
		*s.nodeScaleInAdvanceReqSink = req
	}
	return s.nodeScaleInReport, s.nodeScaleInErr
}

func (s managementStub) CancelNodeScaleIn(_ context.Context, nodeID uint64) (managementusecase.NodeScaleInReport, error) {
	if s.nodeScaleInNodeIDSink != nil {
		*s.nodeScaleInNodeIDSink = nodeID
	}
	return s.nodeScaleInReport, s.nodeScaleInErr
}

func (s managementStub) ListSlots(_ context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	if s.listSlotsOptionsSink != nil {
		*s.listSlotsOptionsSink = opts
	}
	return append([]managementusecase.Slot(nil), s.slots...), s.slotsErr
}

func (s managementStub) GetSlot(context.Context, uint32) (managementusecase.SlotDetail, error) {
	return s.slotDetail, s.slotDetailErr
}

func (s managementStub) ListSlotLogEntries(_ context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	if s.slotLogEntriesReqSink != nil {
		*s.slotLogEntriesReqSink = req
	}
	return s.slotLogEntriesPage, s.slotLogEntriesErr
}

func (s managementStub) ListControllerLogEntries(_ context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	if s.controllerLogEntriesReqSink != nil {
		*s.controllerLogEntriesReqSink = req
	}
	return s.controllerLogEntriesPage, s.controllerLogEntriesErr
}

func (s managementStub) GetControllerRaftStatus(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftStatusResponse, error) {
	if s.controllerRaftStatusNodeIDSink != nil {
		*s.controllerRaftStatusNodeIDSink = nodeID
	}
	return s.controllerRaftStatus, s.controllerRaftStatusErr
}

func (s managementStub) CompactControllerRaftLogs(context.Context) (managementusecase.CompactControllerRaftLogsResponse, error) {
	return s.controllerRaftCompaction, s.controllerRaftCompactionErr
}

func (s managementStub) CompactControllerRaftLog(_ context.Context, nodeID uint64) (managementusecase.CompactControllerRaftLogsResponse, error) {
	if s.controllerRaftCompactionNodeIDSink != nil {
		*s.controllerRaftCompactionNodeIDSink = nodeID
	}
	return s.controllerRaftNodeCompaction, s.controllerRaftNodeCompactionErr
}

func (s managementStub) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.CompactSlotRaftLogResponse, error) {
	if s.slotRaftCompactionNodeIDSink != nil {
		*s.slotRaftCompactionNodeIDSink = nodeID
	}
	if s.slotRaftCompactionSlotIDSink != nil {
		*s.slotRaftCompactionSlotIDSink = slotID
	}
	return s.slotRaftCompaction, s.slotRaftCompactionErr
}

func (s managementStub) AddSlot(context.Context) (managementusecase.SlotDetail, error) {
	return s.slotAdd, s.slotAddErr
}

func (s managementStub) RemoveSlot(context.Context, uint32) (managementusecase.SlotRemoveResult, error) {
	return s.slotRemove, s.slotRemoveErr
}

func (s managementStub) RecoverSlot(context.Context, uint32, managementusecase.SlotRecoverStrategy) (managementusecase.SlotRecoverResult, error) {
	return s.slotRecover, s.slotRecoverErr
}

func (s managementStub) RebalanceSlots(context.Context) (managementusecase.SlotRebalanceResult, error) {
	return s.slotRebalance, s.slotRebalanceErr
}

func (s managementStub) ListTasks(context.Context) ([]managementusecase.Task, error) {
	return append([]managementusecase.Task(nil), s.tasks...), s.tasksErr
}

func (s managementStub) GetTask(context.Context, uint32) (managementusecase.TaskDetail, error) {
	return s.task, s.taskErr
}

func (s managementStub) GetDistributedTasksSummary(context.Context) (managementusecase.DistributedTaskSummary, error) {
	return s.distributedTaskSummary, s.distributedTaskSummaryErr
}

func (s managementStub) ListDistributedTasks(_ context.Context, query managementusecase.DistributedTaskQuery) (managementusecase.DistributedTaskListResult, error) {
	if s.distributedTasksReqSink != nil {
		*s.distributedTasksReqSink = query
	}
	return s.distributedTasks, s.distributedTasksErr
}

func (s managementStub) GetDistributedTask(context.Context, managementusecase.DistributedTaskDomain, string) (managementusecase.DistributedTaskDetail, error) {
	return s.distributedTask, s.distributedTaskErr
}

func (s managementStub) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	if s.listConnectionsReqSink != nil {
		*s.listConnectionsReqSink = req
	}
	return append([]managementusecase.Connection(nil), s.connections...), s.connectionsErr
}

func (s managementStub) GetConnection(_ context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	if s.connectionDetailReqSink != nil {
		*s.connectionDetailReqSink = req
	}
	return s.connectionDetail, s.connectionDetailErr
}

func (s managementStub) ListUsers(_ context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error) {
	if s.usersReqSink != nil {
		*s.usersReqSink = req
	}
	return s.usersPage, s.usersErr
}

func (s managementStub) GetUser(_ context.Context, uid string) (managementusecase.UserDetail, error) {
	return s.userDetail, s.userDetailErr
}

func (s managementStub) KickUser(_ context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error) {
	if s.kickUserReqSink != nil {
		*s.kickUserReqSink = req
	}
	return s.kickUserResponse, s.kickUserErr
}

func (s managementStub) ResetUserToken(_ context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error) {
	if s.resetUserTokenReqSink != nil {
		*s.resetUserTokenReqSink = req
	}
	return s.resetUserTokenResponse, s.resetUserTokenErr
}

func (s managementStub) ListSystemUsers(context.Context) (managementusecase.ListSystemUsersResponse, error) {
	return s.systemUsers, s.systemUsersErr
}

func (s managementStub) AddSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersAddReqSink != nil {
		*s.systemUsersAddReqSink = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func (s managementStub) RemoveSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersRemoveReqSink != nil {
		*s.systemUsersRemoveReqSink = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func (s managementStub) ListBusinessChannels(_ context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	if s.businessChannelsReqSink != nil {
		*s.businessChannelsReqSink = req
	}
	return s.businessChannelsPage, s.businessChannelsErr
}

func (s managementStub) GetBusinessChannel(_ context.Context, channelID string, channelType int64) (managementusecase.BusinessChannelDetail, error) {
	if s.businessChannelDetail.ChannelID == "" {
		s.businessChannelDetail.BusinessChannelListItem.ChannelID = channelID
		s.businessChannelDetail.BusinessChannelListItem.ChannelType = channelType
	}
	return s.businessChannelDetail, s.businessChannelDetailErr
}

func (s managementStub) UpsertBusinessChannel(_ context.Context, req managementusecase.UpsertBusinessChannelRequest) (managementusecase.BusinessChannelDetail, error) {
	if s.businessChannelUpsertReqSink != nil {
		*s.businessChannelUpsertReqSink = req
	}
	return s.businessChannelDetail, s.businessChannelDetailErr
}

func (s managementStub) ListBusinessChannelMembers(_ context.Context, req managementusecase.ListBusinessChannelMembersRequest) (managementusecase.ListBusinessChannelMembersResponse, error) {
	if s.businessChannelMembersReqSink != nil {
		*s.businessChannelMembersReqSink = req
	}
	return s.businessChannelMembersPage, s.businessChannelMembersErr
}

func (s managementStub) MutateBusinessChannelMembers(_ context.Context, req managementusecase.MutateBusinessChannelMembersRequest) (managementusecase.MutateBusinessChannelMembersResponse, error) {
	if s.businessChannelMutateReqSink != nil && businessChannelMutateSinkEmpty(*s.businessChannelMutateReqSink) {
		*s.businessChannelMutateReqSink = req
	} else if s.businessChannelSecondMutateReqSink != nil {
		*s.businessChannelSecondMutateReqSink = req
	} else if s.businessChannelMutateReqSink != nil {
		*s.businessChannelMutateReqSink = req
	}
	return s.businessChannelMutateResponse, s.businessChannelMutateErr
}

func businessChannelMutateSinkEmpty(req managementusecase.MutateBusinessChannelMembersRequest) bool {
	return req.ChannelID == "" && req.ChannelType == 0 && req.ListKind == "" && len(req.UIDs) == 0 && !req.Add
}

func (s managementStub) ListChannelRuntimeMeta(_ context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error) {
	if s.channelRuntimeMetaReqSink != nil {
		*s.channelRuntimeMetaReqSink = req
	}
	return s.channelRuntimeMetaPage, s.channelRuntimeMetaErr
}

func (s managementStub) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (managementusecase.ChannelRuntimeMetaDetail, error) {
	if s.channelRuntimeMetaDetailReqSink != nil {
		*s.channelRuntimeMetaDetailReqSink = channelRuntimeMetaDetailCall{channelID: channelID, channelType: channelType}
	}
	return s.channelRuntimeMetaDetail, s.channelRuntimeMetaDetailErr
}

func (s managementStub) GetChannelClusterSummary(context.Context) (managementusecase.ChannelClusterSummary, error) {
	return s.channelClusterSummary, s.channelClusterSummaryErr
}

func (s managementStub) ListChannelClusterUnhealthy(_ context.Context, req managementusecase.ListChannelClusterUnhealthyRequest) (managementusecase.ListChannelClusterUnhealthyResponse, error) {
	if s.channelClusterUnhealthyReqSink != nil {
		*s.channelClusterUnhealthyReqSink = req
	}
	return s.channelClusterUnhealthyPage, s.channelClusterUnhealthyErr
}

func (s managementStub) GetChannelClusterReplicaDetail(_ context.Context, channelID string, channelType int64) (managementusecase.ChannelClusterReplicaDetail, error) {
	if s.channelClusterReplicaDetailReqSink != nil {
		*s.channelClusterReplicaDetailReqSink = channelRuntimeMetaDetailCall{channelID: channelID, channelType: channelType}
	}
	return s.channelClusterReplicaDetail, s.channelClusterReplicaDetailErr
}

func (s managementStub) RepairChannelClusterLeader(_ context.Context, req managementusecase.RepairChannelClusterLeaderRequest) (managementusecase.RepairChannelClusterLeaderResponse, error) {
	if s.channelClusterRepairReqSink != nil {
		*s.channelClusterRepairReqSink = req
	}
	return s.channelClusterRepair, s.channelClusterRepairErr
}

func (s managementStub) TransferChannelClusterLeader(_ context.Context, req managementusecase.TransferChannelClusterLeaderRequest) (managementusecase.TransferChannelClusterLeaderResponse, error) {
	if s.channelClusterTransferReqSink != nil {
		*s.channelClusterTransferReqSink = req
	}
	return s.channelClusterTransfer, s.channelClusterTransferErr
}

func (s managementStub) ListMessages(_ context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error) {
	if s.messagesReqSink != nil {
		*s.messagesReqSink = req
	}
	return s.messagesPage, s.messagesErr
}

func (s managementStub) ListRecentConversations(_ context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error) {
	if s.recentConversationsReqSink != nil {
		*s.recentConversationsReqSink = req
	}
	return s.recentConversations, s.recentConversationsErr
}

func (s managementStub) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if s.retentionReqSink != nil {
		*s.retentionReqSink = req
	}
	return s.retentionResult, s.retentionErr
}

func (s managementStub) TransferChannelLeader(context.Context, channel.ChannelID, managementusecase.TransferChannelLeaderRequest) (managementusecase.ChannelMigrationResult, error) {
	return managementusecase.ChannelMigrationResult{}, nil
}

func (s managementStub) MigrateChannelReplica(context.Context, channel.ChannelID, managementusecase.MigrateChannelReplicaRequest) (managementusecase.ChannelMigrationResult, error) {
	return managementusecase.ChannelMigrationResult{}, nil
}

func (s managementStub) GetChannelMigration(context.Context, channel.ChannelID) (managementusecase.ChannelMigrationDetail, error) {
	return managementusecase.ChannelMigrationDetail{}, nil
}

func (s managementStub) AbortChannelMigration(context.Context, channel.ChannelID, string) (managementusecase.ChannelMigrationDetail, error) {
	return managementusecase.ChannelMigrationDetail{}, nil
}

func (s managementStub) GetOverview(context.Context) (managementusecase.Overview, error) {
	return s.overview, s.overviewErr
}

func (s managementStub) ListNetworkSummary(context.Context) (managementusecase.NetworkSummary, error) {
	return s.networkSummary, s.networkSummaryErr
}

func (s managementStub) GetMonitorMetrics(_ context.Context, nodeID uint64, _, _ time.Duration) (managementusecase.MonitorMetricsResult, error) {
	if s.monitorMetricsNodeIDSink != nil {
		*s.monitorMetricsNodeIDSink = nodeID
	}
	return s.monitorMetrics, s.monitorMetricsErr
}

func (s managementStub) ListNodePlugins(_ context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error) {
	out := s.pluginList
	if out.NodeID == 0 {
		out.NodeID = nodeID
	}
	return out, s.pluginListErr
}

func (s managementStub) GetNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	out := s.pluginDetail
	if out.NodeID == 0 {
		out.NodeID = nodeID
	}
	if out.No == "" {
		out.No = pluginNo
	}
	return out, s.pluginDetailErr
}

func (s managementStub) UpdateNodePluginConfig(_ context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	if s.pluginUpdateReqSink != nil {
		*s.pluginUpdateReqSink = managementusecase.UpdatePluginConfigRequest{NodeID: nodeID, PluginNo: pluginNo, Config: append(json.RawMessage(nil), config...)}
	}
	out := s.pluginDetail
	if out.NodeID == 0 {
		out.NodeID = nodeID
	}
	if out.No == "" {
		out.No = pluginNo
	}
	return out, s.pluginMutationErr
}

func (s managementStub) RestartNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	if s.pluginRestartReqSink != nil {
		*s.pluginRestartReqSink = managementusecase.PluginBindingMutationRequest{UID: fmt.Sprintf("%d", nodeID), PluginNo: pluginNo}
	}
	out := s.pluginDetail
	if out.NodeID == 0 {
		out.NodeID = nodeID
	}
	if out.No == "" {
		out.No = pluginNo
	}
	return out, s.pluginMutationErr
}

func (s managementStub) UninstallNodePlugin(_ context.Context, nodeID uint64, pluginNo string) error {
	if s.pluginUninstallReqSink != nil {
		*s.pluginUninstallReqSink = managementusecase.PluginBindingMutationRequest{UID: fmt.Sprintf("%d", nodeID), PluginNo: pluginNo}
	}
	return s.pluginMutationErr
}

func (s managementStub) ListPluginBindings(_ context.Context, req managementusecase.PluginBindingListRequest) (managementusecase.PluginBindingListResponse, error) {
	if s.pluginBindingListReqSink != nil {
		*s.pluginBindingListReqSink = req
	}
	return s.pluginBindingList, s.pluginBindingListErr
}

func (s managementStub) BindPluginUser(_ context.Context, req managementusecase.PluginBindingMutationRequest) (managementusecase.PluginBindingMutationResponse, error) {
	if s.pluginBindingMutationReqSink != nil {
		*s.pluginBindingMutationReqSink = req
	}
	return s.pluginBindingMutation, s.pluginBindingMutationErr
}

func (s managementStub) UnbindPluginUser(_ context.Context, req managementusecase.PluginBindingMutationRequest) error {
	if s.pluginBindingUnbindReqSink != nil {
		*s.pluginBindingUnbindReqSink = req
	}
	return s.pluginBindingMutationErr
}

func (s managementStub) ListNodeOnboardingCandidates(context.Context) (managementusecase.NodeOnboardingCandidatesResponse, error) {
	return s.nodeOnboardingCandidates, s.nodeOnboardingCandidatesErr
}

func (s managementStub) CreateNodeOnboardingPlan(_ context.Context, req managementusecase.CreateNodeOnboardingPlanRequest) (managementusecase.NodeOnboardingJobResponse, error) {
	if s.nodeOnboardingPlanReqSink != nil {
		*s.nodeOnboardingPlanReqSink = req
	}
	return s.nodeOnboardingJob, s.nodeOnboardingJobErr
}

func (s managementStub) StartNodeOnboardingJob(context.Context, string) (managementusecase.NodeOnboardingJobResponse, error) {
	return s.nodeOnboardingJob, s.nodeOnboardingJobErr
}

func (s managementStub) ListNodeOnboardingJobs(context.Context, managementusecase.ListNodeOnboardingJobsRequest) (managementusecase.NodeOnboardingJobsResponse, error) {
	return s.nodeOnboardingJobs, s.nodeOnboardingJobErr
}

func (s managementStub) GetNodeOnboardingJob(context.Context, string) (managementusecase.NodeOnboardingJobResponse, error) {
	return s.nodeOnboardingJob, s.nodeOnboardingJobErr
}

func (s managementStub) RetryNodeOnboardingJob(context.Context, string) (managementusecase.NodeOnboardingJobResponse, error) {
	return s.nodeOnboardingJob, s.nodeOnboardingJobErr
}

func (s managementStub) QueryDiagnostics(_ context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error) {
	if s.diagnosticsReqSink != nil {
		*s.diagnosticsReqSink = req
	}
	return s.diagnosticsResponse, s.diagnosticsErr
}

func (s managementStub) CreateDiagnosticsTrackingRule(context.Context, managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error) {
	return managementusecase.DiagnosticsTrackingMutationResponse{}, nil
}

func (s managementStub) ListDiagnosticsTrackingRules(context.Context) (managementusecase.DiagnosticsTrackingListResponse, error) {
	return managementusecase.DiagnosticsTrackingListResponse{}, nil
}

func (s managementStub) DeleteDiagnosticsTrackingRule(context.Context, string) (managementusecase.DiagnosticsTrackingDeleteResponse, error) {
	return managementusecase.DiagnosticsTrackingDeleteResponse{}, nil
}

func (s managementStub) GetDashboardMetrics(time.Duration, time.Duration) (managementusecase.DashboardMetricsResult, error) {
	return managementusecase.DashboardMetricsResult{}, nil
}

type channelRuntimeMetaDetailCall struct {
	channelID   string
	channelType int64
}

func uint64Ptr(value uint64) *uint64 {
	return &value
}

func timePtr(value time.Time) *time.Time {
	return &value
}

type loginResponseBody struct {
	Username    string           `json:"username"`
	TokenType   string           `json:"token_type"`
	AccessToken string           `json:"access_token"`
	ExpiresIn   int64            `json:"expires_in"`
	ExpiresAt   time.Time        `json:"expires_at"`
	Permissions []permissionBody `json:"permissions"`
}

type permissionBody struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}
