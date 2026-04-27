package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerNodeScaleInPlanReturnsReport(t *testing.T) {
	var gotNodeID uint64
	var gotReq managementusecase.NodeScaleInPlanRequest
	srv := New(Options{Management: managementStub{
		nodeScaleInReport:      sampleManagerNodeScaleInReport(),
		nodeScaleInNodeIDSink:  &gotNodeID,
		nodeScaleInPlanReqSink: &gotReq,
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/3/scale-in/plan", strings.NewReader(`{"confirm_statefulset_tail":true,"expected_tail_node_id":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(3), gotNodeID)
	require.True(t, gotReq.ConfirmStatefulSetTail)
	require.Equal(t, uint64(3), gotReq.ExpectedTailNodeID)
	body := decodeManagerScaleInBody(t, rec.Body.String())
	require.Equal(t, float64(3), body["node_id"])
	require.Equal(t, "blocked", body["status"])
	require.Equal(t, false, body["safe_to_remove"])
	require.Equal(t, true, body["connection_safety_verified"])
	require.Equal(t, "slot_replica_count_unknown", body["blocked_reasons"].([]any)[0].(map[string]any)["code"])
	require.Equal(t, float64(2), body["progress"].(map[string]any)["assigned_slot_replicas"])
}

func TestManagerNodeScaleInStartReturnsBlockedReport(t *testing.T) {
	report := sampleManagerNodeScaleInReport()
	srv := New(Options{Management: managementStub{
		nodeScaleInErr: &managementusecase.NodeScaleInReportError{Err: managementusecase.ErrNodeScaleInBlocked, Report: report},
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/3/scale-in/start", strings.NewReader(`{"confirm_statefulset_tail":false}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	body := decodeManagerScaleInBody(t, rec.Body.String())
	require.Equal(t, "scale_in_blocked", body["error"])
	require.Equal(t, "blocked", body["report"].(map[string]any)["status"])
}

func TestManagerNodeScaleInStatusReturnsReport(t *testing.T) {
	srv := New(Options{Management: managementStub{nodeScaleInReport: sampleManagerNodeScaleInReport()}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/3/scale-in/status", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeManagerScaleInBody(t, rec.Body.String())
	require.Equal(t, float64(3), body["node_id"])
	require.Equal(t, "blocked", body["status"])
}

func TestManagerNodeScaleInAdvanceClampsRequest(t *testing.T) {
	var gotReq managementusecase.AdvanceNodeScaleInRequest
	srv := New(Options{Management: managementStub{
		nodeScaleInReport:         sampleManagerNodeScaleInReport(),
		nodeScaleInAdvanceReqSink: &gotReq,
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/3/scale-in/advance", strings.NewReader(`{"max_leader_transfers":99,"force_close_connections":true}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 3, gotReq.MaxLeaderTransfers)
	require.True(t, gotReq.ForceCloseConnections)
}

func TestManagerNodeScaleInCancelReturnsReport(t *testing.T) {
	srv := New(Options{Management: managementStub{nodeScaleInReport: sampleManagerNodeScaleInReport()}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/3/scale-in/cancel", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeManagerScaleInBody(t, rec.Body.String())
	require.Equal(t, float64(3), body["node_id"])
}

func TestManagerNodeScaleInRoutesRequirePermissions(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		permissions []PermissionConfig
	}{
		{name: "plan requires slot read", method: http.MethodPost, path: "/manager/nodes/3/scale-in/plan", permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}}},
		{name: "status requires node read", method: http.MethodGet, path: "/manager/nodes/3/scale-in/status", permissions: []PermissionConfig{{Resource: "cluster.slot", Actions: []string{"r"}}}},
		{name: "start requires slot write", method: http.MethodPost, path: "/manager/nodes/3/scale-in/start", permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"w"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{
				Auth:       testAuthConfig([]UserConfig{{Username: "limited", Password: "secret", Permissions: tc.permissions}}),
				Management: managementStub{nodeScaleInReport: sampleManagerNodeScaleInReport()},
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "limited"))

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
		})
	}
}

func TestManagerNodeScaleInRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{Management: managementStub{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/bad/scale-in/status", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
}

func TestManagerNodeScaleInRejectsInvalidJSONBody(t *testing.T) {
	srv := New(Options{Management: managementStub{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/3/scale-in/plan", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid body"}`, rec.Body.String())
}

func sampleManagerNodeScaleInReport() managementusecase.NodeScaleInReport {
	return managementusecase.NodeScaleInReport{
		NodeID:                   3,
		Status:                   managementusecase.NodeScaleInStatusBlocked,
		SafeToRemove:             false,
		ConnectionSafetyVerified: true,
		Checks: managementusecase.NodeScaleInChecks{
			SlotReplicaCountKnown:    false,
			ControllerReadsAvailable: true,
			TargetNodeFound:          true,
			TargetIsDataNode:         true,
			TargetActiveOrDraining:   true,
			TailNodeMappingVerified:  true,
			RuntimeViewsFresh:        true,
			ConnectionSafetyKnown:    true,
		},
		Progress: managementusecase.NodeScaleInProgress{
			AssignedSlotReplicas:          2,
			ObservedSlotReplicas:          2,
			SlotLeaders:                   1,
			ActiveTasksInvolvingNode:      1,
			ActiveMigrationsInvolvingNode: 0,
			ActiveConnections:             4,
			ClosingConnections:            1,
			GatewaySessions:               5,
			ActiveConnectionsUnknown:      false,
		},
		BlockedReasons: []managementusecase.NodeScaleInBlockedReason{{
			Code:    "slot_replica_count_unknown",
			Message: "slot replica count is not configured",
			NodeID:  3,
		}},
	}
}

func decodeManagerScaleInBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	return body
}
