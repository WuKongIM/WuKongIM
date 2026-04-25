package manager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerSlotLeaderTransferRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferRejectsInsufficientPermission(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferRejectsInvalidSlotID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/bad/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid slot_id"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferRejectsInvalidBody(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid body"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferRejectsInvalidTargetNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":0}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid target_node_id"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotLeaderTransferErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"slot not found"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferRejectsUnassignedTarget(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotLeaderTransferErr: management.ErrTargetNodeNotAssigned},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":9}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"target_node_id is not a desired peer"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotLeaderTransferErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"slot leader unavailable"}`, rec.Body.String())
}

func TestManagerSlotLeaderTransferReturnsUpdatedSlotDetail(t *testing.T) {
	lastReportAt := time.Date(2026, 4, 22, 1, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{
			slotLeaderTransfer: management.SlotDetail{
				Slot: management.Slot{
					SlotID: 2,
					State: management.SlotState{
						Quorum: "ready",
						Sync:   "matched",
					},
					Assignment: management.SlotAssignment{
						DesiredPeers:   []uint64{1, 2, 3},
						ConfigEpoch:    8,
						BalanceVersion: 3,
					},
					Runtime: management.SlotRuntime{
						CurrentPeers:        []uint64{1, 2, 3},
						LeaderID:            3,
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
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
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
			"leader_id": 3,
			"healthy_voters": 3,
			"has_quorum": true,
			"observed_config_epoch": 8,
			"last_report_at": "2026-04-22T01:00:00Z"
		},
		"task": null
	}`, rec.Body.String())
}

func (s managementStub) TransferSlotLeader(context.Context, uint32, uint64) (management.SlotDetail, error) {
	return s.slotLeaderTransfer, s.slotLeaderTransferErr
}
