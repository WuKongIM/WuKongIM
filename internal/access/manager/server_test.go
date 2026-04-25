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
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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

func TestManagerNodesReturnsAggregatedList(t *testing.T) {
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
			nodes: []managementusecase.Node{{
				NodeID:          1,
				Addr:            "127.0.0.1:7000",
				Status:          "alive",
				LastHeartbeatAt: lastHeartbeatAt,
				ControllerRole:  "leader",
				SlotCount:       3,
				LeaderSlotCount: 2,
				IsLocal:         true,
				CapacityWeight:  1,
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
			"node_id": 1,
			"addr": "127.0.0.1:7000",
			"status": "alive",
			"last_heartbeat_at": "2026-04-21T15:04:05Z",
			"is_local": true,
			"capacity_weight": 1,
			"controller": {
				"role": "leader"
			},
			"slot_stats": {
				"count": 3,
				"leader_count": 2
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
					Addr:            "127.0.0.1:7002",
					Status:          "draining",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 2,
					IsLocal:         true,
					CapacityWeight:  2,
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
		"addr": "127.0.0.1:7002",
		"status": "draining",
		"last_heartbeat_at": "2026-04-21T15:04:05Z",
		"is_local": true,
		"capacity_weight": 2,
		"controller": {
			"role": "follower"
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 2
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": [2, 4]
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
					Addr:            "127.0.0.1:7002",
					Status:          "draining",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 0,
					IsLocal:         false,
					CapacityWeight:  2,
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
		"addr": "127.0.0.1:7002",
		"status": "draining",
		"last_heartbeat_at": "2026-04-22T02:04:05Z",
		"is_local": false,
		"capacity_weight": 2,
		"controller": {
			"role": "follower"
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 0
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": []
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
					Addr:            "127.0.0.1:7002",
					Status:          "alive",
					LastHeartbeatAt: lastHeartbeatAt,
					ControllerRole:  "follower",
					SlotCount:       3,
					LeaderSlotCount: 0,
					IsLocal:         false,
					CapacityWeight:  2,
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
		"addr": "127.0.0.1:7002",
		"status": "alive",
		"last_heartbeat_at": "2026-04-22T02:05:06Z",
		"is_local": false,
		"capacity_weight": 2,
		"controller": {
			"role": "follower"
		},
		"slot_stats": {
			"count": 3,
			"leader_count": 0
		},
		"slots": {
			"hosted_ids": [2, 4, 7],
			"leader_ids": []
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
				State: managementusecase.SlotState{
					Quorum: "ready",
					Sync:   "matched",
				},
				Assignment: managementusecase.SlotAssignment{
					DesiredPeers:   []uint64{1, 2, 3},
					ConfigEpoch:    8,
					BalanceVersion: 3,
				},
				Runtime: managementusecase.SlotRuntime{
					CurrentPeers:        []uint64{1, 2, 3},
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
			"state": {
				"quorum": "ready",
				"sync": "matched"
			},
			"assignment": {
				"desired_peers": [1, 2, 3],
				"config_epoch": 8,
				"balance_version": 3
			},
			"runtime": {
				"current_peers": [1, 2, 3],
				"leader_id": 1,
				"healthy_voters": 3,
				"has_quorum": true,
				"observed_config_epoch": 8,
				"last_report_at": "2026-04-21T16:00:00Z"
			}
		}]
	}`, rec.Body.String())
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
						Quorum: "ready",
						Sync:   "matched",
					},
					Assignment: managementusecase.SlotAssignment{
						DesiredPeers:   []uint64{2, 3, 5},
						ConfigEpoch:    8,
						BalanceVersion: 3,
					},
					Runtime: managementusecase.SlotRuntime{
						CurrentPeers:        []uint64{2, 3, 5},
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
			"sync": "matched"
		},
		"assignment": {
			"desired_peers": [2, 3, 5],
			"config_epoch": 8,
			"balance_version": 3
		},
		"runtime": {
			"current_peers": [2, 3, 5],
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
			"sync": "unreported"
		},
		"assignment": {
			"desired_peers": null,
			"config_epoch": 0,
			"balance_version": 0
		},
		"runtime": {
			"current_peers": [7, 8, 9],
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
						Quorum: "ready",
						Sync:   "matched",
					},
					Assignment: managementusecase.SlotAssignment{
						DesiredPeers:   []uint64{2, 3, 5},
						ConfigEpoch:    8,
						BalanceVersion: 3,
					},
					Runtime: managementusecase.SlotRuntime{
						CurrentPeers:        []uint64{2, 3, 5},
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
				"sync": "matched"
			},
			"assignment": {
				"desired_peers": [2, 3, 5],
				"config_epoch": 8,
				"balance_version": 3
			},
			"runtime": {
				"current_peers": [2, 3, 5],
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
					ChannelID:    "g2",
					ChannelType:  2,
					SlotID:       2,
					ChannelEpoch: 11,
					LeaderEpoch:  5,
					Leader:       3,
					Replicas:     []uint64{3, 4},
					ISR:          []uint64{3},
					MinISR:       1,
					Status:       "active",
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
			"status": "active"
		}],
		"has_more": true,
		"next_cursor": %q
	}`, mustEncodeChannelRuntimeMetaCursorForTest(t, nextCursor)), rec.Body.String())
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
					ChannelID:    "g1",
					ChannelType:  2,
					SlotID:       7,
					ChannelEpoch: 12,
					LeaderEpoch:  6,
					Leader:       3,
					Replicas:     []uint64{3, 5, 8},
					ISR:          []uint64{3, 5},
					MinISR:       2,
					Status:       "active",
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
	require.JSONEq(t, `{
		"total": 1,
		"items": [{
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
	var received uint64
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
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/202", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(202), received)
	require.JSONEq(t, `{
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
	nodes                           []managementusecase.Node
	nodesErr                        error
	nodeDetail                      managementusecase.NodeDetail
	nodeDetailErr                   error
	nodeDraining                    managementusecase.NodeDetail
	nodeDrainingErr                 error
	nodeResume                      managementusecase.NodeDetail
	nodeResumeErr                   error
	slotLeaderTransfer              managementusecase.SlotDetail
	slotLeaderTransferErr           error
	slotRecover                     managementusecase.SlotRecoverResult
	slotRecoverErr                  error
	slotRebalance                   managementusecase.SlotRebalanceResult
	slotRebalanceErr                error
	slots                           []managementusecase.Slot
	slotsErr                        error
	slotDetail                      managementusecase.SlotDetail
	slotDetailErr                   error
	tasks                           []managementusecase.Task
	tasksErr                        error
	task                            managementusecase.TaskDetail
	taskErr                         error
	connections                     []managementusecase.Connection
	connectionsErr                  error
	connectionDetailReqSink         *uint64
	connectionDetail                managementusecase.ConnectionDetail
	connectionDetailErr             error
	channelRuntimeMetaReqSink       *managementusecase.ListChannelRuntimeMetaRequest
	channelRuntimeMetaPage          managementusecase.ListChannelRuntimeMetaResponse
	channelRuntimeMetaErr           error
	channelRuntimeMetaDetailReqSink *channelRuntimeMetaDetailCall
	channelRuntimeMetaDetail        managementusecase.ChannelRuntimeMetaDetail
	channelRuntimeMetaDetailErr     error
	messagesReqSink                 *managementusecase.ListMessagesRequest
	messagesPage                    managementusecase.ListMessagesResponse
	messagesErr                     error
	overview                        managementusecase.Overview
	overviewErr                     error
}

func (s managementStub) ListNodes(context.Context) ([]managementusecase.Node, error) {
	return append([]managementusecase.Node(nil), s.nodes...), s.nodesErr
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

func (s managementStub) ListSlots(context.Context) ([]managementusecase.Slot, error) {
	return append([]managementusecase.Slot(nil), s.slots...), s.slotsErr
}

func (s managementStub) GetSlot(context.Context, uint32) (managementusecase.SlotDetail, error) {
	return s.slotDetail, s.slotDetailErr
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

func (s managementStub) ListConnections(context.Context) ([]managementusecase.Connection, error) {
	return append([]managementusecase.Connection(nil), s.connections...), s.connectionsErr
}

func (s managementStub) GetConnection(_ context.Context, sessionID uint64) (managementusecase.ConnectionDetail, error) {
	if s.connectionDetailReqSink != nil {
		*s.connectionDetailReqSink = sessionID
	}
	return s.connectionDetail, s.connectionDetailErr
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

func (s managementStub) ListMessages(_ context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error) {
	if s.messagesReqSink != nil {
		*s.messagesReqSink = req
	}
	return s.messagesPage, s.messagesErr
}

func (s managementStub) GetOverview(context.Context) (managementusecase.Overview, error) {
	return s.overview, s.overviewErr
}

type channelRuntimeMetaDetailCall struct {
	channelID   string
	channelType int64
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
