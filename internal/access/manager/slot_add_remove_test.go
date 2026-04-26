package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerSlotAddRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotAddRejectsInsufficientPermission(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotAddReturnsCreatedSlotDetail(t *testing.T) {
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
			slotAdd: management.SlotDetail{
				Slot: management.Slot{
					SlotID: 3,
					State: management.SlotState{
						Quorum: "ready",
						Sync:   "matched",
					},
					Assignment: management.SlotAssignment{
						DesiredPeers:   []uint64{1, 2, 3},
						ConfigEpoch:    7,
						BalanceVersion: 4,
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"slot_id": 3,
		"state": {
			"quorum": "ready",
			"sync": "matched"
		},
		"assignment": {
			"desired_peers": [1, 2, 3],
			"config_epoch": 7,
			"balance_version": 4
		},
		"runtime": {
			"current_peers": null,
			"leader_id": 0,
			"healthy_voters": 0,
			"has_quorum": false,
			"observed_config_epoch": 0,
			"last_report_at": "0001-01-01T00:00:00Z"
		},
		"task": null
	}`, rec.Body.String())
}

func TestManagerSlotAddMapsConflictsAndUnavailableErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		body string
	}{
		{
			name: "migration in progress",
			err:  management.ErrSlotMigrationsInProgress,
			code: http.StatusConflict,
			body: `{"error":"conflict","message":"slot migrations already in progress"}`,
		},
		{
			name: "leader unavailable",
			err:  raftcluster.ErrNoLeader,
			code: http.StatusServiceUnavailable,
			body: `{"error":"service_unavailable","message":"slot add unavailable"}`,
		},
		{
			name: "invalid operation",
			err:  raftcluster.ErrInvalidConfig,
			code: http.StatusBadRequest,
			body: `{"error":"bad_request","message":"invalid slot operation"}`,
		},
		{
			name: "invalid argument",
			err:  controllermeta.ErrInvalidArgument,
			code: http.StatusBadRequest,
			body: `{"error":"bad_request","message":"invalid slot operation"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managementStub{slotAddErr: tt.err}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, tt.code, rec.Code)
			require.JSONEq(t, tt.body, rec.Body.String())
		})
	}
}

func TestManagerSlotDeleteRejectsInvalidSlotID(t *testing.T) {
	srv := New(Options{Management: managementStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/manager/slots/bad", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid slot_id"}`, rec.Body.String())
}

func TestManagerSlotDeleteRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig(nil),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/manager/slots/2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":"unauthorized","message":"unauthorized"}`, rec.Body.String())
}

func TestManagerSlotDeleteRejectsInsufficientPermission(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodDelete, "/manager/slots/2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerSlotDeleteReturnsRemovalStarted(t *testing.T) {
	srv := New(Options{
		Management: managementStub{
			slotRemove: management.SlotRemoveResult{
				SlotID: 2,
				Result: "removal_started",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/manager/slots/2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"slot_id":2,"result":"removal_started"}`, rec.Body.String())
}

func TestManagerSlotDeleteMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		body string
	}{
		{
			name: "migration in progress",
			err:  management.ErrSlotMigrationsInProgress,
			code: http.StatusConflict,
			body: `{"error":"conflict","message":"slot migrations already in progress"}`,
		},
		{
			name: "controller not found",
			err:  controllermeta.ErrNotFound,
			code: http.StatusNotFound,
			body: `{"error":"not_found","message":"slot not found"}`,
		},
		{
			name: "cluster not found",
			err:  raftcluster.ErrSlotNotFound,
			code: http.StatusNotFound,
			body: `{"error":"not_found","message":"slot not found"}`,
		},
		{
			name: "leader unavailable",
			err:  raftcluster.ErrNotLeader,
			code: http.StatusServiceUnavailable,
			body: `{"error":"service_unavailable","message":"slot remove unavailable"}`,
		},
		{
			name: "invalid remove operation",
			err:  raftcluster.ErrInvalidConfig,
			code: http.StatusConflict,
			body: `{"error":"conflict","message":"slot remove unavailable"}`,
		},
		{
			name: "invalid remove argument",
			err:  controllermeta.ErrInvalidArgument,
			code: http.StatusConflict,
			body: `{"error":"conflict","message":"slot remove unavailable"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managementStub{slotRemoveErr: tt.err}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/manager/slots/2", nil)

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, tt.code, rec.Code)
			require.JSONEq(t, tt.body, rec.Body.String())
		})
	}
}
