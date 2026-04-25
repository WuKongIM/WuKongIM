package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerSlotRecoverRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{"strategy":"latest_live_replica"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotRecoverRejectsInvalidBody(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid body"}`, rec.Body.String())
}

func TestManagerSlotRecoverRejectsUnsupportedStrategy(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotRecoverErr: managementusecase.ErrUnsupportedRecoverStrategy},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{"strategy":"unsupported"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"unsupported strategy"}`, rec.Body.String())
}

func TestManagerSlotRecoverReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotRecoverErr: controllermeta.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{"strategy":"latest_live_replica"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"slot not found"}`, rec.Body.String())
}

func TestManagerSlotRecoverReturnsConflictWhenManualRecoveryRequired(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotRecoverErr: raftcluster.ErrManualRecoveryRequired},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{"strategy":"latest_live_replica"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.JSONEq(t, `{"error":"conflict","message":"manual recovery required"}`, rec.Body.String())
}

func TestManagerSlotRecoverReturnsUpdatedOutcome(t *testing.T) {
	lastReportAt := time.Date(2026, 4, 22, 1, 2, 3, 0, time.UTC)
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
			slotRecover: managementusecase.SlotRecoverResult{
				Strategy: "latest_live_replica",
				Result:   "quorum_reachable",
				Slot: managementusecase.SlotDetail{
					Slot: managementusecase.Slot{
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
							LeaderID:            3,
							HealthyVoters:       3,
							HasQuorum:           true,
							ObservedConfigEpoch: 8,
							LastReportAt:        lastReportAt,
						},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/2/recover", bytes.NewBufferString(`{"strategy":"latest_live_replica"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"strategy": "latest_live_replica",
		"result": "quorum_reachable",
		"slot": {
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
				"last_report_at": "2026-04-22T01:02:03Z"
			},
			"task": null
		}
	}`, rec.Body.String())
}

func TestManagerSlotRebalanceRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/rebalance", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotRebalanceReturnsConflictWhenMigrationsInProgress(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotRebalanceErr: managementusecase.ErrSlotMigrationsInProgress},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/rebalance", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.JSONEq(t, `{"error":"conflict","message":"slot migrations already in progress"}`, rec.Body.String())
}

func TestManagerSlotRebalanceReturnsEmptyPlan(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/rebalance", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"total":0,"items":[]}`, rec.Body.String())
}

func TestManagerSlotRebalanceReturnsPlanItems(t *testing.T) {
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
			slotRebalance: managementusecase.SlotRebalanceResult{
				Total: 2,
				Items: []managementusecase.SlotRebalancePlanItem{
					{HashSlot: 4, FromSlotID: 1, ToSlotID: 2},
					{HashSlot: 5, FromSlotID: 1, ToSlotID: 3},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/rebalance", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"total": 2,
		"items": [
			{"hash_slot": 4, "from_slot_id": 1, "to_slot_id": 2},
			{"hash_slot": 5, "from_slot_id": 1, "to_slot_id": 3}
		]
	}`, rec.Body.String())
}

func TestManagerSlotRebalanceReturnsServiceUnavailableWhenLeaderUnavailable(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managementStub{slotRebalanceErr: raftcluster.ErrNoLeader},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/rebalance", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"slot rebalance unavailable"}`, rec.Body.String())
}
