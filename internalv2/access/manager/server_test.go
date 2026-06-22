package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body loginResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body.Username != "admin" || body.TokenType != "Bearer" || body.AccessToken == "" {
		t.Fatalf("login body = %+v, want username, bearer token", body)
	}
	if body.ExpiresIn != int64(time.Hour/time.Second) {
		t.Fatalf("expires_in = %d, want %d", body.ExpiresIn, int64(time.Hour/time.Second))
	}
	if time.Until(body.ExpiresAt) <= 0 {
		t.Fatalf("expires_at = %s, want future timestamp", body.ExpiresAt)
	}
	if len(body.Permissions) != 1 || body.Permissions[0].Resource != "cluster.node" || len(body.Permissions[0].Actions) != 1 || body.Permissions[0].Actions[0] != "r" {
		t.Fatalf("permissions = %#v, want copied grants", body.Permissions)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("Unmarshal raw() error = %v", err)
	}
	if _, ok := raw["token"]; ok {
		t.Fatalf("legacy token field present in response: %s", rec.Body.String())
	}
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

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"invalid_credentials","message":"invalid credentials"}`) {
		t.Fatalf("body = %q, want invalid credentials error", rec.Body.String())
	}
}

func TestManagerLoginRouteDisabledWhenAuthOff(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerNodesReturnsReadOnlyInventory(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			nodes: managementusecase.NodeList{
				GeneratedAt:        generatedAt,
				ControllerLeaderID: 1,
				Items: []managementusecase.Node{{
					NodeID:          1,
					Name:            "node-1",
					Addr:            "127.0.0.1:7011",
					Status:          "alive",
					LastHeartbeatAt: generatedAt,
					IsLocal:         true,
					CapacityWeight:  1,
					Membership: managementusecase.NodeMembership{
						Role:        "data",
						JoinState:   "active",
						Schedulable: true,
					},
					Health: managementusecase.NodeHealth{
						Status:          "alive",
						LastHeartbeatAt: generatedAt,
					},
					Controller: managementusecase.NodeController{
						Role:       "leader",
						Voter:      true,
						LeaderID:   1,
						RaftHealth: "unknown",
					},
					Slots: managementusecase.NodeSlotSummary{
						ReplicaCount: 2,
						LeaderCount:  1,
					},
					Runtime: managementusecase.NodeRuntimeSummary{
						NodeID:  1,
						Unknown: true,
					},
					Actions: managementusecase.NodeActions{},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-16T10:00:00Z",
		"controller_leader_id": 1,
		"total": 1,
		"items": [{
			"node_id": 1,
			"name": "node-1",
			"addr": "127.0.0.1:7011",
			"status": "alive",
			"last_heartbeat_at": "2026-06-16T10:00:00Z",
			"is_local": true,
			"capacity_weight": 1,
			"membership": {
				"role": "data",
				"join_state": "active",
				"schedulable": true
			},
			"health": {
				"status": "alive",
				"last_heartbeat_at": "2026-06-16T10:00:00Z"
			},
			"controller": {
				"role": "leader",
				"voter": true,
				"leader_id": 1,
				"raft_health": "unknown",
				"first_index": 0,
				"applied_index": 0,
				"snapshot_index": 0
			},
			"slot_stats": {
				"count": 2,
				"leader_count": 1
			},
			"slots": {
				"replica_count": 2,
				"leader_count": 1,
				"follower_count": 0,
				"quorum_lost_count": 0,
				"unreported_count": 0
			},
			"runtime": {
				"node_id": 1,
				"active_online": 0,
				"closing_online": 0,
				"total_online": 0,
				"gateway_sessions": 0,
				"sessions_by_listener": {},
				"accepting_new_sessions": false,
				"draining": false,
				"unknown": true
			},
			"actions": {
				"can_drain": false,
				"can_resume": false,
				"can_scale_in": false,
				"can_onboard": false
			}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodesRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/nodes", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerRuntimeWorkqueuesReturnsLocalTopPressure(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	provider := &managerTopStub{snapshot: accessapi.TopSnapshot{
		GeneratedAt:   generatedAt,
		WindowSeconds: 10,
		Scope:         "local_node",
		Node: accessapi.TopNodeSnapshot{
			ID:    1,
			Name:  "node-1",
			Ready: true,
		},
		Pressure: &accessapi.TopPressure{
			OverallLevel: "degraded",
			Top: []accessapi.TopPressureItem{{
				Component:            "gateway",
				Pool:                 "async_send",
				Queue:                "send",
				Priority:             "none",
				Level:                "degraded",
				Score:                0.82,
				Depth:                82,
				Capacity:             100,
				WaitP99MS:            12.4,
				TaskP99MS:            20.5,
				AdmissionErrorPerSec: 0.3,
				Hint:                 "queue depth is approaching capacity",
			}},
		},
		Sources: accessapi.TopSources{
			Collector: accessapi.TopSourceStatus{Available: true, SampleCount: 10},
			Metrics:   accessapi.TopMetricsSource{Enabled: false, Required: false},
		},
	}}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Top: provider,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.View != accessapi.TopViewRuntime {
		t.Fatalf("provider query view = %q, want %q", provider.query.View, accessapi.TopViewRuntime)
	}
	if provider.query.Window != 10*time.Second {
		t.Fatalf("provider query window = %s, want 10s", provider.query.Window)
	}
	if provider.query.Limit != 100 {
		t.Fatalf("provider query limit = %d, want 100", provider.query.Limit)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-16T10:00:00Z",
		"window_seconds": 10,
		"scope": {"view":"local_node","node_id":1,"node_name":"node-1","ready":true},
		"summary": {
			"overall_level":"degraded",
			"total":1,
			"ok":0,
			"busy":0,
			"degraded":1,
			"critical":0,
			"hottest":{"component":"gateway","pool":"async_send","queue":"send","priority":"none","level":"degraded","score":0.82}
		},
		"items":[{
			"component":"gateway","pool":"async_send","queue":"send","priority":"none","level":"degraded","score":0.82,
			"depth":82,"capacity":100,"inflight":0,"workers":0,
			"wait_p99_ms":12.4,"task_p99_ms":20.5,"admission_error_per_sec":0.3,
			"hint":"queue depth is approaching capacity"
		}],
		"sources":{"collector":{"available":true,"sample_count":10},"metrics":{"enabled":false,"required":false},"notes":[]}
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesPreservesProviderLabels(t *testing.T) {
	response := mapRuntimeWorkqueuesSnapshot(accessapi.TopSnapshot{
		Pressure: &accessapi.TopPressure{
			OverallLevel: "busy",
			Top: []accessapi.TopPressureItem{
				{
					Component: "transportv2",
					Pool:      "slot propose",
					Queue:     "inflight",
					Priority:  "none",
					Level:     "busy",
					Score:     0.7,
				},
				{
					Component: "slot",
					Pool:      "scheduler",
					Queue:     "scheduler",
					Priority:  "none",
					Level:     "ok",
				},
			},
		},
	}, 10*time.Second)

	if got, want := response.Items[0].Pool, "slot propose"; got != want {
		t.Fatalf("pool = %q, want %q", got, want)
	}
	if got, want := response.Items[0].Queue, "inflight"; got != want {
		t.Fatalf("queue = %q, want %q", got, want)
	}
	if response.Summary.Hottest == nil || response.Summary.Hottest.Pool != "slot propose" || response.Summary.Hottest.Queue != "inflight" {
		t.Fatalf("hottest = %#v, want provider pool and queue labels", response.Summary.Hottest)
	}
}

func TestManagerRuntimeWorkqueuesMapsUnavailableSources(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"top snapshot provider is not configured"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesMapsWarmingUp(t *testing.T) {
	srv := New(Options{Top: &managerTopStub{err: accessapi.ErrTopWarmingUp}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"top collector warming up"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesRejectsInvalidWindow(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "invalid duration", url: "/manager/runtime/workqueues?window=soon"},
		{name: "below minimum", url: "/manager/runtime/workqueues?window=1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Top: &managerTopStub{}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"invalid_request","message":"window invalid"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerRuntimeWorkqueuesRejectsInvalidLimit(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "invalid integer", url: "/manager/runtime/workqueues?limit=many"},
		{name: "zero", url: "/manager/runtime/workqueues?limit=0"},
		{name: "negative", url: "/manager/runtime/workqueues?limit=-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Top: &managerTopStub{}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"invalid_request","message":"limit invalid"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerRuntimeWorkqueuesPassesCustomWindow(t *testing.T) {
	provider := &managerTopStub{}
	srv := New(Options{Top: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues?window=30s", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.View != accessapi.TopViewRuntime {
		t.Fatalf("provider query view = %q, want %q", provider.query.View, accessapi.TopViewRuntime)
	}
	if provider.query.Window != 30*time.Second {
		t.Fatalf("provider query window = %s, want 30s", provider.query.Window)
	}
	if provider.query.Limit != 100 {
		t.Fatalf("provider query limit = %d, want 100", provider.query.Limit)
	}
}

func TestManagerRuntimeWorkqueuesParsesNodeID(t *testing.T) {
	provider := &managerTopStub{snapshot: accessapi.TopSnapshot{
		GeneratedAt:   time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
		WindowSeconds: 10,
		Scope:         "local_node",
		Node:          accessapi.TopNodeSnapshot{ID: 2, Name: "node-2", Ready: true},
		Sources: accessapi.TopSources{
			Collector: accessapi.TopSourceStatus{Available: true, SampleCount: 10},
			Metrics:   accessapi.TopMetricsSource{Enabled: false, Required: false},
		},
	}}
	srv := New(Options{Top: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues?window=30s&node_id=2", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.NodeID != 2 {
		t.Fatalf("provider query NodeID = %d, want 2", provider.query.NodeID)
	}
}

func TestManagerRuntimeWorkqueuesRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{Top: &managerTopStub{}})

	for _, path := range []string{
		"/manager/runtime/workqueues?node_id=bad",
		"/manager/runtime/workqueues?node_id=0",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		srv.Engine().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d; body=%s", path, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if !jsonEqual(rec.Body.String(), `{"error":"invalid_request","message":"invalid node_id"}`) {
			t.Fatalf("%s body = %s, want invalid node_id", path, rec.Body.String())
		}
	}
}

func TestManagerRuntimeWorkqueuesClampsLargeLimit(t *testing.T) {
	provider := &managerTopStub{}
	srv := New(Options{Top: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues?limit=250", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.View != accessapi.TopViewRuntime {
		t.Fatalf("provider query view = %q, want %q", provider.query.View, accessapi.TopViewRuntime)
	}
	if provider.query.Window != 10*time.Second {
		t.Fatalf("provider query window = %s, want 10s", provider.query.Window)
	}
	if provider.query.Limit != 200 {
		t.Fatalf("provider query limit = %d, want 200", provider.query.Limit)
	}
}

func TestManagerRuntimeWorkqueuesRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Top: &managerTopStub{},
	})

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerRealtimeMonitorReturnsUnifiedPayload(t *testing.T) {
	generatedAt := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	provider := &managerMonitorStub{response: RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusReady,
		GeneratedAt:   generatedAt,
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope: RealtimeMonitorScope{
			View:   RealtimeMonitorScopeUnified,
			NodeID: 1,
		},
		Sources: RealtimeMonitorSources{
			Prometheus: RealtimeMonitorPrometheusSource{
				Enabled: true,
				BaseURL: "http://127.0.0.1:9090",
				QueryMS: 18,
			},
			ControlSnapshot: RealtimeMonitorSource{
				Enabled: true,
				QueryMS: 1,
			},
		},
		Categories: []RealtimeMonitorCategory{{
			Key:   RealtimeMonitorCategoryCommon,
			Count: 2,
		}, {
			Key:   RealtimeMonitorCategoryGateway,
			Count: 1,
		}, {
			Key:   RealtimeMonitorCategoryInternal,
			Count: 1,
		}},
		Snapshot: []RealtimeMonitorSnapshotEntry{},
		Cards: []RealtimeMonitorCard{{
			Key:       "sendRate",
			Category:  RealtimeMonitorCategoryGateway,
			Stage:     RealtimeMonitorStageSendEntry,
			Source:    RealtimeMonitorSourcePrometheus,
			Tone:      RealtimeMonitorToneNormal,
			Unit:      "msg/s",
			Value:     12.5,
			Available: true,
			Series: []RealtimeMonitorPoint{{
				Timestamp: 1781767200000,
				Value:     12.5,
			}},
			Stats: []RealtimeMonitorStat{{
				Key:   "avg",
				Value: 12.5,
			}},
		}, {
			Key:       "rpcSuccessRate",
			Category:  RealtimeMonitorCategoryInternal,
			Stage:     RealtimeMonitorStageInternalNetwork,
			Source:    RealtimeMonitorSourcePrometheus,
			Tone:      RealtimeMonitorToneNormal,
			Unit:      "%",
			Value:     99.96,
			Available: true,
			Series: []RealtimeMonitorPoint{{
				Timestamp: 1781767200000,
				Value:     99.96,
			}},
			Stats: []RealtimeMonitorStat{},
		}},
	}}
	srv := New(Options{RealtimeMonitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor?window=15m&step=20s&category=internal", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.Window != 15*time.Minute || provider.query.Step != 20*time.Second {
		t.Fatalf("provider query = %#v, want 15m/20s", provider.query)
	}
	if provider.query.Category != RealtimeMonitorCategoryInternal {
		t.Fatalf("provider query category = %q, want %q", provider.query.Category, RealtimeMonitorCategoryInternal)
	}
	if !jsonEqual(rec.Body.String(), `{
		"status":"ready",
		"generated_at":"2026-06-18T10:00:00Z",
		"window_seconds":900,
		"step_seconds":20,
		"scope":{"view":"realtime_monitor","node_id":1},
		"sources":{
			"prometheus":{"enabled":true,"base_url":"http://127.0.0.1:9090","query_ms":18,"error":""},
			"control_snapshot":{"enabled":true,"query_ms":1,"error":""}
		},
		"categories":[
			{"key":"common","count":2},
			{"key":"gateway","count":1},
			{"key":"internal","count":1}
		],
		"snapshot":[],
		"cards":[
			{"key":"sendRate","category":"gateway","stage":"sendEntry","source":"prometheus","tone":"normal","unit":"msg/s","value":12.5,"series":[{"timestamp":1781767200000,"value":12.5}],"stats":[{"key":"avg","value":12.5}],"available":true,"error":""},
			{"key":"rpcSuccessRate","category":"internal","stage":"internalNetwork","source":"prometheus","tone":"normal","unit":"%","value":99.96,"series":[{"timestamp":1781767200000,"value":99.96}],"stats":[],"available":true,"error":""}
		]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRealtimeMonitorParsesNodeID(t *testing.T) {
	provider := &managerMonitorStub{response: RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusReady,
		GeneratedAt:   time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC),
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopeUnified, NodeID: 2},
		Snapshot:      []RealtimeMonitorSnapshotEntry{},
		Cards:         []RealtimeMonitorCard{},
	}}
	srv := New(Options{RealtimeMonitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor?window=15m&node_id=2", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.NodeID != 2 {
		t.Fatalf("provider query NodeID = %d, want 2", provider.query.NodeID)
	}
}

func TestManagerRealtimeMonitorDefaultsToCommonCategory(t *testing.T) {
	provider := &managerMonitorStub{response: RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusReady,
		GeneratedAt:   time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC),
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopeUnified},
		Snapshot:      []RealtimeMonitorSnapshotEntry{},
		Cards:         []RealtimeMonitorCard{},
	}}
	srv := New(Options{RealtimeMonitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.Category != "common" {
		t.Fatalf("provider query category = %q, want common", provider.query.Category)
	}
}

func TestManagerRealtimeMonitorRejectsInvalidQuery(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "invalid window", url: "/manager/realtime-monitor?window=soon", want: "window invalid"},
		{name: "unsupported window", url: "/manager/realtime-monitor?window=2h", want: "window invalid"},
		{name: "invalid step", url: "/manager/realtime-monitor?step=soon", want: "step invalid"},
		{name: "too small step", url: "/manager/realtime-monitor?step=1s", want: "step invalid"},
		{name: "invalid node id", url: "/manager/realtime-monitor?node_id=bad", want: "invalid node_id"},
		{name: "zero node id", url: "/manager/realtime-monitor?node_id=0", want: "invalid node_id"},
		{name: "all category removed", url: "/manager/realtime-monitor?category=all", want: "category invalid"},
		{name: "invalid category", url: "/manager/realtime-monitor?category=business", want: "category invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{RealtimeMonitor: &managerMonitorStub{}})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"invalid_request","message":"`+tt.want+`"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerRealtimeMonitorRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		RealtimeMonitor: &managerMonitorStub{},
	})

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerRealtimeMonitorRemovesOldRoutes(t *testing.T) {
	provider := &managerMonitorStub{}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "node-reader",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			},
		}),
		RealtimeMonitor: provider,
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	for _, path := range []string{"/manager/monitor/realtime", "/manager/cluster-monitor/realtime"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "node-reader"))
		srv.Engine().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "node-reader"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, want %d; body=%s", allowed.Code, http.StatusOK, allowed.Body.String())
	}
	if provider.query.Window != 15*time.Minute || provider.query.Step != 20*time.Second {
		t.Fatalf("allowed provider query = %#v, want default 15m window and 20s step", provider.query)
	}
}

func TestManagerSlotsReturnsReadOnlyInventory(t *testing.T) {
	reportedAt := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	var gotOpts managementusecase.ListSlotsOptions
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastSlotsOptions: &gotOpts,
			slots: []managementusecase.Slot{{
				SlotID: 9,
				HashSlots: &managementusecase.SlotHashSlots{
					Count: 2,
					Items: []uint16{3, 4},
				},
				State: managementusecase.SlotState{
					Quorum:      "ready",
					Sync:        "matched",
					LeaderMatch: true,
				},
				Assignment: managementusecase.SlotAssignment{
					DesiredPeers:    []uint64{1, 2},
					PreferredLeader: 1,
					ConfigEpoch:     7,
				},
				Task: &managementusecase.SlotTask{
					TaskID:           "slot-9-bootstrap-7",
					Kind:             "bootstrap",
					Step:             "create_slot",
					Status:           "pending",
					TargetNode:       1,
					TargetPeers:      []uint64{1, 2},
					CompletionPolicy: "all_target_peers",
					ConfigEpoch:      7,
					Participants: []managementusecase.SlotTaskParticipant{
						{NodeID: 1, Status: "done"},
						{NodeID: 2, Attempt: 1, Status: "failed", LastError: "open failed"},
					},
				},
				Runtime: managementusecase.SlotRuntime{
					CurrentPeers:        []uint64{1, 2},
					CurrentVoters:       []uint64{1, 2},
					PreferredLeaderID:   1,
					HealthyVoters:       2,
					HasQuorum:           true,
					ObservedConfigEpoch: 7,
					LastReportAt:        reportedAt,
				},
				NodeLog: &managementusecase.SlotNodeLogStatus{
					NodeID:       2,
					LeaderID:     1,
					Role:         "follower",
					CommitIndex:  93,
					AppliedIndex: 91,
				},
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotOpts.NodeID != 2 {
		t.Fatalf("node filter = %d, want 2", gotOpts.NodeID)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"slot_id": 9,
			"hash_slots": {
				"count": 2,
				"items": [3, 4]
			},
			"state": {
				"quorum": "ready",
				"sync": "matched",
				"leader_match": true,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": [1, 2],
				"preferred_leader_id": 1,
				"config_epoch": 7,
				"balance_version": 0
			},
			"task": {
				"task_id": "slot-9-bootstrap-7",
				"kind": "bootstrap",
				"step": "create_slot",
				"status": "pending",
				"target_node": 1,
				"target_peers": [1, 2],
				"completion_policy": "all_target_peers",
				"config_epoch": 7,
				"attempt": 0,
				"participants": [{
					"node_id": 1,
					"attempt": 0,
					"status": "done"
				}, {
					"node_id": 2,
					"attempt": 1,
					"status": "failed",
					"last_error": "open failed"
				}]
			},
				"runtime": {
					"current_peers": [1, 2],
					"current_voters": [1, 2],
					"preferred_leader_id": 1,
					"healthy_voters": 2,
					"has_quorum": true,
					"observed_config_epoch": 7,
				"last_report_at": "2026-06-16T11:00:00Z"
			},
			"node_log": {
				"node_id": 2,
				"leader_id": 1,
				"role": "follower",
				"commit_index": 93,
				"applied_index": 91
			}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotsRequiresSlotReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/slots", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerBusinessChannelsReturnsReadOnlyList(t *testing.T) {
	nextCursor := managementusecase.ChannelListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 2, TypeFilter: 2, KeywordHash: 0xabc}
	var gotReq managementusecase.ListBusinessChannelsRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastBusinessChannelsRequest: &gotReq,
			businessChannels: managementusecase.ListBusinessChannelsResponse{
				Items: []managementusecase.BusinessChannelListItem{{
					ChannelID:                 "g1",
					ChannelType:               2,
					SlotID:                    9,
					HashSlot:                  7,
					Ban:                       true,
					SendBan:                   true,
					SubscriberMutationVersion: 5,
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?node_id=2&type=2&keyword=g&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.NodeID != 2 || gotReq.TypeFilter != 2 || gotReq.Keyword != "g" || gotReq.Limit != 1 {
		t.Fatalf("request = %#v, want node 2 type 2 keyword g limit 1", gotReq)
	}
	var body struct {
		Items      []BusinessChannelListItemDTO `json:"items"`
		HasMore    bool                         `json:"has_more"`
		NextCursor string                       `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChannelID != "g1" || body.Items[0].ChannelType != 2 || body.Items[0].SlotID != 9 || body.Items[0].HashSlot != 7 || !body.Items[0].Ban || !body.Items[0].SendBan || body.Items[0].Disband || body.Items[0].SubscriberMutationVersion != 5 {
		t.Fatalf("items = %#v, want business channel row", body.Items)
	}
	if !body.HasMore || body.NextCursor == "" {
		t.Fatalf("pagination = has_more:%t next:%q, want next cursor", body.HasMore, body.NextCursor)
	}
	decoded, err := decodeBusinessChannelCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeBusinessChannelCursor() error = %v", err)
	}
	if decoded != nextCursor {
		t.Fatalf("decoded cursor = %#v, want %#v", decoded, nextCursor)
	}
}

func TestManagerChannelRuntimeMetaReturnsClusterRuntimeList(t *testing.T) {
	nextCursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 2}
	var gotReq managementusecase.ListChannelRuntimeMetaRequest
	maxSeq := uint64(88)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastChannelRuntimeMetaRequest: &gotReq,
			channelRuntimeMeta: managementusecase.ListChannelRuntimeMetaResponse{
				Items: []managementusecase.ChannelRuntimeMeta{{
					ChannelID:     "g1",
					ChannelType:   2,
					SlotID:        9,
					ChannelEpoch:  11,
					LeaderEpoch:   5,
					Leader:        2,
					Replicas:      []uint64{1, 2, 3},
					ISR:           []uint64{1, 2},
					MinISR:        2,
					MaxMessageSeq: &maxSeq,
					Status:        "active",
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?node_id=2&node_scope=leader&include_max_message_seq=true&channel_id=g&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.NodeID != 2 || gotReq.NodeScope != managementusecase.ChannelRuntimeMetaNodeScopeLeader || gotReq.ChannelIDQuery != "g" || gotReq.Limit != 1 || !gotReq.IncludeMaxMessageSeq {
		t.Fatalf("request = %#v, want node 2 leader scope channel g include max seq limit 1", gotReq)
	}
	var body ChannelRuntimeMetaListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChannelID != "g1" || body.Items[0].Leader != 2 || body.Items[0].MaxMessageSeq == nil || *body.Items[0].MaxMessageSeq != 88 {
		t.Fatalf("items = %#v, want runtime meta row with max seq", body.Items)
	}
	if !body.HasMore || body.NextCursor == "" {
		t.Fatalf("pagination = has_more:%t next:%q, want next cursor", body.HasMore, body.NextCursor)
	}
	decoded, err := decodeChannelRuntimeMetaCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeChannelRuntimeMetaCursor() error = %v", err)
	}
	if decoded != nextCursor {
		t.Fatalf("decoded cursor = %#v, want %#v", decoded, nextCursor)
	}
}

func TestManagerBusinessChannelsRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?node_id=bad", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid node_id"}`) {
		t.Fatalf("body = %s, want invalid node_id", rec.Body.String())
	}
}

func TestManagerBusinessChannelsRequiresChannelReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/channels", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerBusinessChannelOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerSlotOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerNodeOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerStartRequiresListenAddr(t *testing.T) {
	srv := New(Options{})

	if err := srv.Start(); err != ErrListenAddrRequired {
		t.Fatalf("Start() error = %v, want %v", err, ErrListenAddrRequired)
	}
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
	if err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
	return token
}

type managerNodesStub struct {
	nodes                              managementusecase.NodeList
	slots                              []managementusecase.Slot
	channelRuntimeMeta                 managementusecase.ListChannelRuntimeMetaResponse
	businessChannels                   managementusecase.ListBusinessChannelsResponse
	recentConversations                managementusecase.RecentConversationsResponse
	messagesPage                       managementusecase.ListMessagesResponse
	connections                        []managementusecase.Connection
	connectionDetail                   managementusecase.ConnectionDetail
	pluginList                         managementusecase.NodePluginList
	pluginDetail                       managementusecase.Plugin
	pluginBindingList                  managementusecase.PluginBindingListResponse
	pluginBindingMutation              managementusecase.PluginBindingMutationResponse
	usersPage                          managementusecase.ListUsersResponse
	userDetail                         managementusecase.UserDetail
	slotLogEntriesPage                 managementusecase.SlotLogEntriesResponse
	controllerLogEntriesPage           managementusecase.ControllerLogEntriesResponse
	controllerTasksResponse            managementusecase.ListControllerTasksResponse
	controllerTask                     managementusecase.ControllerTask
	controllerRaftStatus               managementusecase.ControllerRaftStatus
	controllerRaftCompactResult        managementusecase.ControllerRaftCompactionResult
	controllerRaftCompactSummary       managementusecase.ControllerRaftCompactionSummary
	slotRaftCompactSummary             managementusecase.SlotRaftCompactionSummary
	slotLeaderTransferResponse         managementusecase.SlotLeaderTransferResponse
	slotLeaderTransferBatchPlan        managementusecase.SlotLeaderTransferBatchPlanResponse
	slotLeaderTransferBatchExecute     managementusecase.SlotLeaderTransferBatchExecuteResponse
	diagnosticsResponse                managementusecase.DiagnosticsQueryResponse
	diagnosticsTrackingCreateResponse  managementusecase.DiagnosticsTrackingMutationResponse
	diagnosticsTrackingListResponse    managementusecase.DiagnosticsTrackingListResponse
	diagnosticsTrackingDeleteResponse  managementusecase.DiagnosticsTrackingDeleteResponse
	kickUserResponse                   managementusecase.KickUserResponse
	resetUserTokenResponse             managementusecase.ResetUserTokenResponse
	retentionResult                    managementusecase.AdvanceMessageRetentionResponse
	systemUsers                        managementusecase.ListSystemUsersResponse
	systemUsersMutation                managementusecase.MutateSystemUsersResponse
	lastSlotsOptions                   *managementusecase.ListSlotsOptions
	lastChannelRuntimeMetaRequest      *managementusecase.ListChannelRuntimeMetaRequest
	lastBusinessChannelsRequest        *managementusecase.ListBusinessChannelsRequest
	recentConversationsReqSink         *managementusecase.RecentConversationsRequest
	messagesReqSink                    *managementusecase.ListMessagesRequest
	connectionsReqSink                 *managementusecase.ListConnectionsRequest
	connectionDetailReqSink            *managementusecase.GetConnectionRequest
	pluginListNodeSink                 *uint64
	pluginDetailNodeSink               *uint64
	pluginDetailNoSink                 *string
	pluginBindingListReqSink           *managementusecase.PluginBindingListRequest
	pluginBindingMutationReqSink       *managementusecase.PluginBindingMutationRequest
	pluginBindingUnbindReqSink         *managementusecase.PluginBindingMutationRequest
	slotLogEntriesReqSink              *managementusecase.ListSlotLogEntriesRequest
	controllerLogEntriesReqSink        *managementusecase.ListControllerLogEntriesRequest
	controllerTasksReqSink             *managementusecase.ListControllerTasksRequest
	controllerTaskIDSink               *string
	controllerRaftStatusNodeSink       *uint64
	controllerRaftCompactNodeSink      *uint64
	slotRaftCompactNodeSink            *uint64
	slotRaftCompactSlotSink            *uint32
	slotLeaderTransferReqSink          *managementusecase.SlotLeaderTransferRequest
	slotLeaderTransferBatchPlanSink    *managementusecase.SlotLeaderTransferBatchPlanRequest
	slotLeaderTransferBatchExecuteSink *managementusecase.SlotLeaderTransferBatchExecuteRequest
	diagnosticsReqSink                 *managementusecase.DiagnosticsQueryRequest
	diagnosticsTrackingCreateReqSink   *managementusecase.DiagnosticsTrackingCreateRequest
	diagnosticsTrackingDeleteRuleSink  *string
	retentionReqSink                   *managementusecase.AdvanceMessageRetentionRequest
	usersReqSink                       *managementusecase.ListUsersRequest
	kickUserReqSink                    *managementusecase.KickUserRequest
	resetUserTokenReqSink              *managementusecase.ResetUserTokenRequest
	systemUsersAddReq                  *managementusecase.MutateSystemUsersRequest
	systemUsersRemoveReq               *managementusecase.MutateSystemUsersRequest
	err                                error
	slotsErr                           error
	channelRuntimeMetaErr              error
	businessChannelsErr                error
	recentConversationsErr             error
	messagesErr                        error
	connectionsErr                     error
	connectionDetailErr                error
	pluginListErr                      error
	pluginDetailErr                    error
	pluginBindingListErr               error
	pluginBindingMutationErr           error
	pluginBindingUnbindErr             error
	slotLogEntriesErr                  error
	controllerLogEntriesErr            error
	controllerTasksErr                 error
	controllerTaskErr                  error
	controllerRaftStatusErr            error
	controllerRaftCompactErr           error
	controllerRaftCompactAllErr        error
	slotRaftCompactErr                 error
	slotLeaderTransferErr              error
	slotLeaderTransferBatchPlanErr     error
	slotLeaderTransferBatchExecuteErr  error
	diagnosticsErr                     error
	diagnosticsTrackingCreateErr       error
	diagnosticsTrackingListErr         error
	diagnosticsTrackingDeleteErr       error
	retentionErr                       error
	usersErr                           error
	userDetailErr                      error
	kickUserErr                        error
	resetUserTokenErr                  error
	systemUsersErr                     error
	systemUsersMutationErr             error
}

type managerTopStub struct {
	snapshot accessapi.TopSnapshot
	err      error
	query    accessapi.TopSnapshotQuery
}

type managerMonitorStub struct {
	response RealtimeMonitorResponse
	err      error
	query    RealtimeMonitorQuery
}

func (s *managerTopStub) SnapshotTop(_ context.Context, query accessapi.TopSnapshotQuery) (accessapi.TopSnapshot, error) {
	s.query = query
	if s.err != nil {
		return accessapi.TopSnapshot{}, s.err
	}
	return s.snapshot, nil
}

func (s *managerMonitorStub) RealtimeMonitor(_ context.Context, query RealtimeMonitorQuery) (RealtimeMonitorResponse, error) {
	s.query = query
	if s.err != nil {
		return RealtimeMonitorResponse{}, s.err
	}
	return s.response, nil
}

func testFloat64Ptr(v float64) *float64 {
	return &v
}

func (s managerNodesStub) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return s.nodes, s.err
}

func (s managerNodesStub) ListSlots(_ context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	if s.lastSlotsOptions != nil {
		*s.lastSlotsOptions = opts
	}
	return s.slots, s.slotsErr
}

func (s managerNodesStub) ListChannelRuntimeMeta(_ context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error) {
	if s.lastChannelRuntimeMetaRequest != nil {
		*s.lastChannelRuntimeMetaRequest = req
	}
	return s.channelRuntimeMeta, s.channelRuntimeMetaErr
}

func (s managerNodesStub) ListSlotLogEntries(_ context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	if s.slotLogEntriesReqSink != nil {
		*s.slotLogEntriesReqSink = req
	}
	return s.slotLogEntriesPage, s.slotLogEntriesErr
}

func (s managerNodesStub) ListControllerLogEntries(_ context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	if s.controllerLogEntriesReqSink != nil {
		*s.controllerLogEntriesReqSink = req
	}
	return s.controllerLogEntriesPage, s.controllerLogEntriesErr
}

func (s managerNodesStub) ListControllerTasks(_ context.Context, req managementusecase.ListControllerTasksRequest) (managementusecase.ListControllerTasksResponse, error) {
	if s.controllerTasksReqSink != nil {
		*s.controllerTasksReqSink = req
	}
	return s.controllerTasksResponse, s.controllerTasksErr
}

func (s managerNodesStub) ControllerTask(_ context.Context, taskID string) (managementusecase.ControllerTask, error) {
	if s.controllerTaskIDSink != nil {
		*s.controllerTaskIDSink = taskID
	}
	return s.controllerTask, s.controllerTaskErr
}

func (s managerNodesStub) ControllerRaftStatus(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error) {
	if s.controllerRaftStatusNodeSink != nil {
		*s.controllerRaftStatusNodeSink = nodeID
	}
	return s.controllerRaftStatus, s.controllerRaftStatusErr
}

func (s managerNodesStub) CompactControllerRaftLog(_ context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error) {
	if s.controllerRaftCompactNodeSink != nil {
		*s.controllerRaftCompactNodeSink = nodeID
	}
	return s.controllerRaftCompactResult, s.controllerRaftCompactErr
}

func (s managerNodesStub) CompactControllerRaftLogs(context.Context) (managementusecase.ControllerRaftCompactionSummary, error) {
	return s.controllerRaftCompactSummary, s.controllerRaftCompactAllErr
}

func (s managerNodesStub) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionSummary, error) {
	if s.slotRaftCompactNodeSink != nil {
		*s.slotRaftCompactNodeSink = nodeID
	}
	if s.slotRaftCompactSlotSink != nil {
		*s.slotRaftCompactSlotSink = slotID
	}
	return s.slotRaftCompactSummary, s.slotRaftCompactErr
}

func (s managerNodesStub) RequestSlotLeaderTransfer(_ context.Context, req managementusecase.SlotLeaderTransferRequest) (managementusecase.SlotLeaderTransferResponse, error) {
	if s.slotLeaderTransferReqSink != nil {
		*s.slotLeaderTransferReqSink = req
	}
	return s.slotLeaderTransferResponse, s.slotLeaderTransferErr
}

func (s managerNodesStub) PlanSlotLeaderTransfers(_ context.Context, req managementusecase.SlotLeaderTransferBatchPlanRequest) (managementusecase.SlotLeaderTransferBatchPlanResponse, error) {
	if s.slotLeaderTransferBatchPlanSink != nil {
		*s.slotLeaderTransferBatchPlanSink = req
	}
	return s.slotLeaderTransferBatchPlan, s.slotLeaderTransferBatchPlanErr
}

func (s managerNodesStub) ExecuteSlotLeaderTransferBatch(_ context.Context, req managementusecase.SlotLeaderTransferBatchExecuteRequest) (managementusecase.SlotLeaderTransferBatchExecuteResponse, error) {
	if s.slotLeaderTransferBatchExecuteSink != nil {
		*s.slotLeaderTransferBatchExecuteSink = req
	}
	return s.slotLeaderTransferBatchExecute, s.slotLeaderTransferBatchExecuteErr
}

func (s managerNodesStub) QueryDiagnostics(_ context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error) {
	if s.diagnosticsReqSink != nil {
		*s.diagnosticsReqSink = req
	}
	return s.diagnosticsResponse, s.diagnosticsErr
}

func (s managerNodesStub) CreateDiagnosticsTrackingRule(_ context.Context, req managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error) {
	if s.diagnosticsTrackingCreateReqSink != nil {
		*s.diagnosticsTrackingCreateReqSink = req
	}
	return s.diagnosticsTrackingCreateResponse, s.diagnosticsTrackingCreateErr
}

func (s managerNodesStub) ListDiagnosticsTrackingRules(context.Context) (managementusecase.DiagnosticsTrackingListResponse, error) {
	return s.diagnosticsTrackingListResponse, s.diagnosticsTrackingListErr
}

func (s managerNodesStub) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) (managementusecase.DiagnosticsTrackingDeleteResponse, error) {
	if s.diagnosticsTrackingDeleteRuleSink != nil {
		*s.diagnosticsTrackingDeleteRuleSink = ruleID
	}
	return s.diagnosticsTrackingDeleteResponse, s.diagnosticsTrackingDeleteErr
}

func (s managerNodesStub) ListBusinessChannels(_ context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	if s.lastBusinessChannelsRequest != nil {
		*s.lastBusinessChannelsRequest = req
	}
	return s.businessChannels, s.businessChannelsErr
}

func (s managerNodesStub) ListRecentConversations(_ context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error) {
	if s.recentConversationsReqSink != nil {
		*s.recentConversationsReqSink = req
	}
	return s.recentConversations, s.recentConversationsErr
}

func (s managerNodesStub) ListMessages(_ context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error) {
	if s.messagesReqSink != nil {
		*s.messagesReqSink = req
	}
	return s.messagesPage, s.messagesErr
}

func (s managerNodesStub) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	if s.connectionsReqSink != nil {
		*s.connectionsReqSink = req
	}
	return append([]managementusecase.Connection(nil), s.connections...), s.connectionsErr
}

func (s managerNodesStub) GetConnection(_ context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	if s.connectionDetailReqSink != nil {
		*s.connectionDetailReqSink = req
	}
	return s.connectionDetail, s.connectionDetailErr
}

func (s managerNodesStub) ListNodePlugins(_ context.Context, nodeID uint64) (managementusecase.NodePluginList, error) {
	if s.pluginListNodeSink != nil {
		*s.pluginListNodeSink = nodeID
	}
	return s.pluginList, s.pluginListErr
}

func (s managerNodesStub) GetNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	if s.pluginDetailNodeSink != nil {
		*s.pluginDetailNodeSink = nodeID
	}
	if s.pluginDetailNoSink != nil {
		*s.pluginDetailNoSink = pluginNo
	}
	return s.pluginDetail, s.pluginDetailErr
}

func (s managerNodesStub) ListPluginBindings(_ context.Context, req managementusecase.PluginBindingListRequest) (managementusecase.PluginBindingListResponse, error) {
	if s.pluginBindingListReqSink != nil {
		*s.pluginBindingListReqSink = req
	}
	return s.pluginBindingList, s.pluginBindingListErr
}

func (s managerNodesStub) BindPluginUser(_ context.Context, req managementusecase.PluginBindingMutationRequest) (managementusecase.PluginBindingMutationResponse, error) {
	if s.pluginBindingMutationReqSink != nil {
		*s.pluginBindingMutationReqSink = req
	}
	return s.pluginBindingMutation, s.pluginBindingMutationErr
}

func (s managerNodesStub) UnbindPluginUser(_ context.Context, req managementusecase.PluginBindingMutationRequest) error {
	if s.pluginBindingUnbindReqSink != nil {
		*s.pluginBindingUnbindReqSink = req
	}
	return s.pluginBindingUnbindErr
}

func (s managerNodesStub) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if s.retentionReqSink != nil {
		*s.retentionReqSink = req
	}
	return s.retentionResult, s.retentionErr
}

func (s managerNodesStub) ListUsers(_ context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error) {
	if s.usersReqSink != nil {
		*s.usersReqSink = req
	}
	return s.usersPage, s.usersErr
}

func (s managerNodesStub) GetUser(_ context.Context, _ string) (managementusecase.UserDetail, error) {
	return s.userDetail, s.userDetailErr
}

func (s managerNodesStub) KickUser(_ context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error) {
	if s.kickUserReqSink != nil {
		*s.kickUserReqSink = req
	}
	return s.kickUserResponse, s.kickUserErr
}

func (s managerNodesStub) ResetUserToken(_ context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error) {
	if s.resetUserTokenReqSink != nil {
		*s.resetUserTokenReqSink = req
	}
	return s.resetUserTokenResponse, s.resetUserTokenErr
}

func (s managerNodesStub) ListSystemUsers(context.Context) (managementusecase.ListSystemUsersResponse, error) {
	return s.systemUsers, s.systemUsersErr
}

func (s managerNodesStub) AddSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersAddReq != nil {
		*s.systemUsersAddReq = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func (s managerNodesStub) RemoveSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersRemoveReq != nil {
		*s.systemUsersRemoveReq = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func jsonEqual(got, want string) bool {
	var gotValue any
	var wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		return false
	}
	return deepEqualJSON(gotValue, wantValue)
}

func deepEqualJSON(a, b any) bool {
	encodedA, err := json.Marshal(a)
	if err != nil {
		return false
	}
	encodedB, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(encodedA, encodedB)
}
