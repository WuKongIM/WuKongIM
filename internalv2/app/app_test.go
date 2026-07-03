package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaycore "github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/bwmarrin/snowflake"
)

func newTestApp(t *testing.T, cfg Config, opts ...Option) (*App, error) {
	t.Helper()
	opts = append([]Option{WithLogger(wklog.NewNop())}, opts...)
	app, err := New(cfg, opts...)
	if app != nil {
		t.Cleanup(app.restoreDiagnosticsSink)
	}
	return app, err
}

func startTestApp(t *testing.T, app *App) {
	t.Helper()
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
}

func mustIssueManagerTokenForAppTest(t *testing.T, srv *accessmanager.Server, username string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"`+username+`","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}
	if body.AccessToken == "" {
		t.Fatalf("login access_token is empty; body=%s", rec.Body.String())
	}
	return body.AccessToken
}

func TestLegacyRouteAddressesPreferExplicitExternalConfig(t *testing.T) {
	cfg := APIConfig{
		ExternalTCPAddr: "im.example.com:15100",
		ExternalWSSAddr: "wss://im.example.com:15300",
	}
	listeners := []gateway.ListenerOptions{
		{Name: "tcp-wkproto", Network: "tcp", Address: "127.0.0.1:5100"},
		{Name: "ws-jsonrpc", Network: "websocket", Address: "127.0.0.1:5200"},
	}

	external, intranet := legacyRouteAddresses(cfg, listeners)

	if external != (accessapi.LegacyRouteAddresses{
		TCPAddr: "im.example.com:15100",
		WSAddr:  "ws://127.0.0.1:5200",
		WSSAddr: "wss://im.example.com:15300",
	}) {
		t.Fatalf("external = %#v", external)
	}
	if intranet != (accessapi.LegacyRouteAddresses{TCPAddr: "127.0.0.1:5100"}) {
		t.Fatalf("intranet = %#v", intranet)
	}
}

func TestLegacyRouteNodeAddressesDeriveRemoteVoterHosts(t *testing.T) {
	external := accessapi.LegacyRouteAddresses{
		TCPAddr: "im-node1.example.com:15100",
		WSAddr:  "ws://im-node1.example.com:15200",
		WSSAddr: "wss://im-node1.example.com:15300",
	}
	intranet := accessapi.LegacyRouteAddresses{TCPAddr: "10.0.0.1:5100"}
	voters := []clusterv2.ControlVoter{
		{NodeID: 1, Addr: "node1.internal:7000"},
		{NodeID: 2, Addr: "node2.internal:7000"},
	}

	nodes := legacyRouteNodeAddresses(1, voters, external, intranet)

	if got := nodes[1]; got != (accessapi.LegacyRouteNodeAddresses{External: external, Intranet: intranet}) {
		t.Fatalf("local node route = %#v", got)
	}
	if got := nodes[2]; got != (accessapi.LegacyRouteNodeAddresses{
		External: accessapi.LegacyRouteAddresses{
			TCPAddr: "node2.internal:15100",
			WSAddr:  "ws://node2.internal:15200",
			WSSAddr: "wss://node2.internal:15300",
		},
		Intranet: accessapi.LegacyRouteAddresses{
			TCPAddr: "node2.internal:5100",
		},
	}) {
		t.Fatalf("remote node route = %#v", got)
	}
}

func TestStartOrderIsClusterThenGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start", got)
	}
}

func TestStartWaitsForSeedJoinAdmissionBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeManagerCluster{
		fakeCluster: fakeCluster{calls: &calls},
		nodeID:      4,
		snapshot: control.Snapshot{
			Nodes: []control.Node{{
				NodeID:    1,
				Addr:      "10.0.0.1:11110",
				Roles:     []control.Role{control.RoleController, control.RoleData},
				JoinState: control.NodeJoinStateActive,
			}},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterv2.Config{
			NodeID: 4,
			Control: clusterv2.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterv2.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "wrong-token",
			},
		},
	}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = app.Start(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want context deadline while waiting for seed join admission", err)
	}
	if got := joinCalls(calls); got != "cluster.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,cluster.stop without gateway.start", got)
	}
}

func TestSeedJoinStartSkipsClusterWriteReadinessBeforeActivation(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &fakeSeedJoinWriteReadyCluster{
		fakeWriteReadyCluster: fakeWriteReadyCluster{
			fakeCluster: fakeCluster{calls: &calls},
			snapshots: []clusterv2.Snapshot{{
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
				HashSlotCount: 1,
			}},
		},
		nodeID: 4,
		controlSnapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 1, Addr: "10.0.0.1:11110", JoinState: control.NodeJoinStateActive},
				{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateJoining},
			},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterv2.Config{
			NodeID: 4,
			Control: clusterv2.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterv2.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start" {
		t.Fatalf("calls = %s, want seed join startup to skip write route/probe before gateway", got)
	}
}

func TestSeedJoinActiveRestartWaitsForClusterWriteReadiness(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &fakeSeedJoinWriteReadyCluster{
		fakeWriteReadyCluster: fakeWriteReadyCluster{
			fakeCluster: fakeCluster{calls: &calls},
			snapshots: []clusterv2.Snapshot{{
				RoutesReady:   true,
				SlotsReady:    true,
				ChannelsReady: true,
				HashSlotCount: 1,
			}},
		},
		nodeID: 4,
		controlSnapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 1, Addr: "10.0.0.1:11110", JoinState: control.NodeJoinStateActive},
				{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateActive},
			},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{
		NodeID: 4,
		Cluster: clusterv2.Config{
			NodeID: 4,
			Control: clusterv2.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterv2.JoinConfig{
				Seeds:         []string{"10.0.0.1:11110"},
				AdvertiseAddr: "10.0.0.4:11110",
				Token:         "join-secret",
			},
		},
	}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = app.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "cluster write readiness") {
		t.Fatalf("Start() error = %v, want cluster write readiness failure", err)
	}
	if got := joinCalls(calls); strings.Contains(got, "gateway.start") {
		t.Fatalf("calls = %s, want no gateway.start before active seed join write readiness", got)
	}
}

func TestGatewayStartFailureStopsCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	logger := &recordingAppLogger{}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway), WithLogger(logger))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop", got)
	}
	requireAppLogEvent(t, logger, "ERROR", "internalv2.app.lifecycle_start_failed")
}

func TestStartOrderIncludesAPIBeforeGatewayWhenConfigured(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start", got)
	}
}

func TestStartStopOrderIncludesManagerBetweenAPIAndGateway(t *testing.T) {
	calls := make([]string, 0, 8)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	manager := &fakeManager{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithManager(manager), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := "cluster.start,api.start,manager.start,gateway.start,gateway.stop,manager.stop,api.stop,cluster.stop"
	if got := joinCalls(calls); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestNewWiresManagerServerWhenListenAddrConfigured(t *testing.T) {
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "*",
					Actions:  []string{"*"},
				}},
			}},
		},
	}, WithCluster(&fakeCluster{}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.manager == nil {
		t.Fatalf("manager server = nil, want configured manager runtime")
	}
}

func TestManagerServerMutatesPluginBindingsFromClusterMetadata(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.plugin",
					Actions:  []string{"r", "w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}
	token := mustIssueManagerTokenForAppTest(t, srv, "admin")

	bindRec := httptest.NewRecorder()
	bindReq := httptest.NewRequest(http.MethodPost, "/manager/plugin-bindings", strings.NewReader(`{"uid":"bot","plugin_no":"receive-plugin"}`))
	bindReq.Header.Set("Authorization", "Bearer "+token)
	bindReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(bindRec, bindReq)
	if bindRec.Code != http.StatusOK {
		t.Fatalf("bind status = %d, want %d; body=%s", bindRec.Code, http.StatusOK, bindRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/manager/plugin-bindings?uid=bot", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listBody struct {
		Total int `json:"total"`
		Items []struct {
			UID      string `json:"uid"`
			PluginNo string `json:"plugin_no"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("Unmarshal list body error = %v", err)
	}
	if listBody.Total != 1 || len(listBody.Items) != 1 || listBody.Items[0].UID != "bot" || listBody.Items[0].PluginNo != "receive-plugin" {
		t.Fatalf("list body = %#v, want one bot/receive-plugin binding", listBody)
	}

	pluginListRec := httptest.NewRecorder()
	pluginListReq := httptest.NewRequest(http.MethodGet, "/manager/plugin-bindings?plugin_no=receive-plugin", nil)
	pluginListReq.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(pluginListRec, pluginListReq)
	if pluginListRec.Code != http.StatusOK {
		t.Fatalf("plugin list status = %d, want %d; body=%s", pluginListRec.Code, http.StatusOK, pluginListRec.Body.String())
	}
	var pluginListBody struct {
		Total int `json:"total"`
		Items []struct {
			UID      string `json:"uid"`
			PluginNo string `json:"plugin_no"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pluginListRec.Body.Bytes(), &pluginListBody); err != nil {
		t.Fatalf("Unmarshal plugin list body error = %v", err)
	}
	if pluginListBody.Total != 1 || len(pluginListBody.Items) != 1 || pluginListBody.Items[0].UID != "bot" || pluginListBody.Items[0].PluginNo != "receive-plugin" {
		t.Fatalf("plugin list body = %#v, want one bot/receive-plugin binding", pluginListBody)
	}

	unbindRec := httptest.NewRecorder()
	unbindReq := httptest.NewRequest(http.MethodDelete, "/manager/plugin-bindings", strings.NewReader(`{"uid":"bot","plugin_no":"receive-plugin"}`))
	unbindReq.Header.Set("Authorization", "Bearer "+token)
	unbindReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(unbindRec, unbindReq)
	if unbindRec.Code != http.StatusOK {
		t.Fatalf("unbind status = %d, want %d; body=%s", unbindRec.Code, http.StatusOK, unbindRec.Body.String())
	}
	if bindings := cluster.pluginBindingsByUID["bot"]; len(bindings) != 0 {
		t.Fatalf("bindings after unbind = %#v, want empty", bindings)
	}
}

func TestManagerServerReceivesTopProviderWhenConfigured(t *testing.T) {
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			}},
		},
		Top: TopConfig{APIEnabled: true},
	}, WithCluster(&fakeManagerCluster{nodeID: 1}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.topProvider == nil {
		t.Fatal("top provider was not wired")
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("runtime workqueues status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "top collector warming up") {
		t.Fatalf("runtime workqueues body = %s, want top collector warming up", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "provider is not configured") {
		t.Fatalf("runtime workqueues body = %s, want configured top provider", rec.Body.String())
	}
}

func TestManagerServerReceivesPrometheusMonitorProviderWhenConfigured(t *testing.T) {
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:18080"},
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			}},
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
			Prometheus: PrometheusConfig{
				Enabled:    true,
				ListenAddr: "127.0.0.1:9090",
			},
		},
	}, WithCluster(&fakeManagerCluster{nodeID: 1}), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor?category=internal", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("monitor status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), accessmanager.RealtimeMonitorStatusPrometheusDisabled) {
		t.Fatalf("monitor body = %s, want configured prometheus provider", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("monitor body = %s, want prometheus source enabled", rec.Body.String())
	}
}

func TestManagerServerListsNodesFromClusterSnapshot(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 2,
		snapshot: control.Snapshot{
			ControllerID: 1,
			Nodes: []control.Node{
				{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
				{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			},
			Slots: []control.SlotAssignment{{
				SlotID:          1,
				DesiredPeers:    []uint64{1, 2},
				PreferredLeader: 1,
			}},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	nodesRec := httptest.NewRecorder()
	nodesReq := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	nodesReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(nodesRec, nodesReq)
	if nodesRec.Code != http.StatusOK {
		t.Fatalf("nodes status = %d, want %d; body=%s", nodesRec.Code, http.StatusOK, nodesRec.Body.String())
	}
	var nodesBody struct {
		ControllerLeaderID uint64 `json:"controller_leader_id"`
		Total              int    `json:"total"`
		Items              []struct {
			NodeID  uint64 `json:"node_id"`
			Addr    string `json:"addr"`
			IsLocal bool   `json:"is_local"`
			Actions struct {
				CanDrain bool `json:"can_drain"`
			} `json:"actions"`
		} `json:"items"`
	}
	if err := json.Unmarshal(nodesRec.Body.Bytes(), &nodesBody); err != nil {
		t.Fatalf("Unmarshal nodes body error = %v", err)
	}
	if nodesBody.ControllerLeaderID != 1 || nodesBody.Total != 2 {
		t.Fatalf("nodes summary = %+v, want leader 1 total 2", nodesBody)
	}
	if nodesBody.Items[1].NodeID != 2 || nodesBody.Items[1].Addr != "127.0.0.1:7012" || !nodesBody.Items[1].IsLocal {
		t.Fatalf("local node row = %+v, want node 2 at 127.0.0.1:7012", nodesBody.Items[1])
	}
	if nodesBody.Items[0].Actions.CanDrain {
		t.Fatalf("can_drain = true, want false while node operations are unmigrated")
	}
}

func TestManagerServerListsLocalNodeRuntimeSummary(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 2,
		snapshot: control.Snapshot{
			ControllerID: 1,
			Nodes: []control.Node{
				{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
				{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			},
		},
	}
	onlineRegistry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	activeRoute := online.OwnerRoute{UID: "u1", HashSlot: 1, OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 101, Listener: "tcp", ConnectedUnix: 100}
	pendingRoute := online.OwnerRoute{UID: "u2", HashSlot: 2, OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 2, SessionID: 102, Listener: "tcp", ConnectedUnix: 101}
	if err := onlineRegistry.RegisterPending(online.LocalSession{Route: activeRoute}); err != nil {
		t.Fatalf("RegisterPending(active) error = %v", err)
	}
	if err := onlineRegistry.MarkActive(activeRoute.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := onlineRegistry.RegisterPending(online.LocalSession{Route: pendingRoute}); err != nil {
		t.Fatalf("RegisterPending(pending) error = %v", err)
	}
	gatewayRuntime := &fakeGateway{
		calls: &[]string{},
		sessionSummary: gatewaycore.SessionSummary{
			GatewaySessions:      3,
			SessionsByListener:   map[string]int{"tcp": 2, "ws": 1},
			AcceptingNewSessions: false,
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{ListenAddr: ":0"},
	}, WithCluster(cluster), WithOnlineRegistry(onlineRegistry), WithGateway(gatewayRuntime))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/nodes", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("nodes status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Items []struct {
			NodeID  uint64 `json:"node_id"`
			Runtime struct {
				ActiveOnline         int            `json:"active_online"`
				ClosingOnline        int            `json:"closing_online"`
				TotalOnline          int            `json:"total_online"`
				GatewaySessions      int            `json:"gateway_sessions"`
				PendingActivations   int            `json:"pending_activations"`
				SessionsByListener   map[string]int `json:"sessions_by_listener"`
				AcceptingNewSessions bool           `json:"accepting_new_sessions"`
				Draining             bool           `json:"draining"`
				Unknown              bool           `json:"unknown"`
			} `json:"runtime"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal nodes body error = %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items len = %d, want 2: %s", len(body.Items), rec.Body.String())
	}
	runtime := body.Items[1].Runtime
	if body.Items[1].NodeID != 2 || runtime.Unknown || runtime.ActiveOnline != 1 ||
		runtime.ClosingOnline != 0 || runtime.TotalOnline != 2 || runtime.GatewaySessions != 3 ||
		runtime.PendingActivations != 1 ||
		runtime.SessionsByListener["tcp"] != 2 || runtime.SessionsByListener["ws"] != 1 ||
		runtime.AcceptingNewSessions || !runtime.Draining {
		t.Fatalf("local runtime = %+v on row %+v, want concrete runtime summary", runtime, body.Items[1])
	}
}

func TestManagementRuntimeSummaryReportsLocalControlRevision(t *testing.T) {
	app := &App{
		cluster: &fakeManagerCluster{
			nodeID:   2,
			snapshot: control.Snapshot{Revision: 42},
		},
	}
	reader := managementRuntimeSummaryReader{app: app, localNodeID: 2}

	got, err := reader.NodeRuntimeSummary(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeRuntimeSummary() error = %v", err)
	}
	if got.ControlRevision != 42 {
		t.Fatalf("runtime summary = %#v, want control revision 42", got)
	}
}

func TestManagementGatewayDrainWriterTogglesLocalGateway(t *testing.T) {
	gateway := &gatewayAdmissionStub{accepting: true}
	app := &App{gateway: gateway}
	writer := managementGatewayDrainWriter{app: app, localNodeID: 1}

	summary, err := writer.SetNodeDrainMode(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if gateway.AcceptingNewSessions() || !summary.Draining || summary.AcceptingNewSessions {
		t.Fatalf("gateway accepting=%v summary=%#v, want local drain", gateway.AcceptingNewSessions(), summary)
	}
}

func TestManagementGatewayDrainWriterResumesLocalGateway(t *testing.T) {
	gateway := &gatewayAdmissionStub{accepting: false}
	app := &App{gateway: gateway}
	writer := managementGatewayDrainWriter{app: app, localNodeID: 1}

	summary, err := writer.SetNodeDrainMode(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("SetNodeDrainMode(resume) error = %v", err)
	}
	if !gateway.AcceptingNewSessions() || summary.Draining || !summary.AcceptingNewSessions {
		t.Fatalf("gateway accepting=%v summary=%#v, want local resume", gateway.AcceptingNewSessions(), summary)
	}
}

func TestManagementGatewayDrainWriterFailsClosedWhenRemoteMissing(t *testing.T) {
	local := &gatewayAdmissionStub{accepting: true}
	writer := managementGatewayDrainWriter{app: &App{gateway: local}, localNodeID: 1}

	summary, err := writer.SetNodeDrainMode(context.Background(), 4, true)
	if !errors.Is(err, managementusecase.ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(remote missing) error = %v, want ErrNodeScaleInUnavailable", err)
	}
	if !summary.Unknown || summary.NodeID != 4 {
		t.Fatalf("summary = %#v, want unknown target node 4", summary)
	}
	if local.acceptingChanged {
		t.Fatalf("remote drain unexpectedly changed local gateway admission")
	}
}

func TestManagerConnectionRPCDrainUsesOwnerLocalGatewayPrimitive(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 4}
	gateway := &gatewayAdmissionStub{accepting: true}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	summary, err := accessnode.NewClient(cluster).SetManagerDrainMode(context.Background(), 4, true)
	if err != nil {
		t.Fatalf("SetManagerDrainMode() error = %v", err)
	}
	if !gateway.acceptingChanged || gateway.AcceptingNewSessions() {
		t.Fatalf("gateway accepting=%v changed=%v, want local drain", gateway.AcceptingNewSessions(), gateway.acceptingChanged)
	}
	if summary.NodeID != 4 || !summary.Draining || summary.AcceptingNewSessions {
		t.Fatalf("summary = %#v, want drained node 4", summary)
	}
}

func TestManagerConnectionRPCDrainRejectsNonLocalTarget(t *testing.T) {
	gateway := &gatewayAdmissionStub{accepting: true}
	service := managerConnectionRPCService{
		drain: managementGatewayDrainWriter{app: &App{gateway: gateway}, localNodeID: 4},
	}

	_, err := service.SetNodeDrainMode(context.Background(), managementusecase.SetNodeDrainModeRequest{NodeID: 5, Draining: true})
	if !errors.Is(err, managementusecase.ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(non-local) error = %v, want ErrNodeScaleInUnavailable", err)
	}
	if gateway.acceptingChanged {
		t.Fatalf("non-local drain unexpectedly changed gateway admission")
	}
}

func TestNewRegistersManagerLogRPCWhenClusterSupportsLogReads(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerLogRPCServiceID]; !ok {
		t.Fatalf("manager log rpc handler not registered")
	}
}

func TestNewRegistersManagerControllerRaftRPCWhenClusterSupportsOperations(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerControllerRaftRPCServiceID]; !ok {
		t.Fatalf("manager controller raft rpc handler not registered")
	}
}

func TestNewRegistersManagerSlotRaftRPCWhenClusterSupportsOperations(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerSlotRaftRPCServiceID]; !ok {
		t.Fatalf("manager slot raft rpc handler not registered")
	}
}

func TestNewRegistersManagerMessageRetentionRPCWhenClusterSupportsRetention(t *testing.T) {
	key := metadb.ConversationKey{ChannelID: "room-1", ChannelType: 2}
	cluster := &fakeManagerCluster{
		nodeID: 1,
		channelRuntimeMetas: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{key: {
			ChannelID: "room-1", ChannelType: 2,
			ChannelEpoch: 4, LeaderEpoch: 5, Leader: 1, LeaseUntilMS: 1713859200123,
			Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channelv2.StatusActive),
		}},
		conversationMessages: map[metadb.ConversationKey][]channelv2.Message{
			key: {{MessageID: 101, MessageSeq: 2, ChannelID: "room-1", ChannelType: 2}},
		},
	}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	client := accessnode.NewClient(cluster)
	got, err := client.AdvanceManagerMessageRetention(context.Background(), 1, managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 2,
	})
	if err != nil {
		t.Fatalf("AdvanceManagerMessageRetention() error = %v", err)
	}
	if got.Status != managementusecase.MessageRetentionStatusAdvanced || got.AdvancedThroughSeq != 2 || got.MinAvailableSeq != 3 {
		t.Fatalf("response = %+v, want advanced through 2 min 3", got)
	}
	if cluster.rpcNodeID != 1 || cluster.rpcServiceID != accessnode.ManagerMessageRetentionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 1 service %d", cluster.rpcNodeID, cluster.rpcServiceID, accessnode.ManagerMessageRetentionRPCServiceID)
	}
}

func TestManagerServerCompactsSlotRaftFromClusterOperator(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		slotRaftCompact: clusterv2.SlotRaftCompactionResult{
			NodeID:             1,
			SlotID:             9,
			AppliedIndex:       50,
			Compacted:          true,
			AfterSnapshotIndex: 50,
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.slot",
					Actions:  []string{"w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/slots/9/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if cluster.slotRaftCompactSlotID != 9 {
		t.Fatalf("compact slot id = %d, want 9", cluster.slotRaftCompactSlotID)
	}
	if !strings.Contains(rec.Body.String(), `"slot_id":9`) || !strings.Contains(rec.Body.String(), `"compacted":true`) {
		t.Fatalf("body = %s, want compacted slot 9 result", rec.Body.String())
	}
}

func TestManagerServerRequestsSlotLeaderTransferFromClusterControl(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Revision: 9,
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			},
			Slots: []control.SlotAssignment{{
				SlotID:          1,
				DesiredPeers:    []uint64{1, 2, 3},
				ConfigEpoch:     4,
				PreferredLeader: 1,
			}},
		},
		slotRaftStatus: clusterv2.SlotRaftStatus{
			NodeID:        1,
			SlotID:        1,
			LeaderID:      1,
			CurrentVoters: []uint64{1, 2, 3},
		},
		slotLeaderTransferResult: control.SlotLeaderTransferResult{
			Created: true,
			Task: &control.ReconcileTask{
				TaskID:      "slot-1-leader-transfer-4-r9",
				SlotID:      1,
				Kind:        control.TaskKindLeaderTransfer,
				Step:        control.TaskStepTransferLeader,
				SourceNode:  1,
				TargetNode:  2,
				TargetPeers: []uint64{1, 2, 3},
				ConfigEpoch: 4,
				Status:      control.TaskStatusPending,
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.slot",
					Actions:  []string{"w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/1/leader-transfer", strings.NewReader(`{"target_node":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if cluster.slotLeaderTransferRequest.SourceNode != 1 {
		t.Fatalf("source node = %d, want 1", cluster.slotLeaderTransferRequest.SourceNode)
	}
	if cluster.slotLeaderTransferRequest.TargetNode != 2 {
		t.Fatalf("target node = %d, want 2", cluster.slotLeaderTransferRequest.TargetNode)
	}
	if cluster.slotLeaderTransferRequest.StateRevision != 9 {
		t.Fatalf("state revision = %d, want 9", cluster.slotLeaderTransferRequest.StateRevision)
	}
}

func TestManagerServerStartsNodeOnboardingFromClusterControl(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Revision: 12,
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, Health: freshReadyAppNodeHealth(12)},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, Health: freshReadyAppNodeHealth(12)},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, Health: freshReadyAppNodeHealth(12)},
				{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, Health: freshReadyAppNodeHealth(12)},
			},
			Slots: []control.SlotAssignment{{
				SlotID:          1,
				DesiredPeers:    []uint64{1, 2, 3},
				ConfigEpoch:     7,
				PreferredLeader: 1,
			}},
		},
		slotReplicaMoveResult: control.SlotReplicaMoveResult{
			Created: true,
			Task: &control.ReconcileTask{
				TaskID:      "slot-1-replica-move-1-to-4-r12",
				SlotID:      1,
				Kind:        control.TaskKindSlotReplicaMove,
				Step:        control.TaskStepOpenLearner,
				SourceNode:  1,
				TargetNode:  4,
				TargetPeers: []uint64{4, 2, 3},
				ConfigEpoch: 7,
				Status:      control.TaskStatusPending,
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/start", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if cluster.slotReplicaMoveRequest.SlotID != 1 {
		t.Fatalf("slot id = %d, want 1", cluster.slotReplicaMoveRequest.SlotID)
	}
	if cluster.slotReplicaMoveRequest.SourceNode != 1 || cluster.slotReplicaMoveRequest.TargetNode != 4 {
		t.Fatalf("move request = %#v, want source 1 target 4", cluster.slotReplicaMoveRequest)
	}
	if cluster.slotReplicaMoveRequest.StateRevision != 12 {
		t.Fatalf("state revision = %d, want 12", cluster.slotReplicaMoveRequest.StateRevision)
	}
	if !reflect.DeepEqual(cluster.slotReplicaMoveRequest.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("target peers = %v, want [4 2 3]", cluster.slotReplicaMoveRequest.TargetPeers)
	}
}

func TestManagerServerJoinsNodeFromClusterControl(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		joinNodeResult: control.JoinNodeResult{
			Created:  true,
			Revision: 12,
			Node: control.Node{
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: control.NodeJoinStateJoining,
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"name":"node-4","addr":"10.0.0.4:11110","capacity_weight":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if cluster.joinNodeRequest.NodeID != 4 || cluster.joinNodeRequest.Name != "node-4" || cluster.joinNodeRequest.Addr != "10.0.0.4:11110" || cluster.joinNodeRequest.CapacityWeight != 2 {
		t.Fatalf("join request = %#v, want parsed lifecycle request", cluster.joinNodeRequest)
	}
}

func TestManagerServerMapsControllerLifecycleAvailabilityErrors(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID:      1,
		joinNodeErr: cv2.ErrNotLeader,
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:     true,
			JWTSecret:  "test-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"addr":"10.0.0.4:11110"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestManagerServerReadsControllerRaftStatusFromClusterOperator(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		controllerRaftStatus: clusterv2.ControllerRaftStatus{
			NodeID:        1,
			Role:          "leader",
			LeaderID:      1,
			Term:          4,
			FirstIndex:    2,
			LastIndex:     12,
			CommitIndex:   11,
			AppliedIndex:  10,
			SnapshotIndex: 6,
			SnapshotTerm:  3,
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.controller",
					Actions:  []string{"r"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/1/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("controller raft status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		NodeID        uint64 `json:"node_id"`
		AppliedIndex  uint64 `json:"applied_index"`
		FirstIndex    uint64 `json:"first_index"`
		SnapshotIndex uint64 `json:"snapshot_index"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.NodeID != 1 || body.AppliedIndex != 10 || body.FirstIndex != 2 || body.SnapshotIndex != 6 {
		t.Fatalf("body = %+v, want node 1 applied 10 first 2 snapshot 6", body)
	}
}

func TestApplicationLogReaderMapsLocalLogDTOs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(`{"level":"warn","ts":1000,"msg":"slow write","uid":"u1"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write app log: %v", err)
	}
	reader := newApplicationLogReader(1, dir)

	sources, err := reader.ApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 1})
	if err != nil {
		t.Fatalf("ApplicationLogSources() error = %v", err)
	}
	entries, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{
		NodeID: 1,
		Source: "app",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ApplicationLogEntries() error = %v", err)
	}

	if sources.NodeID != 1 || len(sources.Sources) == 0 || sources.Sources[0].Name != "app" || sources.Sources[0].File != "app.log" {
		t.Fatalf("sources = %#v, want app source without local path", sources)
	}
	if entries.NodeID != 1 || entries.Source != "app" || len(entries.Items) != 1 {
		t.Fatalf("entries page = %#v, want one app entry", entries)
	}
	entry := entries.Items[0]
	if entry.Level != "warn" || entry.Message != "slow write" || entry.Fields["uid"] != "u1" {
		t.Fatalf("entry = %#v, want mapped warn slow write with uid field", entry)
	}
	entry.Fields["uid"] = "changed"
	again, err := reader.ApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{
		NodeID: 1,
		Source: "app",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ApplicationLogEntries(second) error = %v", err)
	}
	if again.Items[0].Fields["uid"] != "u1" {
		t.Fatalf("second entry fields = %#v, want cloned fields unaffected by caller mutation", again.Items[0].Fields)
	}
}

func TestAppRegistersManagerAppLogRPC(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerAppLogRPCServiceID]; !ok {
		t.Fatalf("manager app log rpc handler not registered")
	}
}

func TestNewRegistersManagerChannelRPCWhenClusterSupportsChannelScans(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerChannelRPCServiceID]; !ok {
		t.Fatalf("manager channel rpc handler not registered")
	}
}

func TestDBInspectReaderQueriesDerivedNodeStorage(t *testing.T) {
	dataDir := t.TempDir()
	store, err := metadb.Open(filepath.Join(dataDir, "slotmeta"))
	if err != nil {
		t.Fatalf("open meta store: %v", err)
	}
	if err := store.ForHashSlot(metadb.HashSlot(routing.HashSlotForKey("u1", 16))).CreateUser(context.Background(), metadb.User{UID: "u1", Token: "t1"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close meta store: %v", err)
	}
	messageStore, err := messagedb.Open(filepath.Join(dataDir, "messages"))
	if err != nil {
		t.Fatalf("open message store: %v", err)
	}
	if err := messageStore.Close(); err != nil {
		t.Fatalf("close message store: %v", err)
	}

	reader := newDBInspectReader(dbInspectReaderOptions{
		NodeID:        1,
		DataDir:       dataDir,
		HashSlotCount: 16,
		Now:           func() time.Time { return time.Unix(100, 0) },
	})

	resp, err := reader.QueryDBInspect(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 1,
		Query:  "select uid, token from meta.user where uid='u1' limit 10",
	})
	if err != nil {
		t.Fatalf("QueryDBInspect() error = %v", err)
	}
	if resp.NodeID != 1 || !resp.GeneratedAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("response metadata = node:%d at:%s, want node 1 at 100", resp.NodeID, resp.GeneratedAt)
	}
	if len(resp.Rows) != 1 || resp.Rows[0]["uid"] != "u1" || resp.Rows[0]["token"] != "t1" {
		t.Fatalf("rows = %#v, want seeded user", resp.Rows)
	}
	if resp.Stats.ScanMode != "point-partition" || resp.Stats.ReturnedRows != 1 {
		t.Fatalf("stats = %#v, want point partition one row", resp.Stats)
	}
}

func TestDBInspectReaderRequiresDataDir(t *testing.T) {
	reader := newDBInspectReader(dbInspectReaderOptions{NodeID: 1, HashSlotCount: 16})
	_, err := reader.QueryDBInspect(context.Background(), managementusecase.DBInspectQueryRequest{
		NodeID: 1,
		Query:  "show tables",
	})
	if !errors.Is(err, managementusecase.ErrDBInspectUnavailable) {
		t.Fatalf("QueryDBInspect() err = %v, want ErrDBInspectUnavailable", err)
	}
}

func TestAppWiresManagerDBInspectRoute(t *testing.T) {
	dir := t.TempDir()
	store, err := metadb.Open(filepath.Join(dir, "slotmeta"))
	if err != nil {
		t.Fatalf("open meta store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close meta store: %v", err)
	}
	messageStore, err := messagedb.Open(filepath.Join(dir, "messages"))
	if err != nil {
		t.Fatalf("open message store: %v", err)
	}
	if err := messageStore.Close(); err != nil {
		t.Fatalf("close message store: %v", err)
	}
	cfg := Config{
		NodeID:  1,
		DataDir: dir,
		Cluster: clusterv2.Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:0",
			DataDir:    dir,
			Slots:      clusterv2.SlotConfig{HashSlotCount: 16},
		},
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username:    "admin",
				Password:    "secret",
				Permissions: []ManagerPermissionConfig{{Resource: "cluster.db", Actions: []string{"r"}}},
			}},
		},
	}
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := newTestApp(t, cfg, WithCluster(cluster))
	if err != nil {
		t.Fatalf("newTestApp() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginRec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginRec.Code, loginRec.Body.String())
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`))
	req.Header.Set("Authorization", "Bearer "+login.AccessToken)
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("db inspect status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func TestAppRegistersManagerDBInspectRPC(t *testing.T) {
	dir := t.TempDir()
	cluster := &fakeManagerCluster{nodeID: 1}
	cfg := Config{
		NodeID:  1,
		DataDir: dir,
		Cluster: clusterv2.Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:0",
			DataDir:    dir,
			Slots:      clusterv2.SlotConfig{HashSlotCount: 16},
		},
		Manager: ManagerConfig{ListenAddr: ":0"},
	}
	if _, err := newTestApp(t, cfg, WithCluster(cluster)); err != nil {
		t.Fatalf("newTestApp() error = %v", err)
	}
	if _, ok := cluster.registeredHandlers[accessnode.ManagerDBInspectRPCServiceID]; !ok {
		t.Fatal("manager db inspect rpc handler not registered")
	}
}

func TestManagerServerListsSlotsFromClusterSnapshot(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 2,
		snapshot: control.Snapshot{
			Slots: []control.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 3, PreferredLeader: 1},
				{SlotID: 2, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 4, PreferredLeader: 2},
			},
			HashSlots: control.HashSlotTable{
				Revision: 5,
				Count:    4,
				Ranges: []control.HashSlotRange{
					{From: 0, To: 1, SlotID: 1},
					{From: 2, To: 3, SlotID: 2},
				},
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.slot",
					Actions:  []string{"r"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	slotsRec := httptest.NewRecorder()
	slotsReq := httptest.NewRequest(http.MethodGet, "/manager/slots?node_id=2", nil)
	slotsReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(slotsRec, slotsReq)
	if slotsRec.Code != http.StatusOK {
		t.Fatalf("slots status = %d, want %d; body=%s", slotsRec.Code, http.StatusOK, slotsRec.Body.String())
	}
	var slotsBody struct {
		Total int `json:"total"`
		Items []struct {
			SlotID    uint32 `json:"slot_id"`
			HashSlots struct {
				Count int      `json:"count"`
				Items []uint16 `json:"items"`
			} `json:"hash_slots"`
			State struct {
				Quorum      string `json:"quorum"`
				Sync        string `json:"sync"`
				LeaderMatch bool   `json:"leader_match"`
			} `json:"state"`
			Runtime struct {
				PreferredLeaderID uint64 `json:"preferred_leader_id"`
			} `json:"runtime"`
		} `json:"items"`
	}
	if err := json.Unmarshal(slotsRec.Body.Bytes(), &slotsBody); err != nil {
		t.Fatalf("Unmarshal slots body error = %v", err)
	}
	if slotsBody.Total != 1 || len(slotsBody.Items) != 1 {
		t.Fatalf("slots summary = %+v, want one node-filtered slot", slotsBody)
	}
	row := slotsBody.Items[0]
	if row.SlotID != 2 || row.HashSlots.Count != 2 || len(row.HashSlots.Items) != 2 || row.HashSlots.Items[0] != 2 || row.HashSlots.Items[1] != 3 {
		t.Fatalf("slot row hash slots = %+v, want slot 2 owning hash slots 2 and 3", row)
	}
	if row.State.Quorum != "ready" || row.State.Sync != "matched" || !row.State.LeaderMatch || row.Runtime.PreferredLeaderID != 2 {
		t.Fatalf("slot row state/runtime = %+v/%+v, want ready matched preferred leader 2", row.State, row.Runtime)
	}
}

func TestManagerServerListsBusinessChannelsFromClusterMetadata(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
		channelPages: map[uint32][]metadb.Channel{
			1: {{ChannelID: "g1", ChannelType: 2, Ban: 1, SubscriberMutationVersion: 8}},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.channel",
					Actions:  []string{"r"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	channelsRec := httptest.NewRecorder()
	channelsReq := httptest.NewRequest(http.MethodGet, "/manager/channels?type=2&keyword=g&limit=10", nil)
	channelsReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(channelsRec, channelsReq)
	if channelsRec.Code != http.StatusOK {
		t.Fatalf("channels status = %d, want %d; body=%s", channelsRec.Code, http.StatusOK, channelsRec.Body.String())
	}
	var channelsBody struct {
		Items []struct {
			ChannelID                 string `json:"channel_id"`
			ChannelType               int64  `json:"channel_type"`
			SlotID                    uint32 `json:"slot_id"`
			Ban                       bool   `json:"ban"`
			SubscriberMutationVersion uint64 `json:"subscriber_mutation_version"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(channelsRec.Body.Bytes(), &channelsBody); err != nil {
		t.Fatalf("Unmarshal channels body error = %v", err)
	}
	if len(channelsBody.Items) != 1 || channelsBody.Items[0].ChannelID != "g1" || channelsBody.Items[0].ChannelType != 2 || channelsBody.Items[0].SlotID != 1 || !channelsBody.Items[0].Ban || channelsBody.Items[0].SubscriberMutationVersion != 8 || channelsBody.HasMore {
		t.Fatalf("channels body = %+v, want one business channel row", channelsBody)
	}
}

func TestManagerServerListsRecentConversationsFromConversationUsecase(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
		conversationPages: map[string][]metadb.ConversationState{
			"u1": {{
				UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2,
				ReadSeq: 4, ActiveAt: 200000000000, UpdatedAt: 200000000000,
			}},
		},
		conversationMessages: map[metadb.ConversationKey][]channelv2.Message{
			{ChannelID: "g1", ChannelType: 2}: {{
				MessageID: 99, MessageSeq: 7, ClientMsgNo: "c7",
				ChannelID: "g1", ChannelType: 2, FromUID: "u2",
				ServerTimestampMS: 200000, Payload: []byte("hello"),
			}},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.channel",
					Actions:  []string{"r"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1&limit=10&msg_count=1", nil)
	req.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("conversations status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"channel_id":"g1"`) || !strings.Contains(rec.Body.String(), `"unread":3`) || !strings.Contains(rec.Body.String(), `"payload":"aGVsbG8="`) {
		t.Fatalf("conversations body = %s, want g1 unread recent payload", rec.Body.String())
	}
}

func TestManagerServerListsMessagesFromClusterCommittedLog(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
		conversationMessages: map[metadb.ConversationKey][]channelv2.Message{
			{ChannelID: "room-1", ChannelType: 2}: {
				{MessageID: 100, MessageSeq: 9, ClientMsgNo: "c-100", ChannelID: "room-1", ChannelType: 2, FromUID: "u2", ServerTimestampMS: 1713859100000, Payload: []byte("older")},
				{MessageID: 101, MessageSeq: 10, ClientMsgNo: "c-101", ChannelID: "room-1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 1713859200123, Payload: []byte("hello")},
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0", AuthOn: true, JWTSecret: "manager-secret", JWTIssuer: "wukongim-manager", JWTExpire: time.Hour,
			Users: []ManagerUserConfig{{Username: "admin", Password: "secret", Permissions: []ManagerPermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}}}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?channel_id=room-1&channel_type=2&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message_id":101`) || !strings.Contains(rec.Body.String(), `"payload":"aGVsbG8="`) || !strings.Contains(rec.Body.String(), `"has_more":true`) {
		t.Fatalf("messages body = %s, want newest message with next page", rec.Body.String())
	}
}

func TestManagerServerAdvancesMessageRetentionThroughClusterMeta(t *testing.T) {
	key := metadb.ConversationKey{ChannelID: "room-1", ChannelType: 2}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "room-1", ChannelType: 2,
		ChannelEpoch: 4, LeaderEpoch: 5, Leader: 1, LeaseUntilMS: 1713859200123,
		Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channelv2.StatusActive),
	}
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
		channelRuntimeMetas: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{key: meta},
		conversationMessages: map[metadb.ConversationKey][]channelv2.Message{
			key: {
				{MessageID: 100, MessageSeq: 1, ChannelID: "room-1", ChannelType: 2},
				{MessageID: 101, MessageSeq: 2, ChannelID: "room-1", ChannelType: 2},
				{MessageID: 102, MessageSeq: 3, ChannelID: "room-1", ChannelType: 2},
			},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0", AuthOn: true, JWTSecret: "manager-secret", JWTIssuer: "wukongim-manager", JWTExpire: time.Hour,
			Users: []ManagerUserConfig{{Username: "admin", Password: "secret", Permissions: []ManagerPermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}}}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retention status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
		MinAvailableSeq    uint64 `json:"min_available_seq"`
		Status             string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal retention body error = %v", err)
	}
	if body.Status != "advanced" || body.AdvancedThroughSeq != 2 || body.MinAvailableSeq != 3 {
		t.Fatalf("retention body = %+v, want advanced through 2 min 3", body)
	}
	if cluster.retentionAdvance.RetentionThroughSeq != 2 ||
		cluster.retentionAdvance.ExpectedChannelEpoch != meta.ChannelEpoch ||
		cluster.retentionAdvance.ExpectedLeaderEpoch != meta.LeaderEpoch ||
		cluster.retentionAdvance.ExpectedLeader != meta.Leader ||
		cluster.retentionAdvance.ExpectedLeaseUntilMS != meta.LeaseUntilMS {
		t.Fatalf("retention advance = %+v, want fenced advance from meta %+v", cluster.retentionAdvance, meta)
	}
}

func TestManagerServerListsConnectionsFromLocalOnlineRegistry(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0", AuthOn: true, JWTSecret: "manager-secret", JWTIssuer: "wukongim-manager", JWTExpire: time.Hour,
			Users: []ManagerUserConfig{{Username: "admin", Password: "secret", Permissions: []ManagerPermissionConfig{{Resource: "cluster.connection", Actions: []string{"r"}}}}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.online.RegisterPending(online.LocalSession{Route: online.OwnerRoute{
		UID: "u1", HashSlot: routing.HashSlotForKey("u1", 4), OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11,
		SessionID: 101, DeviceID: "d1", DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster),
		Listener: "tcp", ConnectedUnix: 1713859200,
	}}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := app.online.MarkActive(101); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections", nil)
	req.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("connections status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"session_id":101`) || !strings.Contains(rec.Body.String(), `"device_flag":"app"`) || !strings.Contains(rec.Body.String(), `"state":"active"`) {
		t.Fatalf("connections body = %s, want active local connection", rec.Body.String())
	}
}

func TestManagerServerListsUsersAndMutatesSystemUsers(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
			HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
		},
		userPages: map[uint32][]metadb.User{
			1: {{UID: "u1"}},
		},
		devices: map[fakeManagerDeviceKey]metadb.Device{
			{uid: "u1", deviceFlag: int64(frame.APP)}: {UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelMaster)},
		},
	}
	app, err := newTestApp(t, Config{
		Manager: ManagerConfig{
			ListenAddr: ":0",
			AuthOn:     true,
			JWTSecret:  "manager-secret",
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.user",
					Actions:  []string{"r", "w"},
				}},
			}},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("Unmarshal login body error = %v", err)
	}

	usersRec := httptest.NewRecorder()
	usersReq := httptest.NewRequest(http.MethodGet, "/manager/users?limit=10", nil)
	usersReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(usersRec, usersReq)
	if usersRec.Code != http.StatusOK {
		t.Fatalf("users status = %d, want %d; body=%s", usersRec.Code, http.StatusOK, usersRec.Body.String())
	}
	var usersBody struct {
		Items []struct {
			UID           string `json:"uid"`
			SlotID        uint32 `json:"slot_id"`
			DeviceCount   int    `json:"device_count"`
			TokenSetCount int    `json:"token_set_count"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(usersRec.Body.Bytes(), &usersBody); err != nil {
		t.Fatalf("Unmarshal users body error = %v", err)
	}
	if len(usersBody.Items) != 1 || usersBody.Items[0].UID != "u1" || usersBody.Items[0].SlotID != 1 || usersBody.Items[0].DeviceCount != 1 || usersBody.Items[0].TokenSetCount != 1 || usersBody.HasMore {
		t.Fatalf("users body = %+v, want one user row", usersBody)
	}

	addRec := httptest.NewRecorder()
	addReq := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", strings.NewReader(`{"uids":["sys-a"]}`))
	addReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	addReq.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("system add status = %d, want %d; body=%s", addRec.Code, http.StatusOK, addRec.Body.String())
	}

	systemRec := httptest.NewRecorder()
	systemReq := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)
	systemReq.Header.Set("Authorization", "Bearer "+loginBody.AccessToken)
	srv.Engine().ServeHTTP(systemRec, systemReq)
	if systemRec.Code != http.StatusOK {
		t.Fatalf("system list status = %d, want %d; body=%s", systemRec.Code, http.StatusOK, systemRec.Body.String())
	}
	var systemBody struct {
		Items []struct {
			UID string `json:"uid"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(systemRec.Body.Bytes(), &systemBody); err != nil {
		t.Fatalf("Unmarshal system body error = %v", err)
	}
	if systemBody.Total != 1 || len(systemBody.Items) != 1 || systemBody.Items[0].UID != "sys-a" {
		t.Fatalf("system body = %+v, want sys-a", systemBody)
	}
}

func TestStartStopOrderIncludesPrometheusBetweenAPIAndGateway(t *testing.T) {
	calls := make([]string, 0, 8)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	prometheus := &recordingWorkerRuntime{calls: &calls, name: "prometheus"}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.prometheus = prometheus

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := "cluster.start,api.start,prometheus.start,gateway.start,gateway.stop,prometheus.stop,api.stop,cluster.stop"
	if got := joinCalls(calls); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestStartStopOrderIncludesTopBeforeAPI(t *testing.T) {
	calls := make([]string, 0, 8)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	top := &recordingWorkerRuntime{calls: &calls, name: "top"}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.top = top

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := "cluster.start,top.start,api.start,gateway.start,gateway.stop,api.stop,top.stop,cluster.stop"
	if got := joinCalls(calls); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestGatewayStartFailureStopsPrometheusBeforeAPI(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 7)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	prometheus := &recordingWorkerRuntime{calls: &calls, name: "prometheus"}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	app.prometheus = prometheus

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}

	want := "cluster.start,api.start,prometheus.start,gateway.start,prometheus.stop,api.stop,cluster.stop"
	if got := joinCalls(calls); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestDefaultPresenceConfigUsesTouchDefaults(t *testing.T) {
	cfg := defaultPresenceConfig(PresenceConfig{})

	if cfg.ActivationTimeout != 3*time.Second {
		t.Fatalf("ActivationTimeout = %v, want 3s", cfg.ActivationTimeout)
	}
	if cfg.TouchFlushInterval != time.Second {
		t.Fatalf("TouchFlushInterval = %v, want 1s", cfg.TouchFlushInterval)
	}
	if cfg.TouchBatchSize != 512 {
		t.Fatalf("TouchBatchSize = %d, want 512", cfg.TouchBatchSize)
	}
	if cfg.RouteTTL != 90*time.Second {
		t.Fatalf("RouteTTL = %v, want 90s", cfg.RouteTTL)
	}

	negative := defaultPresenceConfig(PresenceConfig{
		ActivationTimeout:  -time.Second,
		TouchFlushInterval: -time.Second,
		TouchBatchSize:     -1,
		RouteTTL:           -time.Second,
	})
	if negative.ActivationTimeout != -time.Second ||
		negative.TouchFlushInterval != -time.Second ||
		negative.TouchBatchSize != -1 ||
		negative.RouteTTL != -time.Second {
		t.Fatalf("negative presence values were overwritten: %#v", negative)
	}
}

func TestValidatePresenceConfigRejectsInvalidTouchValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  PresenceConfig
	}{
		{name: "activation timeout", cfg: PresenceConfig{ActivationTimeout: -time.Nanosecond}},
		{name: "touch flush interval", cfg: PresenceConfig{TouchFlushInterval: -time.Nanosecond}},
		{name: "touch batch size", cfg: PresenceConfig{TouchBatchSize: -1}},
		{name: "route ttl", cfg: PresenceConfig{RouteTTL: -time.Nanosecond}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePresenceConfig(tt.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validatePresenceConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestDefaultChannelConfigUsesLargeGroupThreshold(t *testing.T) {
	cfg := defaultChannelConfig(ChannelConfig{})
	if cfg.LargeGroupSubscriberThreshold != 500 {
		t.Fatalf("LargeGroupSubscriberThreshold = %d, want 500", cfg.LargeGroupSubscriberThreshold)
	}
	if err := validateChannelConfig(cfg); err != nil {
		t.Fatalf("validateChannelConfig(default) error = %v", err)
	}
	if err := validateChannelConfig(ChannelConfig{LargeGroupSubscriberThreshold: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validateChannelConfig(negative) error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestDefaultChannelAppendConfigUsesRuntimeDefaults(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(6)
	t.Cleanup(func() { runtime.GOMAXPROCS(oldProcs) })

	cfg := defaultChannelAppendConfig(ChannelAppendConfig{})

	if cfg.AuthorityShardCount != 6 {
		t.Fatalf("AuthorityShardCount = %d, want 6", cfg.AuthorityShardCount)
	}
	if cfg.AdvancePoolSize != 500 {
		t.Fatalf("AdvancePoolSize = %d, want 500", cfg.AdvancePoolSize)
	}
	if cfg.EffectPoolSize != 2000 {
		t.Fatalf("EffectPoolSize = %d, want 2000", cfg.EffectPoolSize)
	}
	if cfg.RecipientAuthorityDispatchConcurrency != 100 {
		t.Fatalf("RecipientAuthorityDispatchConcurrency = %d, want 100", cfg.RecipientAuthorityDispatchConcurrency)
	}

	negative := defaultChannelAppendConfig(ChannelAppendConfig{
		AuthorityShardCount:                   -5,
		AdvancePoolSize:                       -6,
		EffectPoolSize:                        -7,
		RecipientAuthorityDispatchConcurrency: -8,
	})
	if negative.AuthorityShardCount != -5 ||
		negative.AdvancePoolSize != -6 ||
		negative.EffectPoolSize != -7 ||
		negative.RecipientAuthorityDispatchConcurrency != -8 {
		t.Fatalf("negative channel append values were overwritten: %#v", negative)
	}
}

func TestDefaultDeliveryConfigKeepsDisabledAndUsesRuntimeDefaults(t *testing.T) {
	cfg := defaultDeliveryConfig(DeliveryConfig{})

	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false by default")
	}
	if cfg.FanoutPageSize != 512 {
		t.Fatalf("FanoutPageSize = %d, want 512", cfg.FanoutPageSize)
	}
	if cfg.PushBatchSize != 512 {
		t.Fatalf("PushBatchSize = %d, want 512", cfg.PushBatchSize)
	}
	if cfg.PendingAckTTL != 30*time.Second {
		t.Fatalf("PendingAckTTL = %v, want 30s", cfg.PendingAckTTL)
	}
	if cfg.PendingAckMaxPerSession != 1024 {
		t.Fatalf("PendingAckMaxPerSession = %d, want 1024", cfg.PendingAckMaxPerSession)
	}
	if cfg.EventQueueSize != 1024 {
		t.Fatalf("EventQueueSize = %d, want 1024", cfg.EventQueueSize)
	}

	negative := defaultDeliveryConfig(DeliveryConfig{
		Enabled:                 true,
		FanoutPageSize:          -1,
		PushBatchSize:           -2,
		PendingAckTTL:           -time.Second,
		PendingAckMaxPerSession: -3,
		EventQueueSize:          -4,
	})
	if !negative.Enabled ||
		negative.FanoutPageSize != -1 || negative.PushBatchSize != -2 ||
		negative.PendingAckTTL != -time.Second || negative.PendingAckMaxPerSession != -3 ||
		negative.EventQueueSize != -4 {
		t.Fatalf("negative delivery values were overwritten: %#v", negative)
	}
}

func TestValidateChannelAppendConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  ChannelAppendConfig
	}{
		{name: "authority shard count", cfg: ChannelAppendConfig{AuthorityShardCount: -1}},
		{name: "advance pool size", cfg: ChannelAppendConfig{AdvancePoolSize: -1}},
		{name: "effect pool size", cfg: ChannelAppendConfig{EffectPoolSize: -1}},
		{name: "recipient authority dispatch concurrency", cfg: ChannelAppendConfig{RecipientAuthorityDispatchConcurrency: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateChannelAppendConfig(tt.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateChannelAppendConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestValidateDeliveryConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  DeliveryConfig
	}{
		{name: "fanout page size", cfg: DeliveryConfig{FanoutPageSize: -1}},
		{name: "push batch size", cfg: DeliveryConfig{PushBatchSize: -1}},
		{name: "pending ack ttl", cfg: DeliveryConfig{PendingAckTTL: -time.Nanosecond}},
		{name: "pending ack max per session", cfg: DeliveryConfig{PendingAckMaxPerSession: -1}},
		{name: "event queue size", cfg: DeliveryConfig{EventQueueSize: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDeliveryConfig(tt.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateDeliveryConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestDefaultConversationConfigUsesRuntimeDefaults(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})

	if cfg.MaxLastMessageConcurrency != 32 {
		t.Fatalf("MaxLastMessageConcurrency = %d, want 32", cfg.MaxLastMessageConcurrency)
	}

	negative := defaultConversationConfig(ConversationConfig{
		MaxLastMessageConcurrency: -2,
	})
	if negative.MaxLastMessageConcurrency != -2 {
		t.Fatalf("negative conversation values were overwritten: %#v", negative)
	}
}

func TestDefaultConversationAuthorityConfig(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})
	if cfg.AuthorityCacheMaxRowsPerUID != 4096 ||
		cfg.AuthorityCacheMaxRows != 100000 ||
		cfg.AuthorityListDBWindowMax != 1000 ||
		cfg.AuthorityHandoffTimeout != 3*time.Second ||
		cfg.AuthorityActiveCooldown != 2*time.Hour ||
		cfg.AuthorityFlushInterval != time.Second ||
		cfg.AuthorityFlushTimeout != 5*time.Second ||
		cfg.AuthorityFlushBatchRows != 128 ||
		cfg.AuthorityAdmitBatchRows != 512 ||
		cfg.AuthorityAdmitConcurrency != 16 {
		t.Fatalf("conversation authority defaults = %#v", cfg)
	}
}

func TestValidateConversationConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConversationConfig)
	}{
		{name: "last message concurrency", mutate: func(cfg *ConversationConfig) { cfg.MaxLastMessageConcurrency = -1 }},
		{name: "authority cache max rows per uid negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRowsPerUID = -1 }},
		{name: "authority cache max rows per uid zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRowsPerUID = 0 }},
		{name: "authority cache max rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRows = -1 }},
		{name: "authority cache max rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRows = 0 }},
		{name: "authority list db window max negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityListDBWindowMax = -1 }},
		{name: "authority list db window max zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityListDBWindowMax = 0 }},
		{name: "authority handoff timeout negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityHandoffTimeout = -time.Nanosecond }},
		{name: "authority handoff timeout zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityHandoffTimeout = 0 }},
		{name: "authority active cooldown negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityActiveCooldown = -time.Nanosecond }},
		{name: "authority active cooldown zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityActiveCooldown = 0 }},
		{name: "authority flush interval negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushInterval = -time.Nanosecond }},
		{name: "authority flush interval zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushInterval = 0 }},
		{name: "authority flush timeout negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushTimeout = -time.Nanosecond }},
		{name: "authority flush timeout zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushTimeout = 0 }},
		{name: "authority flush batch rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushBatchRows = -1 }},
		{name: "authority flush batch rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushBatchRows = 0 }},
		{name: "authority admit batch rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitBatchRows = -1 }},
		{name: "authority admit batch rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitBatchRows = 0 }},
		{name: "authority admit concurrency negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitConcurrency = -1 }},
		{name: "authority admit concurrency zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitConcurrency = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConversationConfig(ConversationConfig{})
			tt.mutate(&cfg)
			if err := validateConversationConfig(cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateConversationConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestNewBuildsRootLogger(t *testing.T) {
	calls := make([]string, 0, 2)
	cfg := Config{Log: LogConfig{Dir: t.TempDir(), Console: false, Format: "json"}}
	cfg.Log.SetExplicitFlags(false, true)
	app, err := New(
		cfg,
		WithCluster(&fakeCluster{calls: &calls}),
		WithGateway(&fakeGateway{calls: &calls}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(app.restoreDiagnosticsSink)
	if app.logger == nil {
		t.Fatal("logger was not wired")
	}
	if app.logger.Named("internalv2") == nil {
		t.Fatal("named logger is nil")
	}
}

func TestNewWiresTopAPIWithoutMetrics(t *testing.T) {
	calls := make([]string, 0, 1)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
		routes: map[uint16]clusterv2.Route{
			0: {Leader: 1, Peers: []uint64{1}},
		},
	}
	app, err := newTestApp(t, Config{
		NodeID: 1,
		API:    APIConfig{ListenAddr: "127.0.0.1:0"},
		Top: TopConfig{
			APIEnabled:      true,
			CollectInterval: time.Second,
			HistoryWindow:   time.Minute,
		},
		Observability: ObservabilityConfig{MetricsEnabled: false},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.metrics != nil {
		t.Fatal("metrics were wired when MetricsEnabled is false")
	}
	if app.top == nil {
		t.Fatal("top collector was not wired")
	}
	if app.topProvider == nil {
		t.Fatal("top provider was not wired")
	}
	if _, ok := app.api.(*accessapi.Server); !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}
}

func TestConfigureObservabilityWiresTopObserversWhenMetricsDisabled(t *testing.T) {
	app := &App{cfg: Config{
		Top: TopConfig{
			APIEnabled:      true,
			CollectInterval: time.Second,
			HistoryWindow:   time.Minute,
		},
		Observability: ObservabilityConfig{MetricsEnabled: false},
	}}
	clusterCfg := clusterv2.Config{NodeID: 4}

	app.configureObservability(&clusterCfg)

	if app.metrics != nil {
		t.Fatal("metrics were wired when MetricsEnabled is false")
	}
	if app.topProvider == nil {
		t.Fatal("top provider was not wired")
	}
	if clusterCfg.Channel.Observer == nil {
		t.Fatal("channel top observer was not wired")
	}
	if clusterCfg.Storage.CommitObserver == nil {
		t.Fatal("storage top observer was not wired")
	}
	if clusterCfg.Slots.Observer == nil {
		t.Fatal("slot top observer was not wired")
	}
	if clusterCfg.Control.RaftObserver == nil {
		t.Fatal("controller raft top observer was not wired")
	}
	if clusterCfg.Transport.Observer == nil {
		t.Fatal("transport top observer was not wired")
	}
	if app.deliveryObserver() == nil {
		t.Fatal("delivery top observer was not wired")
	}
}

func TestConfigureObservabilitySamplesResourcesForMetricsWithoutTopProvider(t *testing.T) {
	app := &App{cfg: Config{
		Observability: ObservabilityConfig{MetricsEnabled: true},
	}}
	clusterCfg := clusterv2.Config{NodeID: 5}

	app.configureObservability(&clusterCfg)

	if app.metrics == nil {
		t.Fatal("metrics were not wired")
	}
	collector, ok := app.top.(*topCollector)
	if !ok {
		t.Fatalf("top runtime = %T, want hidden resource collector", app.top)
	}
	if app.topProvider != nil {
		t.Fatalf("top provider = %T, want nil when Top.APIEnabled=false", app.topProvider)
	}
	if collector.options.ResourceMetrics == nil {
		t.Fatal("resource metrics were not attached to hidden collector")
	}
	if collector.options.StorageMetrics == nil {
		t.Fatal("storage metrics were not attached to hidden collector")
	}
	if collector.options.StorageMetricsSnapshot == nil {
		t.Fatal("storage metrics snapshot callback was not attached to hidden collector")
	}
}

func TestStopSyncsLogger(t *testing.T) {
	calls := make([]string, 0, 4)
	logger := &recordingAppLogger{}
	app, err := New(
		Config{},
		WithCluster(&fakeCluster{calls: &calls}),
		WithGateway(&fakeGateway{calls: &calls}),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if logger.syncCalls != 1 {
		t.Fatalf("Sync calls = %d, want 1", logger.syncCalls)
	}
}

func TestNewWiresDeliveryWhenEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{Enabled: true},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if app.Delivery() == nil {
		t.Fatal("delivery usecase was not wired")
	}
	if app.deliveryManager == nil {
		t.Fatal("delivery manager was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryPushRPCServiceID]; !ok {
		t.Fatalf("delivery push rpc service was not registered")
	}
	if app.deliveryWorker == nil {
		t.Fatal("delivery worker was not wired")
	}
	if app.channelAppendDeliveryWorker == nil {
		t.Fatal("channelappend recipient delivery worker was not wired")
	}
	if app.deliveryManager == nil || app.deliveryManager.PendingAckCount() != 0 {
		t.Fatal("delivery manager was not initialized for async runtime")
	}
	group, ok := app.deliveryWorker.(deliveryWorkerGroup)
	if !ok {
		t.Fatalf("delivery worker = %T, want deliveryWorkerGroup", app.deliveryWorker)
	}
	if len(group) != 3 {
		t.Fatalf("delivery worker count = %d, want recipient worker, retry scheduler, and manager", len(group))
	}
	if group[0] != app.deliveryRetry {
		t.Fatalf("delivery worker[0] = %T, want retry scheduler", group[0])
	}
	if group[1] != app.deliveryManager {
		t.Fatalf("delivery worker[1] = %T, want manager", group[1])
	}
	if _, ok := group[2].(*channelappend.RecipientDeliveryWorker); !ok {
		t.Fatalf("delivery worker[2] = %T, want recipient delivery worker", group[2])
	}
	if group[2] != app.channelAppendDeliveryWorker {
		t.Fatalf("delivery worker[2] = %T, want app channelappend recipient delivery worker", group[2])
	}
	if app.deliveryRetry == nil {
		t.Fatal("delivery retry scheduler was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryFanoutRPCServiceID]; !ok {
		t.Fatalf("delivery fanout rpc service was not registered")
	}
}

func TestNewWiresChannelMembershipProjection(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{
		snapshot: readyFakeClusterSnapshot(1, 16),
	}
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.channels == nil {
		t.Fatal("channel usecase was not wired")
	}

	if err := app.channels.AddSubscribers(context.Background(), channelusecase.SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{"u1", "u2"},
	}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if len(cluster.membershipUpserts) != 1 {
		t.Fatalf("membership upserts = %#v, want one call", cluster.membershipUpserts)
	}
	got := cluster.membershipUpserts[0]
	if got.channelID != "g1" || got.channelType != int64(frame.ChannelTypeGroup) || !reflect.DeepEqual(got.uids, []string{"u1", "u2"}) {
		t.Fatalf("membership upsert = %#v, want g1 group u1/u2", got)
	}
}

func TestNewWiresMessageAppendMetricsWhenDeliveryDisabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:       clusterv2.Config{NodeID: 3},
			Observability: ObservabilityConfig{MetricsEnabled: true},
			Delivery:      DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.messages == nil {
		t.Fatal("message usecase was not wired")
	}
	if app.channelAppends == nil || app.channelAppendRouter == nil {
		t.Fatalf("channel append runtime = (%T, %T), want group and router", app.channelAppends, app.channelAppendRouter)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room-metrics",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	families, err := app.metrics.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "wukongim_message_append_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["path"] == "channelplane" && labels["result"] == "ok" && metric.GetCounter().GetValue() == 1 {
				return
			}
		}
	}
	t.Fatal("message append metric for successful channelplane append was not observed")
}

func TestNewWiresTopMessageAppendWhenMetricsDisabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
			Top: TopConfig{
				APIEnabled:      true,
				CollectInterval: time.Second,
				HistoryWindow:   time.Minute,
			},
			Observability: ObservabilityConfig{MetricsEnabled: false},
			Delivery:      DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	collector, ok := app.topProvider.(*topCollector)
	if !ok {
		t.Fatalf("top provider = %T, want *topCollector", app.topProvider)
	}
	if app.channelAppends == nil || app.channelAppendRouter == nil {
		t.Fatalf("channel append runtime = (%T, %T), want group and router", app.channelAppends, app.channelAppendRouter)
	}
	startTestApp(t, app)

	collector.recordSampleAt(time.Now().Add(-time.Second))
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room-top",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}
	collector.recordSampleAt(time.Now())

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 2 * time.Second,
		View:   accessapi.TopViewTraffic,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Traffic == nil {
		t.Fatal("traffic snapshot is nil")
	}
	if snapshot.Traffic.AppendPerSec <= 0 {
		t.Fatalf("append rate = %f, want > 0", snapshot.Traffic.AppendPerSec)
	}
	if snapshot.Traffic.AppendP50MS <= 0 {
		t.Fatalf("append p50 = %f, want > 0", snapshot.Traffic.AppendP50MS)
	}
}

func TestNewWiresChannelAppendCommitEffectsWhenDeliveryDisabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.messages == nil {
		t.Fatal("message usecase was not wired")
	}
	if app.channelAppends == nil || app.channelAppendRouter == nil {
		t.Fatalf("channel append runtime = (%T, %T), want group and router", app.channelAppends, app.channelAppendRouter)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-conversation-1",
		Payload:     []byte("conversation payload"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	cluster.mu.Lock()
	if len(cluster.conversationStateBatches) != 0 {
		t.Fatalf("conversation state batches = %#v, want no synchronous DB write", cluster.conversationStateBatches)
	}
	cluster.mu.Unlock()
	if app.conversationAuthority == nil {
		t.Fatal("local conversation authority was not wired")
	}
	if app.conversationAuthorityClient == nil {
		t.Fatal("conversation authority client was not reused by app wiring")
	}
	if _, ok := cluster.registeredHandlers[accessnode.ConversationAuthorityRPCServiceID]; !ok {
		t.Fatalf("conversation authority rpc service was not registered")
	}
	if _, ok := cluster.registeredHandlers[accessnode.ChannelAppendRPCServiceID]; !ok {
		t.Fatalf("channel append rpc service was not registered")
	}

	for _, uid := range []string{"u1", "u2"} {
		requireConversationEventually(t, app, uid, channelID, frame.ChannelTypePerson)
	}
}

func TestNewWiresChannelAppendIdempotencyStore(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.idempotencyOK = true
	cluster.idempotencyHit = channelstore.IdempotencyHit{
		Message:     channelv2.Message{MessageID: 42, MessageSeq: 7},
		PayloadHash: appTestPayloadHash([]byte("payload")),
	}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 2,
		ClientMsgNo: "client-1",
		Payload:     []byte("payload"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.MessageID != 42 || result.MessageSeq != 7 || result.Reason != message.ReasonSuccess {
		t.Fatalf("send result = %#v, want idempotent success", result)
	}
	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.idempotencyLookups != 1 {
		t.Fatalf("idempotency lookups = %d, want 1", cluster.idempotencyLookups)
	}
	if cluster.appendSeq != 0 {
		t.Fatalf("append seq = %d, want idempotency hit to bypass append", cluster.appendSeq)
	}
}

func appTestPayloadHash(payload []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	hash := uint64(offset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= prime
	}
	return hash
}

func TestNewWiresConversationMutationsWhenAuthorityEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.messages = map[metadb.ConversationKey][]channelv2.Message{
		{ChannelID: "g1", ChannelType: 2}: {{
			ChannelID:   "g1",
			ChannelType: 2,
			MessageSeq:  12,
		}},
	}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.conversations.SetUnread(context.Background(), conversationusecase.SetUnreadCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Unread:      3,
	})
	if err != nil {
		t.Fatalf("SetUnread() error = %v", err)
	}
	err = app.conversations.DeleteConversation(context.Background(), conversationusecase.DeleteConversationCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  12,
	})
	if err != nil {
		t.Fatalf("DeleteConversation() error = %v", err)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if len(cluster.conversationStateBatches) != 1 || len(cluster.conversationStateBatches[0]) != 1 {
		t.Fatalf("conversation state batches = %#v, want one read-state write", cluster.conversationStateBatches)
	}
	got := cluster.conversationStateBatches[0][0]
	if got.UID != "u1" || got.ChannelID != "g1" || got.ChannelType != 2 || got.ReadSeq != 9 {
		t.Fatalf("conversation state = %#v, want read seq 9", got)
	}
	if len(cluster.conversationDeleteBatches) != 1 || len(cluster.conversationDeleteBatches[0]) != 1 {
		t.Fatalf("conversation delete batches = %#v, want one delete-barrier write", cluster.conversationDeleteBatches)
	}
	deleted := cluster.conversationDeleteBatches[0][0]
	if deleted.UID != "u1" || deleted.ChannelID != "g1" || deleted.ChannelType != 2 || deleted.DeletedToSeq != 12 {
		t.Fatalf("conversation delete = %#v, want delete barrier 12", deleted)
	}
}

func TestNewDoesNotWireConversationFallbackWhenAuthorityUnavailable(t *testing.T) {
	cluster := &fakeConversationFallbackCluster{}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.conversationAuthority != nil || app.conversationAuthorityClient != nil {
		t.Fatalf("authority fields = (%T, %T), want authority unavailable", app.conversationAuthority, app.conversationAuthorityClient)
	}
	if app.channelAppends == nil || app.channelAppendRouter == nil {
		t.Fatalf("channel append runtime = (%T, %T), want append-only group and router", app.channelAppends, app.channelAppendRouter)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-fallback-1",
		Payload:     []byte("fallback payload"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	cluster.mu.Lock()
	stateBatches := append([][]metadb.ConversationState(nil), cluster.conversationStateBatches...)
	cluster.mu.Unlock()
	if len(stateBatches) != 0 {
		t.Fatalf("conversation state batches = %#v, want no legacy DB projection", stateBatches)
	}

	list, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Conversations().List() error = %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("conversation list = %#v, want no legacy DB fallback row", list.Items)
	}
}

func TestChannelAppendUpdatesPersonConversationEventually(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	first, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-coalesce-1",
		Payload:     []byte("old"),
	})
	if err != nil || first.Reason != message.ReasonSuccess {
		t.Fatalf("first Send() = %#v err=%v, want success", first, err)
	}
	second, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u2",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-coalesce-2",
		Payload:     []byte("new"),
	})
	if err != nil || second.Reason != message.ReasonSuccess {
		t.Fatalf("second Send() = %#v err=%v, want success", second, err)
	}

	for _, uid := range []string{"u1", "u2"} {
		requireConversationEventually(t, app, uid, channelID, frame.ChannelTypePerson)
	}
}

func TestConversationAuthorityFansOutConfiguredSmallGroups(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-small": []string{"sender", "member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-small",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-small-1",
		Payload:     []byte("small"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	for _, uid := range []string{"sender", "member"} {
		requireConversationEventually(t, app, uid, "g-small", frame.ChannelTypeGroup)
	}
}

func TestChannelAppendUsesDurableSubscribersAndSenderActiveRow(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-small-missing-sender": []string{"member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-small-missing-sender",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-small-missing-sender-1",
		Payload:     []byte("small"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	member := requireConversationEventually(t, app, "member", "g-small-missing-sender", frame.ChannelTypeGroup)
	if member.ReadSeq != 0 {
		t.Fatalf("member ReadSeq = %d, want receiver row without sender read state", member.ReadSeq)
	}
	sender := requireConversationEventually(t, app, "sender", "g-small-missing-sender", frame.ChannelTypeGroup)
	if sender.ReadSeq != result.MessageSeq {
		t.Fatalf("sender ReadSeq = %d, want latest message seq %d", sender.ReadSeq, result.MessageSeq)
	}
}

func TestAppStopFlushesConversationActiveRows(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
			Conversation: ConversationConfig{
				AuthorityFlushInterval:  time.Hour,
				AuthorityFlushBatchRows: 8,
			},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.conversationActiveWorker == nil {
		t.Fatal("conversation active flush worker was not wired")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	channelID := runtimechannelid.EncodePersonChannel("sender", "receiver")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-stop-flush-1",
		Payload:     []byte("flush"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}
	requireConversationEventually(t, app, "sender", channelID, frame.ChannelTypePerson)

	cluster.mu.Lock()
	beforeStop := len(cluster.conversationPatchBatches)
	cluster.mu.Unlock()
	if beforeStop != 0 {
		t.Fatalf("conversation active flush batches before stop = %d, want no periodic flush with hourly interval", beforeStop)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if len(cluster.conversationPatchBatches) == 0 {
		t.Fatal("conversation active flush batches after stop = 0, want final dirty flush")
	}
	patches := conversationPatchesByUID(cluster.conversationPatchBatches[len(cluster.conversationPatchBatches)-1])
	if patches["sender"].ReadSeq != result.MessageSeq {
		t.Fatalf("sender flushed ReadSeq = %d, want %d", patches["sender"].ReadSeq, result.MessageSeq)
	}
	if patches["receiver"].ReadSeq != 0 {
		t.Fatalf("receiver flushed ReadSeq = %d, want receiver row without sender read state", patches["receiver"].ReadSeq)
	}
}

func TestChannelAppendPagesAllGroupSubscribers(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-large": []string{"sender", "member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-large",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-large-1",
		Payload:     []byte("large"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	for _, uid := range []string{"sender", "member"} {
		requireConversationEventually(t, app, uid, "g-large", frame.ChannelTypeGroup)
	}
}

func TestConversationAuthorityRouteLifecycleWatchesLocalAuthorityEvents(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent)
	node := &recordingConversationAuthorityRouteNode{
		nodeID: 1,
		routes: map[string]clusterv2.Route{
			"u1": routeFromConversationTarget(target),
		},
		watch: watch,
	}
	client := clusterinfra.NewConversationAuthorityClient(node, local)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Watch:          node.WatchRouteAuthorities,
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(target)}}
	waitUntil(t, time.Second, func() bool {
		return local.AdmitPatches(context.Background(), target, nil) == nil
	})

	patch := conversationusecase.ActivePatch{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g-watch",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}
	if err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("client AdmitPatches() error = %v", err)
	}
	page, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("client ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g-watch" || page.Rows[0].ActiveAt != 100 {
		t.Fatalf("authority page rows = %#v, want watched local cache row", page.Rows)
	}
}

func TestConversationAuthorityRouteLifecycleStartIdempotentDoesNotCreateSecondWatch(t *testing.T) {
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &appRecordingConversationAuthorityStore{}})
	var mu sync.Mutex
	watchCalls := 0
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Watch: func() <-chan clusterv2.RouteAuthorityEvent {
			mu.Lock()
			defer mu.Unlock()
			watchCalls++
			return make(chan clusterv2.RouteAuthorityEvent)
		},
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	defer lifecycle.Stop(context.Background())
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	mu.Lock()
	got := watchCalls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("watch calls = %d, want 1 for idempotent Start", got)
	}
}

func TestConversationActiveAdmitBatchRPCUpdatesRemoteAndLocalCache(t *testing.T) {
	localTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 2, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 21}
	localStore := &appRecordingConversationAuthorityStore{}
	remoteStore := &appRecordingConversationAuthorityStore{}
	localAuthority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: localStore})
	remoteAuthority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 2, Store: remoteStore})
	localAuthority.markActive(localTarget)
	remoteAuthority.markActive(remoteTarget)
	remoteAdapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &recordingConversationAuthorityRouteNode{
		nodeID: 1,
		routes: map[string]clusterv2.Route{
			"sender":   routeFromConversationTarget(remoteTarget),
			"receiver": routeFromConversationTarget(localTarget),
		},
		handler: appNodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return remoteAdapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := clusterinfra.NewConversationAuthorityClient(node, localAuthority)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g-active",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  1234,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	senderPage, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "sender", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView(sender) error = %v", err)
	}
	if len(senderPage.Rows) != 1 || senderPage.Rows[0].ChannelID != "g-active" || senderPage.Rows[0].ReadSeq != 42 || senderPage.Rows[0].ActiveAt != 1234 {
		t.Fatalf("sender active rows = %#v, want cached sender row with read seq", senderPage.Rows)
	}

	receiverPage, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "receiver", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView(receiver) error = %v", err)
	}
	if len(receiverPage.Rows) != 1 || receiverPage.Rows[0].ChannelID != "g-active" || receiverPage.Rows[0].ReadSeq != 0 || receiverPage.Rows[0].ActiveAt != 1234 {
		t.Fatalf("receiver active rows = %#v, want cached receiver row without sender read seq", receiverPage.Rows)
	}
	if localStore.totalTouchPatches() != 0 || remoteStore.totalTouchPatches() != 0 {
		t.Fatalf("touch patches local/remote = %d/%d, want cache-visible rows before flush", localStore.totalTouchPatches(), remoteStore.totalTouchPatches())
	}
}

func TestConversationAuthorityRouteLifecycleRemoteEventDrainsPreviousLocalTarget(t *testing.T) {
	localTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 21}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(localTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 75 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := local.AdmitPatches(context.Background(), localTarget, []conversationusecase.ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g-drain",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}}); err != nil {
		t.Fatalf("seed AdmitPatches() error = %v", err)
	}

	sent := make(chan struct{})
	go func() {
		watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(remoteTarget)}}
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("timed out sending remote route-authority event")
	}
	waitUntil(t, time.Second, func() bool {
		return store.totalTouchPatches() == 1
	})
	if !store.lastTouchDeadlineWithin(75*time.Millisecond, 75*time.Millisecond) {
		t.Fatalf("handoff drain deadline = %v, want about 75ms", store.lastTouchDeadline())
	}
	if _, err := local.ListConversationActiveViewForTarget(context.Background(), localTarget, metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10); !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("stale local ListConversationActiveViewForTarget() error = %v, want %v", err, conversationusecase.ErrStaleRoute)
	}
	if err := local.AdmitPatches(context.Background(), localTarget, []conversationusecase.ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g-drain-2",
		ChannelType: 2,
		ActiveAt:    102,
		MessageSeq:  8,
	}}); !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("stale local AdmitPatches() error = %v, want %v", err, conversationusecase.ErrStaleRoute)
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestConversationAuthorityRouteLifecycleDrainDoesNotBlockNewLocalAuthority(t *testing.T) {
	oldLocalTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 21}
	newLocalTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 12, AuthorityEpoch: 22}
	touchStarted := make(chan struct{})
	touchBlock := make(chan struct{})
	store := &appRecordingConversationAuthorityStore{
		touchStarted: touchStarted,
		touchBlock:   touchBlock,
	}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent, 2)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(oldLocalTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 250 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		close(touchBlock)
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()
	if err := local.AdmitPatches(context.Background(), oldLocalTarget, []conversationusecase.ActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g-blocking-drain",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}}); err != nil {
		t.Fatalf("seed AdmitPatches() error = %v", err)
	}

	watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(remoteTarget)}}
	select {
	case <-touchStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handoff drain to enter store touch")
	}
	watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(newLocalTarget)}}

	waitUntil(t, 75*time.Millisecond, func() bool {
		return local.AdmitPatches(context.Background(), newLocalTarget, nil) == nil
	})
}

func TestConversationAuthorityRouteLifecycleIgnoresStaleRouteEvents(t *testing.T) {
	currentTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	staleTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 9, AuthorityEpoch: 99}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent, 1)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(currentTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	sent := make(chan struct{})
	go func() {
		watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(staleTarget)}}
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("timed out sending stale route-authority event")
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if store.totalTouchPatches() != 0 {
		t.Fatalf("touch patches = %d, want stale route event ignored without drain", store.totalTouchPatches())
	}
	if err := local.AdmitPatches(context.Background(), currentTarget, nil); err != nil {
		t.Fatalf("current local target was not preserved: %v", err)
	}
}

func TestConversationAuthorityRouteLifecyclePrefersConfigEpochBeforeAuthorityEpoch(t *testing.T) {
	currentTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	staleTarget := currentTarget
	staleTarget.LeaderNodeID = 2
	staleTarget.ConfigEpoch = 2
	staleTarget.AuthorityEpoch = 99
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent, 1)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(currentTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(staleTarget)}}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if store.totalTouchPatches() != 0 {
		t.Fatalf("touch patches = %d, want lower config epoch ignored without drain", store.totalTouchPatches())
	}
	if err := local.AdmitPatches(context.Background(), currentTarget, nil); err != nil {
		t.Fatalf("current local target was not preserved: %v", err)
	}
}

func TestConversationAuthorityRouteLifecyclePeriodicReconcileRepairsDroppedAuthorityEvent(t *testing.T) {
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 20}
	localTarget := remoteTarget
	localTarget.LeaderNodeID = 1
	localTarget.AuthorityEpoch = 21
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	var mu sync.Mutex
	authorities := []clusterv2.RouteAuthority{authorityFromConversationTarget(remoteTarget)}
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority:    local,
		LocalNodeID:       1,
		HandoffTimeout:    50 * time.Millisecond,
		ReconcileInterval: 10 * time.Millisecond,
		Initial: func() []clusterv2.RouteAuthority {
			mu.Lock()
			defer mu.Unlock()
			return append([]clusterv2.RouteAuthority(nil), authorities...)
		},
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer lifecycle.Stop(context.Background())
	if err := local.AdmitPatches(context.Background(), localTarget, nil); !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("local target before reconcile error = %v, want ErrStaleRoute", err)
	}

	mu.Lock()
	authorities = []clusterv2.RouteAuthority{authorityFromConversationTarget(localTarget)}
	mu.Unlock()

	waitUntil(t, time.Second, func() bool {
		return local.AdmitPatches(context.Background(), localTarget, nil) == nil
	})
}

func TestDeliveryWorkerGroupStopKeepsDependenciesRunningWhenDrainFails(t *testing.T) {
	retry := &recordingWorkerRuntime{}
	manager := &recordingWorkerRuntime{stopErr: context.DeadlineExceeded}
	group := deliveryWorkerGroup{retry, manager}

	err := group.Stop(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	if manager.stopCount != 1 {
		t.Fatalf("manager stop count = %d, want 1", manager.stopCount)
	}
	if retry.stopCount != 0 {
		t.Fatalf("retry stop count = %d, want dependency kept running", retry.stopCount)
	}
}

func TestAppSubscriberPlannerReturnsPersonChannelUIDs(t *testing.T) {
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	page, err := appSubscriberPlanner{}.NextPartitionPage(context.Background(), runtimedelivery.FanoutTask{
		Envelope: runtimedelivery.Envelope{ChannelID: channelID, ChannelType: frame.ChannelTypePerson},
	}, "", 512)
	if err != nil {
		t.Fatalf("NextPartitionPage() error = %v", err)
	}
	if !page.Done {
		t.Fatalf("Done = false, want true")
	}
	if len(page.UIDs) != 2 || page.UIDs[0] == page.UIDs[1] {
		t.Fatalf("UIDs = %#v, want two distinct participants", page.UIDs)
	}
	want := map[string]bool{"u1": true, "u2": true}
	for _, uid := range page.UIDs {
		if !want[uid] {
			t.Fatalf("unexpected UID %q in %#v", uid, page.UIDs)
		}
	}
}

func TestDeliveryRuntimeAdapterScopesPersonChannelAcrossPartitions(t *testing.T) {
	runner := &appRecordingFanoutRunner{}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Planner: runtimedelivery.NewPlanner(runtimedelivery.PlannerOptions{
			Partitioner: appStaticDeliveryPartitioner{
				partitions: []runtimedelivery.Partition{
					{ID: 1, LeaderNodeID: 1},
					{ID: 2, LeaderNodeID: 2},
					{ID: 3, LeaderNodeID: 3},
				},
			},
		}),
		Runner: runner,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")

	err := deliveryRuntimeAdapter{manager: manager}.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		MessageID:   1,
		MessageSeq:  1,
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		FromUID:     "u1",
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	tasks := waitAppFanoutTasks(t, runner, 1, time.Second)
	if len(tasks) != 1 {
		t.Fatalf("fanout tasks = %d, want 1 scoped person task", len(tasks))
	}
	got := tasks[0].Envelope.MessageScopedUIDs
	if len(got) != 2 {
		t.Fatalf("MessageScopedUIDs = %#v, want two participants", got)
	}
	want := map[string]bool{"u1": true, "u2": true}
	for _, uid := range got {
		if !want[uid] {
			t.Fatalf("unexpected scoped UID %q in %#v", uid, got)
		}
	}
}

func waitAppDeliveryPendingAckCount(t *testing.T, app *App, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = app.deliveryManager.PendingAckCount()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending ack count = %d, want %d", got, want)
}

func waitAppFanoutTasks(t *testing.T, runner *appRecordingFanoutRunner, want int, timeout time.Duration) []runtimedelivery.FanoutTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var tasks []runtimedelivery.FanoutTask
	for time.Now().Before(deadline) {
		tasks = runner.snapshot()
		if len(tasks) == want {
			return tasks
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fanout tasks = %d, want %d", len(tasks), want)
	return nil
}

func TestDeliveryEnabledPersonSendWritesRecvAndRecvackClearsPending(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{
				Enabled:        true,
				EventQueueSize: 8,
			},
			Presence: PresenceConfig{TouchFlushInterval: time.Hour},
		},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	senderWrites := &sendackSmokeSessionWrites{}
	recipientWrites := &sendackSmokeSessionWrites{}
	sender := newAppDeliveryTestSession(101, senderWrites)
	recipient := newAppDeliveryTestSession(102, recipientWrites)
	activateAppDeliverySession(t, app, sender, "u1")
	activateAppDeliverySession(t, app, recipient, "u2")

	send := &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "client-person-1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}
	if err := app.Handler().OnFrame(gateway.Context{Session: sender, RequestContext: context.Background()}, send); err != nil {
		t.Fatalf("OnFrame(send) error = %v", err)
	}
	ack := senderWrites.requireOnlySendack(t)
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want success", ack.ReasonCode)
	}

	recv := recipientWrites.waitForRecvPacket(t, time.Second)
	if recv.MessageID != ack.MessageID || recv.MessageSeq != ack.MessageSeq {
		t.Fatalf("recv id/seq = %d/%d, want %d/%d", recv.MessageID, recv.MessageSeq, ack.MessageID, ack.MessageSeq)
	}
	if recv.ChannelID != "u1" || recv.ChannelType != frame.ChannelTypePerson || recv.FromUID != "u1" || string(recv.Payload) != "hello" {
		t.Fatalf("recv packet = %#v, want person view from u1", recv)
	}
	waitAppDeliveryPendingAckCount(t, app, 1, time.Second)

	if err := app.Handler().OnFrame(gateway.Context{Session: recipient, RequestContext: context.Background()}, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	}); err != nil {
		t.Fatalf("OnFrame(recvack) error = %v", err)
	}
	waitAppDeliveryPendingAckCount(t, app, 0, time.Second)
}

func TestDeliveryEnabledGroupSendUsesSubscriberSource(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	cluster.channels = map[metadb.ConversationKey]metadb.Channel{
		{ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup)}: {
			ChannelID:                 "g1",
			ChannelType:               int64(frame.ChannelTypeGroup),
			Large:                     1,
			SubscriberMutationVersion: 1,
		},
	}
	subscribers := &fakeDeliverySubscriberSource{
		pages: []runtimedelivery.UIDPage{
			{UIDs: []string{"u2"}, Done: true},
		},
	}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{
				Enabled:        true,
				EventQueueSize: 8,
			},
			Presence: PresenceConfig{TouchFlushInterval: time.Hour},
		},
		WithCluster(cluster),
		WithDeliverySubscriberSource(subscribers),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	senderWrites := &sendackSmokeSessionWrites{}
	recipientWrites := &sendackSmokeSessionWrites{}
	sender := newAppDeliveryTestSession(201, senderWrites)
	recipient := newAppDeliveryTestSession(202, recipientWrites)
	activateAppDeliverySession(t, app, sender, "u1")
	activateAppDeliverySession(t, app, recipient, "u2")

	send := &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "client-group-1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hello group"),
	}
	if err := app.Handler().OnFrame(gateway.Context{Session: sender, RequestContext: context.Background()}, send); err != nil {
		t.Fatalf("OnFrame(send) error = %v", err)
	}
	ack := senderWrites.requireOnlySendack(t)
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want success", ack.ReasonCode)
	}

	recv := recipientWrites.waitForRecvPacket(t, time.Second)
	if recv.MessageID != ack.MessageID || recv.MessageSeq != ack.MessageSeq {
		t.Fatalf("recv id/seq = %d/%d, want %d/%d", recv.MessageID, recv.MessageSeq, ack.MessageID, ack.MessageSeq)
	}
	if recv.ChannelID != "g1" || recv.ChannelType != frame.ChannelTypeGroup || recv.FromUID != "u1" ||
		string(recv.Payload) != "hello group" {
		t.Fatalf("recv packet = %#v, want group delivery from u1", recv)
	}
	waitAppDeliveryPendingAckCount(t, app, 1, time.Second)
	if len(subscribers.requests) != 1 {
		t.Fatalf("subscriber requests = %d, want 1", len(subscribers.requests))
	}
	req := subscribers.requests[0]
	if req.ChannelID != "g1" || req.ChannelType != frame.ChannelTypeGroup || req.Limit != app.cfg.Delivery.FanoutPageSize {
		t.Fatalf("subscriber request = %#v, want group channel with configured page size", req)
	}
	if req.Partition != (runtimedelivery.Partition{}) {
		t.Fatalf("subscriber partition = %#v, want recipient-authority unpartitioned scan", req.Partition)
	}

	if err := app.Handler().OnFrame(gateway.Context{Session: recipient, RequestContext: context.Background()}, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	}); err != nil {
		t.Fatalf("OnFrame(recvack) error = %v", err)
	}
	waitAppDeliveryPendingAckCount(t, app, 0, time.Second)
}

func TestDeliveryMetaStoreWritesBenchDataAndFiltersSubscriberPages(t *testing.T) {
	const hashSlotCount = 16
	slotThreeUID := testUIDForHashSlot(t, 3, hashSlotCount)
	slotNineUID := testUIDForHashSlot(t, 9, hashSlotCount)
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, hashSlotCount),
		subscribers: map[string][]string{"g1": []string{slotThreeUID, slotNineUID}},
	}
	store := newDeliveryMetaStore(node)

	acceptedChannels, err := store.UpsertChannels(context.Background(), []accessapi.BenchChannelMutation{{
		ChannelID:     "g1",
		ChannelType:   frame.ChannelTypeGroup,
		AllowStranger: true,
	}})
	if err != nil {
		t.Fatalf("UpsertChannels() error = %v", err)
	}
	if acceptedChannels != 1 || len(node.upserted) != 1 || node.upserted[0].ChannelID != "g1" || node.upserted[0].AllowStranger != 1 {
		t.Fatalf("upserted = %#v accepted=%d, want real channel metadata", node.upserted, acceptedChannels)
	}
	acceptedSubscribers, err := store.AddSubscribers(context.Background(), []accessapi.BenchSubscriberMutation{{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{slotThreeUID},
	}, {
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{slotNineUID},
	}})
	if err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	if acceptedSubscribers != 2 || len(node.added) != 2 || node.added[0].version != 1 || node.added[1].version != 2 {
		t.Fatalf("added = %#v accepted=%d, want ordered subscriber mutation versions 1 and 2", node.added, acceptedSubscribers)
	}

	page, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 3, HashSlotEnd: 3},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers() error = %v", err)
	}
	if len(page.UIDs) != 1 || page.UIDs[0] != slotThreeUID || !page.Done {
		t.Fatalf("slot-filtered page = %#v, want only slot 3 uid", page)
	}
	first, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 0, HashSlotEnd: hashSlotCount - 1},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(first) error = %v", err)
	}
	if len(first.UIDs) != 1 || first.NextCursor == "" || first.Done {
		t.Fatalf("first page = %#v, want one uid and continuation", first)
	}
	second, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 0, HashSlotEnd: hashSlotCount - 1},
		Cursor:      first.NextCursor,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(second) error = %v", err)
	}
	if len(second.UIDs) != 1 || !second.Done {
		t.Fatalf("second page = %#v, want final uid", second)
	}
}

func TestDeliveryMetaStoreCachesSubscriberSnapshotAcrossPartitions(t *testing.T) {
	const hashSlotCount = 16
	slotThreeUID := testUIDForHashSlot(t, 3, hashSlotCount)
	slotNineUID := testUIDForHashSlot(t, 9, hashSlotCount)
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, hashSlotCount),
		subscribers: map[string][]string{"g1": []string{slotThreeUID, slotNineUID}},
	}
	store := newDeliveryMetaStore(node)

	first, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 3, HashSlotEnd: 3},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(first) error = %v", err)
	}
	second, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 9, HashSlotEnd: 9},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(second) error = %v", err)
	}

	if len(first.UIDs) != 1 || first.UIDs[0] != slotThreeUID || len(second.UIDs) != 1 || second.UIDs[0] != slotNineUID {
		t.Fatalf("partition pages first=%#v second=%#v, want cached slot-specific results", first, second)
	}
	if node.listCalls != 1 {
		t.Fatalf("subscriber list calls = %d, want one cached channel snapshot read", node.listCalls)
	}
}

func TestDeliveryMetaStoreInvalidatesSubscriberCacheAfterMutation(t *testing.T) {
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, 16),
		subscribers: map[string][]string{"g1": []string{"u1"}},
	}
	store := newDeliveryMetaStore(node)
	if _, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Limit: 10}); err != nil {
		t.Fatalf("ListSubscribers(before) error = %v", err)
	}
	if _, err := store.AddSubscribers(context.Background(), []accessapi.BenchSubscriberMutation{{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{"u2"},
	}}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	node.mu.Lock()
	node.subscribers["g1"] = []string{"u1", "u2"}
	node.mu.Unlock()
	after, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Limit: 10})
	if err != nil {
		t.Fatalf("ListSubscribers(after) error = %v", err)
	}
	if len(after.UIDs) != 2 || after.UIDs[1] != "u2" {
		t.Fatalf("after mutation page = %#v, want refreshed subscribers", after)
	}
	if node.listCalls != 2 {
		t.Fatalf("subscriber list calls = %d, want cache miss after mutation", node.listCalls)
	}
}

func TestNewWiresDeliveryMetaStoreWhenClusterProvidesRealMetadata(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{
		fakeCluster: fakeCluster{calls: &[]string{}},
		snapshot:    readyFakeClusterSnapshot(1, 16),
		subscribers: map[string][]string{},
	}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{Enabled: true},
		},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.deliverySubscribers == nil {
		t.Fatal("delivery subscriber source was not wired")
	}
	if _, ok := app.deliverySubscribers.(*deliveryMetaStore); !ok {
		t.Fatalf("deliverySubscribers = %T, want *deliveryMetaStore", app.deliverySubscribers)
	}
}

func TestNewWiresConversationUsecaseWhenClusterProvidesConversationReads(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.Conversations() == nil {
		t.Fatal("conversation usecase was not wired")
	}
	result, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Conversations().List() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("conversation items = %d, want empty page", len(result.Items))
	}
}

func TestNewDoesNotOverwriteWithMessagesWhenDeliveryEnabled(t *testing.T) {
	override := message.New(message.Options{})
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}, Delivery: DeliveryConfig{Enabled: true}},
		WithCluster(newFakePresenceCluster(1, nil)),
		WithMessages(override),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.Messages() != override {
		t.Fatal("New() overwrote WithMessages override")
	}
}

func TestNewWiresPresenceWhenGatewayEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	gatewayRuntime := &fakeGateway{calls: &[]string{}}

	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Gateway: GatewayConfig{Listeners: []gateway.ListenerOptions{{
				Network: "tcp",
				Address: "127.0.0.1:0",
			}}},
		},
		WithCluster(cluster),
		WithGateway(gatewayRuntime),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if app.presence == nil {
		t.Fatal("presence usecase was not wired")
	}
	if app.online == nil {
		t.Fatal("online registry was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.PresenceAuthorityRPCServiceID]; !ok {
		t.Fatalf("presence authority rpc service was not registered")
	}
	if _, ok := cluster.registeredHandlers[accessnode.PresenceOwnerRPCServiceID]; !ok {
		t.Fatalf("presence owner rpc service was not registered")
	}
	if app.Handler() == nil {
		t.Fatal("gateway handler was not wired")
	}
	if _, err := app.Handler().OnSessionActivate(nil); !errors.Is(err, accessgateway.ErrUnauthenticatedSession) {
		t.Fatalf("OnSessionActivate(nil) error = %v, want unauthenticated session instead of missing presence", err)
	}
}

func TestLocalOwnerPusherWritesRecvPacketAndBindsPendingAck(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	pusher := localOwnerPusher{online: reg, delivery: manager}
	env := runtimedelivery.Envelope{
		MessageID:   9001,
		MessageSeq:  42,
		ChannelID:   "ch1",
		ChannelType: 2,
		FromUID:     "sender",
		ClientMsgNo: "client-1",
		RedDot:      true,
		Payload:     []byte("hello"),
	}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    env,
		Routes: []runtimedelivery.Route{{
			UID:         "u1",
			OwnerNodeID: 1,
			OwnerBootID: 7,
			OwnerSeq:    11,
			SessionID:   101,
		}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want one accepted", result)
	}
	if len(session.writes) != 1 {
		t.Fatalf("delivery writes = %d, want 1", len(session.writes))
	}
	recv, ok := session.writes[0].(*frame.RecvPacket)
	if !ok {
		t.Fatalf("delivery write = %T, want *frame.RecvPacket", session.writes[0])
	}
	if recv.MessageID != int64(env.MessageID) || recv.MessageSeq != env.MessageSeq || recv.ChannelID != env.ChannelID ||
		recv.ChannelType != env.ChannelType || recv.FromUID != env.FromUID || recv.ClientMsgNo != env.ClientMsgNo ||
		string(recv.Payload) != "hello" || !recv.RedDot {
		t.Fatalf("recv packet = %#v", recv)
	}
	env.Payload[0] = 'H'
	if string(recv.Payload) != "hello" {
		t.Fatalf("recv payload = %q, want cloned hello", string(recv.Payload))
	}
	if manager.PendingAckCount() != 1 {
		t.Fatalf("pending ack count = %d, want 1", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsMissingOrInactiveSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	inactiveRoute := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: inactiveRoute, Session: &recordingSessionHandle{}}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes: []runtimedelivery.Route{
			{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101},
			{UID: "missing", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 102},
		},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 2 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want two dropped", result)
	}
}

func TestLocalOwnerPusherDropsWhenPendingAckLimitReached(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 1, MaxPendingPerSession: 1}),
	})
	if !manager.BindPendingAck(runtimedelivery.PendingRecvAck{UID: "u1", SessionID: 101, MessageID: 1}) {
		t.Fatalf("preload pending ack failed")
	}
	pusher := localOwnerPusher{online: reg, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want one dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
	if manager.PendingAckCount() != 1 {
		t.Fatalf("pending ack count = %d, want preload only", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherMarksWriteErrorRetryable(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{writeErr: errors.New("write failed")}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Retryable) != 1 || len(result.Accepted) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want one retryable", result)
	}
	if pusher.delivery.PendingAckCount() != 0 {
		t.Fatalf("pending ack count = %d, want 0 after write error", pusher.delivery.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsTerminalWriteErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "session closed", err: session.ErrSessionClosed},
		{name: "outbound overflow", err: gatewaytransport.ErrOutboundBytesExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
			sessionHandle := &recordingSessionHandle{writeErr: tt.err}
			route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
			if err := reg.RegisterPending(online.LocalSession{Route: route, Session: sessionHandle}); err != nil {
				t.Fatalf("RegisterPending() error = %v", err)
			}
			if err := reg.MarkActive(route.SessionID); err != nil {
				t.Fatalf("MarkActive() error = %v", err)
			}
			manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
			pusher := localOwnerPusher{online: reg, delivery: manager}

			result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
				OwnerNodeID: 1,
				Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
				Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
			})
			if err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
				t.Fatalf("push result = %#v, want one dropped", result)
			}
			if manager.PendingAckCount() != 0 {
				t.Fatalf("pending ack count = %d, want 0 after terminal write error", manager.PendingAckCount())
			}
		})
	}
}

func TestLocalOwnerPusherExpiresPendingAcksDuringPushActivity(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	now := int64(200)
	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 1,
		Now: func() int64 {
			return now
		},
	})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{Acks: tracker})
	manager.BindPendingAck(runtimedelivery.PendingRecvAck{UID: "u1", SessionID: 101, MessageID: 1, MessageSeq: 1, DeliveredAt: 100})
	pusher := localOwnerPusher{online: reg, delivery: manager, pendingAckTTL: 50 * time.Second}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("accepted routes = %d, want 1", len(result.Accepted))
	}
	if _, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 1}); ok {
		t.Fatalf("old pending ack still exists after delivery activity expiration")
	}
	if pending, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 2}); !ok || pending.MessageID != 2 {
		t.Fatalf("new pending ack = %#v, %v, want message 2 true", pending, ok)
	}
}

func TestLocalOwnerPusherDropsOverflowMessageID(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	pusher := localOwnerPusher{online: reg, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: uint64(1 << 63), MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want one dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
	if manager.PendingAckCount() != 0 {
		t.Fatalf("pending ack count = %d, want 0", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsIncompleteRouteIdentity(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want incomplete identity dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
}

func TestPresenceBenchSnapshotAggregatesOwnerAndAuthorityState(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{NodeID: 1}}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.presenceDirectory == nil {
		t.Fatal("presence directory was not wired")
	}
	pending := online.OwnerRoute{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 1, SessionID: 101, ConnectedUnix: 100}
	active := online.OwnerRoute{UID: "u2", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 2, SessionID: 102, ConnectedUnix: 100}
	if err := app.online.RegisterPending(online.LocalSession{Route: pending}); err != nil {
		t.Fatalf("RegisterPending(pending) error = %v", err)
	}
	if err := app.online.RegisterPending(online.LocalSession{Route: active}); err != nil {
		t.Fatalf("RegisterPending(active) error = %v", err)
	}
	if err := app.online.MarkActive(active.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	app.online.MarkTouched(active.SessionID, 120)

	target := presence.RouteTarget{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}
	app.presenceDirectory.BecomeAuthority(target)
	if _, err := app.presenceDirectory.RegisterRoute(target, presence.Route{UID: "u2", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 2, SessionID: 102, ConnectedUnix: 100, LastSeenUnix: 120}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := app.presenceDirectory.TouchRoutes(target, []presence.Route{{UID: "u3", OwnerNodeID: 2, OwnerBootID: 8, OwnerSeq: 1, SessionID: 201, ConnectedUnix: 100, LastSeenUnix: 121}}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	controller := app.benchPresenceController()
	if controller == nil {
		t.Fatal("bench presence controller is nil")
	}
	snap, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap.NodeID != 1 || snap.OwnerRoutesPending != 1 || snap.OwnerRoutesActive != 1 || snap.OwnerTouchedDirty != 1 {
		t.Fatalf("owner snapshot = %+v, want node 1 pending 1 active 1 dirty 1", snap)
	}
	if snap.AuthorityRoutesActive != 2 || snap.AuthorityRoutesByHashSlot[9] != 2 || snap.TouchRoutesTotal != 1 {
		t.Fatalf("authority snapshot = %+v, want active 2 hashSlot 9 count 2 touch total 1", snap)
	}
}

func TestStartOrderStartsClusterThenPresenceWorkerThenGateway(t *testing.T) {
	calls := make([]string, 0, 3)
	events := make(chan clusterv2.RouteAuthorityEvent)
	cluster := newFakePresenceCluster(1, events)
	cluster.calls = &calls
	cluster.snapshot = clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,presence.start,presence.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,presence.start,presence.start,gateway.start", got)
	}
}

func TestStartSeedsPresenceAuthorityFromCurrentRoutes(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent)
	cluster := newFakePresenceCluster(1, events)
	cluster.snapshot = clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 10}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(&fakeGateway{calls: &[]string{}}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer app.Stop(context.Background())

	err = app.presence.Activate(context.Background(), presence.ActivateCommand{
		UID:       "u1",
		SessionID: 11,
	})
	if err != nil {
		t.Fatalf("Activate() error = %v, want seeded local authority", err)
	}
}

func TestPresenceTouchWorkerFlushesDirtyRoutesByTarget(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conns := []online.OwnerRoute{
		{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1001},
		{UID: "u2", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 12, SessionID: 102, DeviceID: "d2", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1002},
		{UID: "u3", HashSlot: 8, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 13, SessionID: 103, DeviceID: "d3", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1003},
	}
	for _, conn := range conns {
		if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
			t.Fatalf("RegisterPending(%d) error = %v", conn.SessionID, err)
		}
		if err := reg.MarkActive(conn.SessionID); err != nil {
			t.Fatalf("MarkActive(%d) error = %v", conn.SessionID, err)
		}
		reg.MarkTouched(conn.SessionID, conn.ConnectedUnix+10)
	}
	targetA := presence.RouteTarget{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}
	targetB := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 2, RouteRevision: 4, AuthorityEpoch: 5}
	authority := &recordingTouchAuthority{targets: map[string]presence.RouteTarget{
		"u1": targetA,
		"u2": targetA,
		"u3": targetB,
	}}
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		Directory: directory,
		BatchSize: 10,
		RouteTTL:  90 * time.Second,
	})

	worker.flushOnce(context.Background(), time.Unix(2000, 0))

	if len(authority.batches) != 2 {
		t.Fatalf("touch batches = %d, want 2", len(authority.batches))
	}
	if got := len(routesForTarget(authority.batches, targetA)); got != 2 {
		t.Fatalf("targetA route count = %d, want 2", got)
	}
	targetBRoutes := routesForTarget(authority.batches, targetB)
	if len(targetBRoutes) != 1 {
		t.Fatalf("targetB route count = %d, want 1", len(targetBRoutes))
	}
	if targetBRoutes[0].LastSeenUnix != conns[2].ConnectedUnix+10 {
		t.Fatalf("LastSeenUnix = %d, want %d", targetBRoutes[0].LastSeenUnix, conns[2].ConnectedUnix+10)
	}
	if len(reg.DrainTouched(10)) != 0 {
		t.Fatalf("dirty routes were not cleared after successful touch")
	}
	if len(directory.expires) != 1 {
		t.Fatalf("ExpireRoutes calls = %d, want 1", len(directory.expires))
	}
}

func TestPresenceTouchWorkerRequeuesFailedFlush(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conn := online.OwnerRoute{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(conn.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	reg.MarkTouched(conn.SessionID, 1010)
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{"u1": {HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}},
		err:     errors.New("touch failed"),
	}
	logger := &recordingAppLogger{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		BatchSize: 10,
		Logger:    logger,
	})

	worker.flushOnce(context.Background(), time.Now())

	requeued := reg.DrainTouched(10)
	if len(requeued) != 1 {
		t.Fatalf("requeued dirty routes = %d, want 1", len(requeued))
	}
	if requeued[0].SessionID != conn.SessionID || requeued[0].UID != conn.UID {
		t.Fatalf("requeued route = %#v, want session %d uid %s", requeued[0], conn.SessionID, conn.UID)
	}
	requireAppLogEvent(t, logger, "WARN", "internalv2.app.presence_touch_failed")
}

func TestPresenceTouchWorkerRequeuesAllGroupsWhenContextCancelsAfterDrain(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conns := []online.OwnerRoute{
		{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001},
		{UID: "u2", HashSlot: 8, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 12, SessionID: 102, ConnectedUnix: 1002},
	}
	for _, conn := range conns {
		if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
			t.Fatalf("RegisterPending(%d) error = %v", conn.SessionID, err)
		}
		if err := reg.MarkActive(conn.SessionID); err != nil {
			t.Fatalf("MarkActive(%d) error = %v", conn.SessionID, err)
		}
		reg.MarkTouched(conn.SessionID, conn.ConnectedUnix+10)
	}
	ctx, cancel := context.WithCancel(context.Background())
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{
			"u1": {HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2},
			"u2": {HashSlot: 8, SlotID: 1, LeaderNodeID: 2, RouteRevision: 4, AuthorityEpoch: 5},
		},
		resolveHook: func(string) { cancel() },
	}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		BatchSize: 10,
	})

	worker.flushOnce(ctx, time.Now())

	if len(authority.batches) != 0 {
		t.Fatalf("touch batches = %d, want 0 after context cancellation", len(authority.batches))
	}
	requeued := reg.DrainTouched(10)
	if len(requeued) != len(conns) {
		t.Fatalf("requeued dirty routes = %d, want %d", len(requeued), len(conns))
	}
}

func TestOwnerRouteFromRouteCarriesRouteMetadata(t *testing.T) {
	route := presence.Route{
		UID:           "u1",
		OwnerNodeID:   1,
		OwnerBootID:   7,
		OwnerSeq:      11,
		SessionID:     101,
		DeviceID:      "d1",
		DeviceFlag:    2,
		DeviceLevel:   3,
		Listener:      "tcp",
		ConnectedUnix: 1001,
		LastSeenUnix:  1010,
	}

	conn := ownerRouteFromRoute(route)

	if conn.DeviceID != route.DeviceID ||
		conn.DeviceFlag != route.DeviceFlag ||
		conn.DeviceLevel != route.DeviceLevel ||
		conn.Listener != route.Listener {
		t.Fatalf("online conn metadata = %#v, want route metadata %#v", conn, route)
	}
	if conn.LastActivityUnix != route.LastSeenUnix {
		t.Fatalf("LastActivityUnix = %d, want %d", conn.LastActivityUnix, route.LastSeenUnix)
	}
}

func TestPresenceOwnerActionsClosesAndUnregistersMatchingLocalSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}

	actions := presenceOwnerActions{local: reg}
	err := actions.ApplyRouteAction(context.Background(), presence.RouteAction{
		UID:         route.UID,
		OwnerNodeID: route.OwnerNodeID,
		OwnerBootID: route.OwnerBootID,
		SessionID:   route.SessionID,
		Reason:      "conflict",
	})
	if err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if session.reason != "conflict" {
		t.Fatalf("close reason = %q, want conflict", session.reason)
	}
	if _, ok := reg.LocalSession(route.SessionID); ok {
		t.Fatalf("session %d still registered after owner action", route.SessionID)
	}
}

func TestPresenceOwnerActionsIgnoresMismatchedLocalSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}

	actions := presenceOwnerActions{local: reg}
	err := actions.ApplyRouteAction(context.Background(), presence.RouteAction{
		UID:         route.UID,
		OwnerNodeID: route.OwnerNodeID,
		OwnerBootID: route.OwnerBootID + 1,
		SessionID:   route.SessionID,
		Reason:      "stale conflict",
	})
	if err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if session.reason != "" {
		t.Fatalf("session was closed with reason %q, want no close", session.reason)
	}
	if _, ok := reg.LocalSession(route.SessionID); !ok {
		t.Fatalf("session %d was unregistered for a mismatched action", route.SessionID)
	}
}

func TestPresenceTouchWorkerIgnoresStaleAuthorityAfterNewerEvent(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   2,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	})

	if got := directory.becomeSnapshot(); len(got) != 1 || got[0].LeaderNodeID != 1 || got[0].AuthorityEpoch != 3 {
		t.Fatalf("become targets = %#v, want one current local authority", got)
	}
	if got := directory.loseSnapshot(); len(got) != 0 {
		t.Fatalf("lost slots = %v, want stale remote authority ignored", got)
	}
}

func TestPresenceTouchWorkerAcceptsNewerNoLeaderAuthority(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:      9,
		SlotID:        1,
		LeaderNodeID:  0,
		RouteRevision: 5,
	})

	if got := directory.loseSnapshot(); !reflect.DeepEqual(got, []uint16{9}) {
		t.Fatalf("lost slots = %v, want newer no-leader authority to clear local authority", got)
	}
}

func TestPresenceTouchWorkerAcceptsSameRaftIdentityWithDifferentAuthorityEpoch(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   0,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  4,
		AuthorityEpoch: 4,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  4,
		AuthorityEpoch: 5,
	})

	got := directory.becomeSnapshot()
	if len(got) != 2 || got[0].AuthorityEpoch != 3 || got[1].AuthorityEpoch != 5 || got[1].LeaderTerm != 9 || got[1].ConfigEpoch != 3 {
		t.Fatalf("become targets = %#v, want same raft identity with epochs 3 then 5", got)
	}
	if lost := directory.loseSnapshot(); !reflect.DeepEqual(lost, []uint16{9}) {
		t.Fatalf("lost slots = %v, want one no-leader clear", lost)
	}
}

func TestPresenceTouchWorkerPrefersConfigEpochBeforeAuthorityEpoch(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   2,
		LeaderTerm:     9,
		ConfigEpoch:    2,
		RouteRevision:  4,
		AuthorityEpoch: 99,
	})

	if got := directory.becomeSnapshot(); len(got) != 1 || got[0].LeaderNodeID != 1 {
		t.Fatalf("become targets = %#v, want original local authority preserved", got)
	}
	if got := directory.loseSnapshot(); len(got) != 0 {
		t.Fatalf("lost slots = %v, want lower config epoch ignored", got)
	}
}

func TestCurrentPresenceAuthoritiesIncludesLeaderTermAndConfigEpoch(t *testing.T) {
	cluster := &fakeWriteReadyCluster{
		snapshots: []clusterv2.Snapshot{{HashSlotCount: 1}},
		routes: map[uint16]clusterv2.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 0, LeaderTerm: 9, ConfigEpoch: 5, Revision: 4, AuthorityEpoch: 3},
		},
	}
	app := &App{cluster: cluster}

	got := app.currentPresenceAuthorities()

	if len(got) != 1 {
		t.Fatalf("authorities len = %d, want 1", len(got))
	}
	if got[0].LeaderNodeID != 0 || got[0].LeaderTerm != 9 || got[0].ConfigEpoch != 5 || got[0].RouteRevision != 4 || got[0].AuthorityEpoch != 3 {
		t.Fatalf("authority = %#v, want no-leader term 9 config 5 revision 4 epoch 3", got[0])
	}
}

func TestPresenceTouchWorkerPeriodicReconcileRepairsDroppedAuthorityEvent(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	remote := clusterv2.RouteAuthority{HashSlot: 9, SlotID: 1, LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 4, AuthorityEpoch: 3}
	local := remote
	local.LeaderNodeID = 1
	local.AuthorityEpoch = 4
	var mu sync.Mutex
	authorities := []clusterv2.RouteAuthority{remote}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:        1,
		Directory:     directory,
		FlushInterval: 10 * time.Millisecond,
		Initial: func() []clusterv2.RouteAuthority {
			mu.Lock()
			defer mu.Unlock()
			return append([]clusterv2.RouteAuthority(nil), authorities...)
		},
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Stop(context.Background())
	waitUntil(t, time.Second, func() bool {
		return len(directory.loseSnapshot()) == 1
	})

	mu.Lock()
	authorities = []clusterv2.RouteAuthority{local}
	mu.Unlock()

	waitUntil(t, time.Second, func() bool {
		got := directory.becomeSnapshot()
		return len(got) == 1 && got[0].LeaderNodeID == 1 && got[0].LeaderTerm == 9 && got[0].ConfigEpoch == 3 && got[0].AuthorityEpoch == 4
	})
}

func TestPresenceTouchWorkerStartIdempotentDoesNotCreateSecondWatch(t *testing.T) {
	var mu sync.Mutex
	watchCalls := 0
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:        1,
		Directory:     &recordingPresenceDirectory{},
		FlushInterval: time.Hour,
		Watch: func() <-chan clusterv2.RouteAuthorityEvent {
			mu.Lock()
			defer mu.Unlock()
			watchCalls++
			return make(chan clusterv2.RouteAuthorityEvent)
		},
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	defer worker.Stop(context.Background())
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	mu.Lock()
	got := watchCalls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("watch calls = %d, want 1 for idempotent Start", got)
	}
}

func TestPresenceTouchWorkerUpdatesAuthorityDirectoryFromEvents(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent, 3)
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Events:    events,
		Directory: directory,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Stop(context.Background())

	events <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	}, {
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   2,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	}, {
		HashSlot:       10,
		SlotID:         1,
		LeaderNodeID:   0,
		RouteRevision:  5,
		AuthorityEpoch: 4,
	}}}

	waitUntil(t, time.Second, func() bool {
		return len(directory.becomeSnapshot()) == 1 && len(directory.loseSnapshot()) == 2
	})
	if got := directory.becomeSnapshot()[0]; got.HashSlot != 9 || got.LeaderNodeID != 1 || got.AuthorityEpoch != 2 {
		t.Fatalf("become target = %#v, want hashSlot=9 leader=1 epoch=2", got)
	}
	if got := directory.loseSnapshot(); !reflect.DeepEqual(got, []uint16{9, 10}) {
		t.Fatalf("lost slots = %v, want [9 10]", got)
	}
}

func TestNewWiresBenchRuntimeControllerWhenClusterSupportsIt(t *testing.T) {
	app, err := newTestApp(t,
		Config{
			API:   APIConfig{ListenAddr: "127.0.0.1:0"},
			Bench: BenchConfig{APIEnabled: true},
		},
		WithCluster(&fakeRuntimeBenchCluster{}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)
	rec := httptest.NewRecorder()
	apiSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var caps struct {
		Supports struct {
			ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
			ChannelRuntimeProbe    bool `json:"channel_runtime_probe"`
			ChannelRuntimeEvict    bool `json:"channel_runtime_evict"`
		} `json:"supports"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !caps.Supports.ChannelRuntimeSnapshot {
		t.Fatalf("channel_runtime_snapshot = false, want true")
	}
	if !caps.Supports.ChannelRuntimeProbe {
		t.Fatalf("channel_runtime_probe = false, want true")
	}
	if !caps.Supports.ChannelRuntimeEvict {
		t.Fatalf("channel_runtime_evict = false, want true")
	}
}

func TestAppWiresLegacyChannelRoutesToClusterMetadata(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{}
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/info", strings.NewReader(`{"channel_id":"g1","channel_type":2,"ban":1,"allow_stranger":1}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(cluster.upserted) != 1 {
		t.Fatalf("upserted = %#v, want one channel", cluster.upserted)
	}
	got := cluster.upserted[0]
	if got.ChannelID != "g1" || got.ChannelType != int64(frame.ChannelTypeGroup) || got.Ban != 1 || got.AllowStranger != 1 {
		t.Fatalf("upserted channel = %#v, want mapped legacy channel metadata", got)
	}
}

func TestAppWiresConversationListRouteToUsecase(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", strings.NewReader(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"conversations":[]`) {
		t.Fatalf("body = %s, want empty conversation list", rec.Body.String())
	}
}

func TestAppWiresMessageSyncRouteToCMDSyncUsecase(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/sync", strings.NewReader(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "[]" {
		t.Fatalf("body = %s, want empty CMD sync array", rec.Body.String())
	}
}

func TestAppWiresConversationListMetrics(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		API:           APIConfig{ListenAddr: "127.0.0.1:0"},
		Observability: ObservabilityConfig{MetricsEnabled: true},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", strings.NewReader(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	families, err := app.metrics.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "wukongim_conversation_list_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["result"] == "ok" && labels["more"] == "false" && metric.GetCounter().GetValue() == 1 {
				return
			}
		}
	}
	t.Fatal("conversation list metric for successful request was not observed")
}

func TestGatewayStartFailureStopsAPIThenCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 5)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start,api.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start,api.stop,cluster.stop", got)
	}
}

func TestStartWaitsForClusterWriteReadinessBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
		routes: map[uint16]clusterv2.Route{
			0: {Leader: 1, Peers: []uint64{1}},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,cluster.route,cluster.probe,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,cluster.route,cluster.probe,gateway.start", got)
	}
}

func TestStartWaitsForClusterWriteProbeBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 6)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
		routes: map[uint16]clusterv2.Route{
			0: {Leader: 1, Peers: []uint64{1}},
		},
		probeErrors: []error{clusterv2.ErrNotLeader, nil},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,cluster.route,cluster.probe,cluster.route,cluster.probe,gateway.start" {
		t.Fatalf("calls = %s, want route and probe retry before gateway.start", got)
	}
}

func TestClusterWriteReadyScalesProbeTimeoutByPhysicalSlotCount(t *testing.T) {
	cluster := &fakeWriteReadyCluster{
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 10, HashSlotCount: 1},
		},
		routes: map[uint16]clusterv2.Route{
			0: {Leader: 1, Peers: []uint64{1}},
		},
	}
	var lastErr error

	if !clusterWriteReady(context.Background(), cluster, &lastErr) {
		t.Fatalf("clusterWriteReady() = false error=%v, want true", lastErr)
	}
	if len(cluster.probeTimeouts) != 1 {
		t.Fatalf("probe timeouts = %d, want 1", len(cluster.probeTimeouts))
	}
	if got := cluster.probeTimeouts[0]; got < 4*time.Second {
		t.Fatalf("probe timeout = %s, want budget scaled for 10 physical slots", got)
	}
}

func TestDefaultClusterWriteReadyTimeoutAllowsMultipleScaledWriteProbeAttempts(t *testing.T) {
	snapshot := clusterv2.Snapshot{SlotCount: 10}
	minimum := 3 * clusterWriteReadyProbeBudget(snapshot)

	if got := defaultClusterWriteReadyTimeout; got < minimum {
		t.Fatalf("default cluster write-ready timeout = %s, want at least %s for multiple scaled write-probe attempts", got, minimum)
	}
}

func TestClusterWriteReadinessFailureStopsClusterBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: false, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{Timeouts: clusterv2.TimeoutConfig{Start: time.Millisecond}}}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cluster write readiness") {
		t.Fatalf("Start() error = %v, want cluster write readiness error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,cluster.stop", got)
	}
}

func TestStopOrderIsGatewayThenCluster(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,gateway.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,gateway.stop,cluster.stop", got)
	}
}

func TestStopOrderIncludesAPIBeforeCluster(t *testing.T) {
	calls := make([]string, 0, 6)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start,gateway.stop,api.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start,gateway.stop,api.stop,cluster.stop", got)
	}
}

func TestConcurrentStartStopCannotLeaveGatewayRunningAfterStopReturns(t *testing.T) {
	cluster := newBlockingCluster()
	gateway := newStateGateway()
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- app.Start(context.Background())
	}()
	<-cluster.startEntered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- app.Stop(context.Background())
	}()
	time.Sleep(10 * time.Millisecond)

	close(cluster.releaseStart)

	if err := <-startDone; err != nil && !errors.Is(err, ErrStopped) {
		t.Fatalf("Start() error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if gateway.runningState() {
		t.Fatalf("gateway is running after Stop returned")
	}
}

func TestRollbackStopFailureLeavesClusterCleanupRetryPossible(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	rollbackErr := errors.New("cluster rollback failed")
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls, stopErr: rollbackErr}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("Start() error = %v, want gateway and rollback errors", err)
	}
	if err := app.Stop(context.Background()); !errors.Is(err, rollbackErr) {
		t.Fatalf("Stop() error = %v, want rollback retry error", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop,cluster.stop", got)
	}
}

func TestNewSeedsMessageIDsFromEffectiveClusterNodeID(t *testing.T) {
	cluster := newFakePresenceCluster(7, nil)
	cluster.snapshot = readyFakeClusterSnapshot(7, 16)
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{NodeID: 7}}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.Messages().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room-message-id",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-message-id-1",
		Payload:     []byte("seed"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}
	requireSnowflakeMessageIDNode(t, int64(result.MessageID), 7)
}

func requireSnowflakeMessageIDNode(t testing.TB, messageID int64, nodeID uint64) {
	t.Helper()
	if messageID <= 0 {
		t.Fatalf("message id = %d, want positive Snowflake id", messageID)
	}
	id := snowflake.ParseInt64(messageID)
	if got, want := id.Node(), int64(nodeID); got != want {
		t.Fatalf("message id node = %d, want %d from %d", got, want, messageID)
	}
}

func TestStaticMultiNodeClusterStartsControllerVoters(t *testing.T) {
	addrs := []string{freeSendackSmokeTCPAddr(t), freeSendackSmokeTCPAddr(t), freeSendackSmokeTCPAddr(t)}
	voters := []clusterv2.ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	apps := make([]*App, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{
			NodeID:  voter.NodeID,
			DataDir: t.TempDir(),
			Cluster: clusterv2.Config{
				NodeID:     voter.NodeID,
				ListenAddr: voter.Addr,
				DataDir:    t.TempDir(),
				Control: clusterv2.ControlConfig{
					ClusterID:      "internalv2-app-static-three",
					Voters:         voters,
					AllowBootstrap: true,
				},
				Slots: clusterv2.SlotConfig{
					InitialSlotCount: 1,
					HashSlotCount:    4,
					ReplicaCount:     3,
				},
				Channel:  clusterv2.ChannelConfig{TickInterval: time.Millisecond},
				Timeouts: clusterv2.TimeoutConfig{Start: 5 * time.Second},
			},
		}
		app, err := newTestApp(t, cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		apps = append(apps, app)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	errs := make(chan error, len(apps))
	for _, app := range apps {
		app := app
		go func() { errs <- app.Start(startCtx) }()
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = app.Stop(stopCtx)
		})
	}
	for range apps {
		if err := <-errs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	nodes := make([]*clusterv2.Node, 0, len(apps))
	for _, app := range apps {
		node, ok := app.cluster.(*clusterv2.Node)
		if !ok {
			t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
		}
		nodes = append(nodes, node)
	}
	waitAppClusterSnapshotsConverge(t, nodes)

	ack := sendDefaultMetaSmokePacket(t, apps[0], channelv2.ChannelID{ID: "room-static-three", Type: 1}, 1, "client-static-three-1")
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want %v", ack.ReasonCode, frame.ReasonSuccess)
	}
	if ack.MessageSeq != 1 {
		t.Fatalf("sendack message seq = %d, want 1", ack.MessageSeq)
	}
}

type fakeCluster struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeCluster) Start(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.start")
	}
	return f.startErr
}

func (f *fakeCluster) Stop(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.stop")
	}
	return f.stopErr
}

type fakeManagerCluster struct {
	fakeCluster
	nodeID       uint64
	snapshot     control.Snapshot
	channelPages map[uint32][]metadb.Channel
	userPages    map[uint32][]metadb.User
	devices      map[fakeManagerDeviceKey]metadb.Device
	systemUIDs   []string

	conversationPages         map[string][]metadb.ConversationState
	conversationMessages      map[metadb.ConversationKey][]channelv2.Message
	channelRuntimeMetas       map[metadb.ConversationKey]metadb.ChannelRuntimeMeta
	channelRetentionViews     map[metadb.ConversationKey]channelv2.RetentionView
	pluginBindingsByUID       map[string][]metadb.PluginUserBinding
	channelOwnerMetas         map[channelv2.ChannelID]channelv2.Meta
	registeredHandlers        map[uint8]clusterv2.NodeRPCHandler
	rpcNodeID                 uint64
	rpcServiceID              uint8
	controllerLogs            clusterv2.ControllerLogEntries
	slotLogs                  clusterv2.SlotLogEntries
	slotRaftStatus            clusterv2.SlotRaftStatus
	controllerRaftStatus      clusterv2.ControllerRaftStatus
	controllerRaftStatusErr   error
	controllerRaftStatuses    []clusterv2.ControllerRaftStatus
	controllerRaftStatusCalls int
	controllerRaftCompact     clusterv2.ControllerRaftCompactionResult
	slotRaftCompact           clusterv2.SlotRaftCompactionResult
	slotRaftCompactSlotID     uint32

	slotLeaderTransferRequest     control.SlotLeaderTransferRequest
	slotLeaderTransferResult      control.SlotLeaderTransferResult
	slotReplicaMoveRequest        control.SlotReplicaMoveRequest
	slotReplicaMoveResult         control.SlotReplicaMoveResult
	joinNodeRequest               control.JoinNodeRequest
	joinNodeResult                control.JoinNodeResult
	joinNodeErr                   error
	activateNodeRequest           control.ActivateNodeRequest
	activateNodeResult            control.ActivateNodeResult
	activateNodeErr               error
	markNodeLeavingRequest        control.MarkNodeLeavingRequest
	markNodeLeavingResult         control.MarkNodeLeavingResult
	markNodeLeavingErr            error
	markNodeRemovedRequest        control.MarkNodeRemovedRequest
	markNodeRemovedResult         control.MarkNodeRemovedResult
	markNodeRemovedErr            error
	promoteControllerVoterRequest control.PromoteControllerVoterRequest
	promoteControllerVoterResult  control.PromoteControllerVoterResult
	promoteControllerVoterErr     error
	retentionAdvance              metadb.ChannelRetentionAdvance
}

type fakeManagerDeviceKey struct {
	uid        string
	deviceFlag int64
}

func (f *fakeManagerCluster) NodeID() uint64 { return f.nodeID }

func (f *fakeManagerCluster) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	if f.registeredHandlers == nil {
		f.registeredHandlers = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registeredHandlers[serviceID] = handler
}

func (f *fakeManagerCluster) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.rpcNodeID = nodeID
	f.rpcServiceID = serviceID
	if (serviceID == accessnode.ManagerConnectionRPCServiceID || serviceID == accessnode.ManagerPluginRPCServiceID || serviceID == accessnode.ManagerMessageRetentionRPCServiceID) && f.registeredHandlers != nil {
		if handler, ok := f.registeredHandlers[serviceID]; ok {
			return handler.HandleRPC(ctx, payload)
		}
	}
	return nil, errors.New("unexpected manager rpc call")
}

func (f *fakeManagerCluster) LocalControllerLogEntries(context.Context, clusterv2.LogEntriesOptions) (clusterv2.ControllerLogEntries, error) {
	return f.controllerLogs, nil
}

func (f *fakeManagerCluster) LocalSlotLogEntries(context.Context, uint32, clusterv2.LogEntriesOptions) (clusterv2.SlotLogEntries, error) {
	return f.slotLogs, nil
}

func (f *fakeManagerCluster) LocalSlotRaftStatus(context.Context, uint32) (clusterv2.SlotRaftStatus, error) {
	return f.slotRaftStatus, nil
}

func (f *fakeManagerCluster) LocalControllerRaftStatus(context.Context) (clusterv2.ControllerRaftStatus, error) {
	if f.controllerRaftStatusErr != nil {
		return clusterv2.ControllerRaftStatus{}, f.controllerRaftStatusErr
	}
	f.controllerRaftStatusCalls++
	if len(f.controllerRaftStatuses) > 0 {
		index := f.controllerRaftStatusCalls - 1
		if index >= len(f.controllerRaftStatuses) {
			index = len(f.controllerRaftStatuses) - 1
		}
		return f.controllerRaftStatuses[index], nil
	}
	return f.controllerRaftStatus, nil
}

func (f *fakeManagerCluster) LocalCompactControllerRaftLog(context.Context) (clusterv2.ControllerRaftCompactionResult, error) {
	return f.controllerRaftCompact, nil
}

func (f *fakeManagerCluster) LocalCompactSlotRaftLog(_ context.Context, slotID uint32) (clusterv2.SlotRaftCompactionResult, error) {
	f.slotRaftCompactSlotID = slotID
	return f.slotRaftCompact, nil
}

func (f *fakeManagerCluster) RequestSlotLeaderTransfer(_ context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	f.slotLeaderTransferRequest = req
	return f.slotLeaderTransferResult, nil
}

func (f *fakeManagerCluster) RequestSlotReplicaMove(_ context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	f.slotReplicaMoveRequest = req
	return f.slotReplicaMoveResult, nil
}

func (f *fakeManagerCluster) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	f.joinNodeRequest = req
	return f.joinNodeResult, f.joinNodeErr
}

func (f *fakeManagerCluster) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	f.activateNodeRequest = req
	return f.activateNodeResult, f.activateNodeErr
}

func (f *fakeManagerCluster) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	f.markNodeLeavingRequest = req
	return f.markNodeLeavingResult, f.markNodeLeavingErr
}

func (f *fakeManagerCluster) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	f.markNodeRemovedRequest = req
	return f.markNodeRemovedResult, f.markNodeRemovedErr
}

func (f *fakeManagerCluster) PromoteControllerVoter(_ context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	f.promoteControllerVoterRequest = req
	return f.promoteControllerVoterResult, f.promoteControllerVoterErr
}

func (f *fakeManagerCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot.Clone(), nil
}

func (f *fakeManagerCluster) ScanChannelsSlotPage(_ context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	items := f.channelPages[slotID]
	start := 0
	if after.ChannelID != "" {
		for i, item := range items {
			if item.ChannelID == after.ChannelID && item.ChannelType == after.ChannelType {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]metadb.Channel(nil), items[start:end]...)
	cursor := after
	if len(page) > 0 {
		last := page[len(page)-1]
		cursor = metadb.ChannelCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return page, cursor, end >= len(items), nil
}

func (f *fakeManagerCluster) ScanUsersSlotPage(_ context.Context, slotID uint32, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
	items := f.userPages[slotID]
	start := 0
	if after.UID != "" {
		for i, item := range items {
			if item.UID == after.UID {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]metadb.User(nil), items[start:end]...)
	cursor := after
	if len(page) > 0 {
		cursor = metadb.UserCursor{UID: page[len(page)-1].UID}
	}
	return page, cursor, end >= len(items), nil
}

func (f *fakeManagerCluster) CreateUserMetadata(_ context.Context, user metadb.User) error {
	f.userPages[1] = append(f.userPages[1], user)
	return nil
}

func (f *fakeManagerCluster) GetUserMetadata(_ context.Context, uid string) (metadb.User, error) {
	for _, page := range f.userPages {
		for _, user := range page {
			if user.UID == uid {
				return user, nil
			}
		}
	}
	return metadb.User{}, metadb.ErrNotFound
}

func (f *fakeManagerCluster) UpsertDeviceMetadata(_ context.Context, device metadb.Device) error {
	if f.devices == nil {
		f.devices = make(map[fakeManagerDeviceKey]metadb.Device)
	}
	f.devices[fakeManagerDeviceKey{uid: device.UID, deviceFlag: device.DeviceFlag}] = device
	return nil
}

func (f *fakeManagerCluster) GetDeviceMetadata(_ context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	device, ok := f.devices[fakeManagerDeviceKey{uid: uid, deviceFlag: deviceFlag}]
	if !ok {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return device, nil
}

func (f *fakeManagerCluster) GetChannelMetadata(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	for _, page := range f.channelPages {
		for _, channel := range page {
			if channel.ChannelID == channelID && channel.ChannelType == channelType {
				return channel, nil
			}
		}
	}
	return metadb.Channel{}, metadb.ErrNotFound
}

func (f *fakeManagerCluster) UpsertChannelMetadata(_ context.Context, channel metadb.Channel) error {
	f.channelPages[1] = append(f.channelPages[1], channel)
	return nil
}

func (f *fakeManagerCluster) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (f *fakeManagerCluster) AddChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, _ uint64) error {
	for _, uid := range uids {
		found := false
		for _, existing := range f.systemUIDs {
			if existing == uid {
				found = true
				break
			}
		}
		if !found {
			f.systemUIDs = append(f.systemUIDs, uid)
		}
	}
	return nil
}

func (f *fakeManagerCluster) RemoveChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, _ uint64) error {
	remove := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		remove[uid] = struct{}{}
	}
	kept := f.systemUIDs[:0]
	for _, uid := range f.systemUIDs {
		if _, ok := remove[uid]; !ok {
			kept = append(kept, uid)
		}
	}
	f.systemUIDs = kept
	return nil
}

func (f *fakeManagerCluster) ListChannelSubscribersPage(_ context.Context, _ string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	start := 0
	if afterUID != "" {
		for i, uid := range f.systemUIDs {
			if uid == afterUID {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = len(f.systemUIDs)
	}
	end := start + limit
	if end > len(f.systemUIDs) {
		end = len(f.systemUIDs)
	}
	page := append([]string(nil), f.systemUIDs[start:end]...)
	next := afterUID
	if len(page) > 0 {
		next = page[len(page)-1]
	}
	return page, next, end >= len(f.systemUIDs), nil
}

func (f *fakeManagerCluster) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	items := f.conversationPages[uid]
	filtered := make([]metadb.ConversationState, 0, len(items))
	for _, item := range items {
		if item.Kind == kind {
			filtered = append(filtered, item)
		}
	}
	items = filtered
	start := 0
	if after.ChannelID != "" {
		for i, item := range items {
			if item.ChannelID == after.ChannelID && item.ChannelType == after.ChannelType && item.ActiveAt == after.ActiveAt {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]metadb.ConversationState(nil), items[start:end]...)
	cursor := after
	if len(page) > 0 {
		last := page[len(page)-1]
		cursor = metadb.ConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return page, cursor, end >= len(items), nil
}

func (f *fakeManagerCluster) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	for _, state := range f.conversationPages[uid] {
		if state.Kind == kind && state.ChannelID == channelID && state.ChannelType == channelType {
			return state, true, nil
		}
	}
	return metadb.ConversationState{}, false, nil
}

func (f *fakeManagerCluster) GetConversationStates(_ context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		for _, state := range f.conversationPages[key.UID] {
			if state.Kind == key.Kind && state.ChannelID == key.ChannelID && state.ChannelType == key.ChannelType {
				states[key] = state
				break
			}
		}
	}
	return states, nil
}

func (f *fakeManagerCluster) ListPluginBindingsByUID(_ context.Context, uid string) ([]metadb.PluginUserBinding, error) {
	return append([]metadb.PluginUserBinding(nil), f.pluginBindingsByUID[uid]...), nil
}

func (f *fakeManagerCluster) ListPluginBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error) {
	items := make([]metadb.PluginUserBinding, 0)
	for _, bindings := range f.pluginBindingsByUID {
		for _, binding := range bindings {
			if binding.PluginNo == pluginNo {
				items = append(items, binding)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UID < items[j].UID })
	start := 0
	if cursor != "" {
		for i, item := range items {
			if item.UID == cursor {
				start = i + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]metadb.PluginUserBinding(nil), items[start:end]...)
	next := ""
	if end < len(items) && len(page) > 0 {
		next = page[len(page)-1].UID
	}
	return page, next, end < len(items), nil
}

func (f *fakeManagerCluster) BindPluginUser(_ context.Context, binding metadb.PluginUserBinding) error {
	if f.pluginBindingsByUID == nil {
		f.pluginBindingsByUID = make(map[string][]metadb.PluginUserBinding)
	}
	bindings := f.pluginBindingsByUID[binding.UID]
	for i, existing := range bindings {
		if existing.PluginNo == binding.PluginNo {
			bindings[i] = binding
			f.pluginBindingsByUID[binding.UID] = bindings
			return nil
		}
	}
	f.pluginBindingsByUID[binding.UID] = append(bindings, binding)
	return nil
}

func (f *fakeManagerCluster) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	bindings := f.pluginBindingsByUID[uid]
	kept := bindings[:0]
	for _, binding := range bindings {
		if binding.PluginNo != pluginNo {
			kept = append(kept, binding)
		}
	}
	if len(kept) == 0 {
		delete(f.pluginBindingsByUID, uid)
		return nil
	}
	f.pluginBindingsByUID[uid] = kept
	return nil
}

func (f *fakeManagerCluster) ReadChannelLastVisible(_ context.Context, channelID channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	key := metadb.ConversationKey{ChannelID: channelID.ID, ChannelType: int64(channelID.Type)}
	messages := f.conversationMessages[key]
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].MessageSeq > visibleAfterSeq {
			return messages[i], true, nil
		}
	}
	return channelv2.Message{}, false, nil
}

func (f *fakeManagerCluster) ReadChannelCommitted(_ context.Context, channelID channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	key := metadb.ConversationKey{ChannelID: channelID.ID, ChannelType: int64(channelID.Type)}
	source := f.conversationMessages[key]
	messages := make([]channelv2.Message, 0, len(source))
	for _, message := range source {
		if req.MinSeq > 0 && message.MessageSeq < req.MinSeq {
			continue
		}
		if req.MaxSeq > 0 && message.MessageSeq > req.MaxSeq {
			continue
		}
		if !req.Reverse && req.FromSeq > 0 && message.MessageSeq < req.FromSeq {
			continue
		}
		if req.Reverse && req.FromSeq > 0 && message.MessageSeq > req.FromSeq {
			continue
		}
		messages = append(messages, message)
	}
	if req.Reverse {
		for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
			messages[left], messages[right] = messages[right], messages[left]
		}
	}
	if req.Limit > 0 && len(messages) > req.Limit {
		messages = messages[:req.Limit]
	}
	return channelstore.ReadCommittedResult{Messages: messages}, nil
}

func (f *fakeManagerCluster) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	meta, ok := f.channelRuntimeMetas[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func (f *fakeManagerCluster) ChannelRetentionView(_ context.Context, id channelv2.ChannelID) (channelv2.RetentionView, error) {
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	if view, ok := f.channelRetentionViews[key]; ok {
		return view, nil
	}
	maxSeq := uint64(0)
	for _, message := range f.conversationMessages[key] {
		if message.MessageSeq > maxSeq {
			maxSeq = message.MessageSeq
		}
	}
	if maxSeq == 0 {
		maxSeq = ^uint64(0)
	}
	return channelv2.RetentionView{HW: maxSeq, CheckpointHW: maxSeq, MinISRMatchOffset: maxSeq}, nil
}

func (f *fakeManagerCluster) AdvanceChannelRetentionThroughSeq(_ context.Context, req metadb.ChannelRetentionAdvance) error {
	f.retentionAdvance = req
	key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
	meta := f.channelRuntimeMetas[key]
	meta.RetentionThroughSeq = req.RetentionThroughSeq
	meta.RetentionUpdatedAtMS = req.RetentionUpdatedAtMS
	f.channelRuntimeMetas[key] = meta
	return nil
}

func (f *fakeManagerCluster) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	if f.channelOwnerMetas == nil {
		return channelv2.Meta{}, nil
	}
	return f.channelOwnerMetas[id], nil
}

var _ clusterinfra.ChannelRuntimeBenchNode = (*fakeRuntimeBenchCluster)(nil)

type fakeRuntimeBenchCluster struct {
	fakeCluster
}

func (f *fakeRuntimeBenchCluster) NodeID() uint64 {
	return 1
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error) {
	return channelv2.RuntimeSnapshot{NodeID: 1}, nil
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	return channelv2.RuntimeProbeResult{}, nil
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	return channelv2.RuntimeEvictResult{}, nil
}

type fakeWriteReadyCluster struct {
	fakeCluster
	snapshots     []clusterv2.Snapshot
	routes        map[uint16]clusterv2.Route
	probeErrors   []error
	probeTimeouts []time.Duration
}

func (f *fakeWriteReadyCluster) Snapshot() clusterv2.Snapshot {
	if len(f.snapshots) == 0 {
		return clusterv2.Snapshot{}
	}
	return f.snapshots[0]
}

func (f *fakeWriteReadyCluster) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.route")
	}
	route, ok := f.routes[hashSlot]
	if !ok {
		return clusterv2.Route{}, clusterv2.ErrRouteNotReady
	}
	return route, nil
}

func (f *fakeWriteReadyCluster) ProbeWriteReady(ctx context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.probe")
	}
	if deadline, ok := ctx.Deadline(); ok {
		f.probeTimeouts = append(f.probeTimeouts, time.Until(deadline))
	}
	if len(f.probeErrors) == 0 {
		return nil
	}
	err := f.probeErrors[0]
	f.probeErrors = f.probeErrors[1:]
	return err
}

type fakeSeedJoinWriteReadyCluster struct {
	fakeWriteReadyCluster
	nodeID          uint64
	controlSnapshot control.Snapshot
}

func (f *fakeSeedJoinWriteReadyCluster) NodeID() uint64 {
	return f.nodeID
}

func (f *fakeSeedJoinWriteReadyCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.controlSnapshot, nil
}

func (f *fakeSeedJoinWriteReadyCluster) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, nil
}

type fakePresenceCluster struct {
	fakeCluster
	nodeID                    uint64
	events                    <-chan clusterv2.RouteAuthorityEvent
	snapshot                  clusterv2.Snapshot
	registeredService         uint8
	registeredHandler         clusterv2.NodeRPCHandler
	registeredHandlers        map[uint8]clusterv2.NodeRPCHandler
	appendSeq                 uint64
	mu                        sync.Mutex
	idempotencyHit            channelstore.IdempotencyHit
	idempotencyOK             bool
	idempotencyErr            error
	idempotencyLookups        int
	messages                  map[metadb.ConversationKey][]channelv2.Message
	conversationStateBatches  [][]metadb.ConversationState
	conversationDeleteBatches [][]metadb.ConversationDelete
	conversationPatchBatches  [][]metadb.ConversationActivePatch
	subscribers               map[string][]string
	channels                  map[metadb.ConversationKey]metadb.Channel
}

type fakeConversationFallbackCluster struct {
	fakeCluster
	appendSeq                uint64
	mu                       sync.Mutex
	messages                 map[metadb.ConversationKey][]channelv2.Message
	conversationStateBatches [][]metadb.ConversationState
	subscribers              map[string][]string
	channels                 map[metadb.ConversationKey]metadb.Channel
}

type recordingDeliveryMetaNode struct {
	fakeCluster
	mu                sync.Mutex
	snapshot          clusterv2.Snapshot
	upserted          []metadb.Channel
	added             []recordedSubscriberMutation
	membershipUpserts []recordedMembershipProjection
	membershipDeletes []recordedMembershipProjection
	subscribers       map[string][]string
	listCalls         int
}

type recordedSubscriberMutation struct {
	channelID   string
	channelType int64
	uids        []string
	version     uint64
}

type recordedMembershipProjection struct {
	channelID   string
	channelType int64
	uids        []string
	joinSeq     uint64
	updatedAt   int64
}

func newFakePresenceCluster(nodeID uint64, events <-chan clusterv2.RouteAuthorityEvent) *fakePresenceCluster {
	return &fakePresenceCluster{nodeID: nodeID, events: events}
}

func conversationStatesByUID(states []metadb.ConversationState) map[string]metadb.ConversationState {
	out := make(map[string]metadb.ConversationState, len(states))
	for _, state := range states {
		out[state.UID] = state
	}
	return out
}

func conversationPatchesByUID(patches []metadb.ConversationActivePatch) map[string]metadb.ConversationActivePatch {
	out := make(map[string]metadb.ConversationActivePatch, len(patches))
	for _, patch := range patches {
		out[patch.UID] = patch
	}
	return out
}

type appRecordingConversationAuthorityStore struct {
	mu                 sync.Mutex
	touchStarted       chan struct{}
	touchStartedOnce   sync.Once
	touchBlock         <-chan struct{}
	touchBatches       [][]metadb.ConversationActivePatch
	rows               map[metadb.ConversationKey]metadb.ConversationState
	lastTouchDeadlineV time.Time
}

func (s *appRecordingConversationAuthorityStore) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]metadb.ConversationState, 0, len(s.rows))
	for _, row := range s.rows {
		if row.UID == uid && row.Kind == kind && conversationRowAfter(row, after) {
			rows = append(rows, row)
		}
	}
	sortConversationRows(rows)
	if limit <= 0 || len(rows) <= limit {
		return append([]metadb.ConversationState(nil), rows...), conversationRowsCursor(rows, after), true, nil
	}
	page := append([]metadb.ConversationState(nil), rows[:limit]...)
	return page, conversationRowsCursor(page, after), false, nil
}

func (s *appRecordingConversationAuthorityStore) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok || row.UID != uid || row.Kind != kind {
		return metadb.ConversationState{}, false, nil
	}
	return row, true, nil
}

func (s *appRecordingConversationAuthorityStore) GetConversationStates(_ context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		row, ok := s.rows[metadb.ConversationKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}]
		if !ok || row.UID != key.UID || row.Kind != key.Kind {
			continue
		}
		states[key] = row
	}
	return states, nil
}

func (s *appRecordingConversationAuthorityStore) TouchConversationActiveAtBatch(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	if s.touchStarted != nil {
		s.touchStartedOnce.Do(func() {
			close(s.touchStarted)
		})
	}
	if s.touchBlock != nil {
		select {
		case <-s.touchBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		s.lastTouchDeadlineV = deadline
	}
	s.touchBatches = append(s.touchBatches, append([]metadb.ConversationActivePatch(nil), patches...))
	if s.rows == nil {
		s.rows = make(map[metadb.ConversationKey]metadb.ConversationState)
	}
	for _, patch := range patches {
		key := metadb.ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}
		row := s.rows[key]
		row.UID = patch.UID
		row.Kind = patch.Kind
		row.ChannelID = patch.ChannelID
		row.ChannelType = patch.ChannelType
		if patch.ReadSeq > row.ReadSeq {
			row.ReadSeq = patch.ReadSeq
		}
		if patch.DeletedToSeq > row.DeletedToSeq {
			row.DeletedToSeq = patch.DeletedToSeq
		}
		if patch.ActiveAt > row.ActiveAt {
			row.ActiveAt = patch.ActiveAt
		}
		if patch.UpdatedAt > row.UpdatedAt {
			row.UpdatedAt = patch.UpdatedAt
		}
		if patch.SparseActiveSet {
			row.SparseActive = patch.SparseActive
		}
		s.rows[key] = row
	}
	return nil
}

func (s *appRecordingConversationAuthorityStore) UpsertConversationStatesBatch(ctx context.Context, states []metadb.ConversationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.rows == nil {
		s.rows = make(map[metadb.ConversationKey]metadb.ConversationState)
	}
	for _, state := range states {
		key := metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
		if existing, ok := s.rows[key]; ok && existing.UID == state.UID {
			state = mergeConversationState(existing, state)
		}
		s.rows[key] = state
	}
	return nil
}

func (s *appRecordingConversationAuthorityStore) totalTouchPatches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, batch := range s.touchBatches {
		total += len(batch)
	}
	return total
}

func (s *appRecordingConversationAuthorityStore) lastTouchDeadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTouchDeadlineV
}

func (s *appRecordingConversationAuthorityStore) lastTouchDeadlineWithin(timeout, slop time.Duration) bool {
	deadline := s.lastTouchDeadline()
	if deadline.IsZero() {
		return false
	}
	remaining := time.Until(deadline)
	return remaining > 0 && remaining <= timeout+slop
}

type recordingConversationAuthorityRouteNode struct {
	nodeID  uint64
	routes  map[string]clusterv2.Route
	watch   <-chan clusterv2.RouteAuthorityEvent
	handler clusterv2.NodeRPCHandler
}

func (n *recordingConversationAuthorityRouteNode) NodeID() uint64 {
	return n.nodeID
}

func (n *recordingConversationAuthorityRouteNode) RouteKey(uid string) (clusterv2.Route, error) {
	route, ok := n.routes[uid]
	if !ok {
		return clusterv2.Route{}, clusterv2.ErrRouteNotReady
	}
	return route, nil
}

func (n *recordingConversationAuthorityRouteNode) CallRPC(ctx context.Context, _ uint64, _ uint8, payload []byte) ([]byte, error) {
	if n.handler != nil {
		return n.handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("unexpected conversation authority rpc")
}

func (n *recordingConversationAuthorityRouteNode) RegisterRPC(uint8, clusterv2.NodeRPCHandler) {}

func (n *recordingConversationAuthorityRouteNode) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	return n.watch
}

type appNodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f appNodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

func routeFromConversationTarget(target conversationusecase.RouteTarget) clusterv2.Route {
	return clusterv2.Route{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		Leader:         target.LeaderNodeID,
		LeaderTerm:     target.LeaderTerm,
		ConfigEpoch:    target.ConfigEpoch,
		Revision:       target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}

func authorityFromConversationTarget(target conversationusecase.RouteTarget) clusterv2.RouteAuthority {
	return clusterv2.RouteAuthority{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		LeaderNodeID:   target.LeaderNodeID,
		LeaderTerm:     target.LeaderTerm,
		ConfigEpoch:    target.ConfigEpoch,
		RouteRevision:  target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}

func readyFakeClusterSnapshot(nodeID uint64, hashSlotCount uint16) clusterv2.Snapshot {
	return clusterv2.Snapshot{
		NodeID:        nodeID,
		RoutesReady:   true,
		SlotsReady:    true,
		ChannelsReady: true,
		SlotCount:     1,
		HashSlotCount: hashSlotCount,
	}
}

func fakeChannelAuthorityMeta(nodeID uint64, id channelv2.ChannelID) channelv2.Meta {
	leader := channelv2.NodeID(nodeID)
	return channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      leader,
		Replicas:    []channelv2.NodeID{leader},
		ISR:         []channelv2.NodeID{leader},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}
}

func testUIDForHashSlot(t *testing.T, want, count uint16) string {
	t.Helper()
	for i := 0; i < 100000; i++ {
		uid := fmt.Sprintf("bench-u-%d", i)
		if routing.HashSlotForKey(uid, count) == want {
			return uid
		}
	}
	t.Fatalf("no uid found for hash slot %d/%d", want, count)
	return ""
}

func (n *recordingDeliveryMetaNode) Snapshot() clusterv2.Snapshot {
	return n.snapshot
}

func (n *recordingDeliveryMetaNode) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: hashSlot, SlotID: 1, Leader: 1, Revision: 1, AuthorityEpoch: 1}, nil
}

func (n *recordingDeliveryMetaNode) UpsertChannelMetadata(_ context.Context, channel metadb.Channel) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.upserted = append(n.upserted, channel)
	return nil
}

func (n *recordingDeliveryMetaNode) GetChannelMetadata(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.upserted) - 1; i >= 0; i-- {
		ch := n.upserted[i]
		if ch.ChannelID == channelID && ch.ChannelType == channelType {
			return ch, nil
		}
	}
	return metadb.Channel{}, metadb.ErrNotFound
}

func (n *recordingDeliveryMetaNode) DeleteChannelMetadata(_ context.Context, channelID string, channelType int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	filtered := n.upserted[:0]
	for _, ch := range n.upserted {
		if ch.ChannelID != channelID || ch.ChannelType != channelType {
			filtered = append(filtered, ch)
		}
	}
	n.upserted = filtered
	delete(n.subscribers, channelID)
	return nil
}

func (n *recordingDeliveryMetaNode) AddChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, version uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.added = append(n.added, recordedSubscriberMutation{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		version:     version,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) RemoveChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, version uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.added = append(n.added, recordedSubscriberMutation{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		version:     version,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	n.mu.Lock()
	n.listCalls++
	uids := append([]string(nil), n.subscribers[channelID]...)
	n.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (n *recordingDeliveryMetaNode) UpsertUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.membershipUpserts = append(n.membershipUpserts, recordedMembershipProjection{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		joinSeq:     joinSeq,
		updatedAt:   updatedAt,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) DeleteUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.membershipDeletes = append(n.membershipDeletes, recordedMembershipProjection{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		updatedAt:   updatedAt,
	})
	return nil
}

func (f *fakePresenceCluster) NodeID() uint64 {
	return f.nodeID
}

func (f *fakePresenceCluster) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	return fakeChannelAuthorityMeta(f.nodeID, id), nil
}

func (f *fakePresenceCluster) GetChannelMetadata(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	channel, ok := f.channels[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return channel, nil
}

func (f *fakePresenceCluster) RouteKey(uid string) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: 9, SlotID: 1, Leader: f.nodeID, Revision: 3, AuthorityEpoch: 2}, nil
}

func (f *fakePresenceCluster) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: hashSlot, SlotID: 1, Leader: f.nodeID, Revision: 3, AuthorityEpoch: 2}, nil
}

func (f *fakePresenceCluster) Snapshot() clusterv2.Snapshot {
	return f.snapshot
}

func (f *fakePresenceCluster) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected presence rpc call")
}

func (f *fakePresenceCluster) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	f.registeredService = serviceID
	f.registeredHandler = handler
	if f.registeredHandlers == nil {
		f.registeredHandlers = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registeredHandlers[serviceID] = handler
}

func (f *fakePresenceCluster) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	fakeItems := make([]channelv2.AppendBatchItemResult, 0, len(req.Messages))
	for _, msg := range req.Messages {
		f.appendSeq++
		msg.MessageSeq = f.appendSeq
		msg.Payload = append([]byte(nil), msg.Payload...)
		fakeItems = append(fakeItems, channelv2.AppendBatchItemResult{
			MessageID:  msg.MessageID,
			MessageSeq: msg.MessageSeq,
			Message:    msg,
		})
	}
	return channelv2.AppendBatchResult{Items: fakeItems}, nil
}

func (f *fakePresenceCluster) LookupChannelIdempotency(_ context.Context, _ channelv2.ChannelID, _ string, _ string) (channelstore.IdempotencyHit, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idempotencyLookups++
	return f.idempotencyHit, f.idempotencyOK, f.idempotencyErr
}

func (f *fakePresenceCluster) UpsertConversationStatesBatch(_ context.Context, states []metadb.ConversationState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.ConversationState(nil), states...)
	f.conversationStateBatches = append(f.conversationStateBatches, batch)
	return nil
}

func (f *fakePresenceCluster) HideConversationsBatch(_ context.Context, deletes []metadb.ConversationDelete) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.ConversationDelete(nil), deletes...)
	f.conversationDeleteBatches = append(f.conversationDeleteBatches, batch)
	return nil
}

func (f *fakePresenceCluster) TouchConversationActiveAtBatch(_ context.Context, patches []metadb.ConversationActivePatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.ConversationActivePatch(nil), patches...)
	f.conversationPatchBatches = append(f.conversationPatchBatches, batch)
	return nil
}

func (f *fakePresenceCluster) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.mu.Lock()
	uids := append([]string(nil), f.subscribers[channelID]...)
	f.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (f *fakePresenceCluster) ListUserChannelMembershipPage(_ context.Context, _ string, _ metadb.UserChannelMembershipCursor, _ int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	return nil, metadb.UserChannelMembershipCursor{}, true, nil
}

func (f *fakePresenceCluster) ListConversationActivePage(_ context.Context, _ metadb.ConversationKind, _ string, _ metadb.ConversationActiveCursor, _ int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	return nil, metadb.ConversationActiveCursor{}, true, nil
}

func (f *fakePresenceCluster) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.conversationStateBatches) - 1; i >= 0; i-- {
		for _, state := range f.conversationStateBatches[i] {
			if state.UID == uid && state.Kind == kind && state.ChannelID == channelID && state.ChannelType == channelType {
				return state, true, nil
			}
		}
	}
	return metadb.ConversationState{}, false, nil
}

func (f *fakePresenceCluster) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		state, ok, err := f.GetConversationState(ctx, key.Kind, key.UID, key.ChannelID, key.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			states[key] = state
		}
	}
	return states, nil
}

func (f *fakePresenceCluster) ReadChannelLastVisible(_ context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	messages := f.messages[metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}]
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.MessageSeq <= visibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		return msg, true, nil
	}
	return channelv2.Message{}, false, nil
}

func (f *fakePresenceCluster) ReadChannelCommitted(context.Context, channelv2.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	return channelstore.ReadCommittedResult{}, nil
}

func (f *fakePresenceCluster) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	if f.calls != nil {
		*f.calls = append(*f.calls, "presence.start")
	}
	if f.events != nil {
		return f.events
	}
	ch := make(chan clusterv2.RouteAuthorityEvent)
	return ch
}

func (f *fakeConversationFallbackCluster) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.messages == nil {
		f.messages = make(map[metadb.ConversationKey][]channelv2.Message)
	}
	key := metadb.ConversationKey{ChannelID: req.ChannelID.ID, ChannelType: int64(req.ChannelID.Type)}
	items := make([]channelv2.AppendBatchItemResult, 0, len(req.Messages))
	for _, msg := range req.Messages {
		f.appendSeq++
		msg.MessageSeq = f.appendSeq
		msg.Payload = append([]byte(nil), msg.Payload...)
		f.messages[key] = append(f.messages[key], msg)
		items = append(items, channelv2.AppendBatchItemResult{
			MessageID:  msg.MessageID,
			MessageSeq: msg.MessageSeq,
			Message:    msg,
		})
	}
	return channelv2.AppendBatchResult{Items: items}, nil
}

func (f *fakeConversationFallbackCluster) NodeID() uint64 {
	return 3
}

func (f *fakeConversationFallbackCluster) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	return fakeChannelAuthorityMeta(3, id), nil
}

func (f *fakeConversationFallbackCluster) GetChannelMetadata(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	channel, ok := f.channels[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return channel, nil
}

func (f *fakeConversationFallbackCluster) UpsertConversationStatesBatch(_ context.Context, states []metadb.ConversationState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.ConversationState(nil), states...)
	f.conversationStateBatches = append(f.conversationStateBatches, batch)
	return nil
}

func (f *fakeConversationFallbackCluster) TouchConversationActiveAtBatch(context.Context, []metadb.ConversationActivePatch) error {
	return errors.New("unexpected authority active patch fallback write")
}

func (f *fakeConversationFallbackCluster) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.mu.Lock()
	uids := append([]string(nil), f.subscribers[channelID]...)
	f.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (f *fakeConversationFallbackCluster) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		return nil, metadb.ConversationActiveCursor{}, true, nil
	}
	latest := make(map[metadb.ConversationKey]metadb.ConversationState)
	for _, batch := range f.conversationStateBatches {
		for _, state := range batch {
			if state.UID != uid || state.Kind != kind {
				continue
			}
			key := metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
			existing, ok := latest[key]
			if !ok {
				latest[key] = state
				continue
			}
			latest[key] = mergeConversationState(existing, state)
		}
	}
	rows := make([]metadb.ConversationState, 0, len(latest))
	for _, state := range latest {
		if conversationRowAfter(state, after) {
			rows = append(rows, state)
		}
	}
	sortConversationRows(rows)
	if len(rows) <= limit {
		return rows, conversationRowsCursor(rows, after), true, nil
	}
	page := append([]metadb.ConversationState(nil), rows[:limit]...)
	return page, conversationRowsCursor(page, after), false, nil
}

func (f *fakeConversationFallbackCluster) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out metadb.ConversationState
	found := false
	for _, batch := range f.conversationStateBatches {
		for _, state := range batch {
			if state.UID != uid || state.Kind != kind || state.ChannelID != channelID || state.ChannelType != channelType {
				continue
			}
			if found {
				out = mergeConversationState(out, state)
			} else {
				out = state
				found = true
			}
		}
	}
	return out, found, nil
}

func (f *fakeConversationFallbackCluster) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		state, ok, err := f.GetConversationState(ctx, key.Kind, key.UID, key.ChannelID, key.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			states[key] = state
		}
	}
	return states, nil
}

func (f *fakeConversationFallbackCluster) ReadChannelLastVisible(_ context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	messages := f.messages[key]
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.MessageSeq <= visibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		return msg, true, nil
	}
	return channelv2.Message{}, false, nil
}

func (f *fakeConversationFallbackCluster) ReadChannelCommitted(_ context.Context, id channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	messages := f.messages[key]
	out := make([]channelv2.Message, 0, req.Limit)
	limit := req.Limit
	if limit <= 0 {
		limit = len(messages)
	}
	maxSeq := req.MaxSeq
	if maxSeq == 0 {
		maxSeq = ^uint64(0)
	}
	if req.Reverse {
		fromSeq := req.FromSeq
		if fromSeq == 0 || fromSeq > uint64(len(messages)) {
			fromSeq = uint64(len(messages))
		}
		for seq := fromSeq; seq >= 1 && seq <= uint64(len(messages)); seq-- {
			msg := messages[seq-1]
			if msg.MessageSeq > maxSeq {
				continue
			}
			out = append(out, cloneChannelV2Message(msg))
			if len(out) >= limit || seq == 1 {
				break
			}
		}
		return channelstore.ReadCommittedResult{Messages: out}, nil
	}
	fromSeq := req.FromSeq
	if fromSeq == 0 {
		fromSeq = 1
	}
	for seq := fromSeq; seq <= maxSeq && seq <= uint64(len(messages)); seq++ {
		out = append(out, cloneChannelV2Message(messages[seq-1]))
		if len(out) >= limit {
			break
		}
	}
	return channelstore.ReadCommittedResult{Messages: out}, nil
}

func cloneChannelV2Message(msg channelv2.Message) channelv2.Message {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

type touchBatch struct {
	target presence.RouteTarget
	routes []presence.Route
}

type recordingPresenceDirectory struct {
	mu      sync.Mutex
	become  []presence.RouteTarget
	lose    []uint16
	expires []expireCall
}

type expireCall struct {
	now time.Time
	ttl time.Duration
}

func (r *recordingPresenceDirectory) BecomeAuthority(target presence.RouteTarget) {
	r.mu.Lock()
	r.become = append(r.become, target)
	r.mu.Unlock()
}

func (r *recordingPresenceDirectory) LoseAuthority(hashSlot uint16) {
	r.mu.Lock()
	r.lose = append(r.lose, hashSlot)
	r.mu.Unlock()
}

func (r *recordingPresenceDirectory) ExpireRoutes(now time.Time, ttl time.Duration) int {
	r.mu.Lock()
	r.expires = append(r.expires, expireCall{now: now, ttl: ttl})
	r.mu.Unlock()
	return 0
}

func (r *recordingPresenceDirectory) becomeSnapshot() []presence.RouteTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]presence.RouteTarget(nil), r.become...)
}

func (r *recordingPresenceDirectory) loseSnapshot() []uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint16(nil), r.lose...)
}

type recordingTouchAuthority struct {
	mu          sync.Mutex
	targets     map[string]presence.RouteTarget
	batches     []touchBatch
	err         error
	resolveHook func(string)
}

type recordingSessionHandle struct {
	reason   string
	writeErr error
	writes   []any
}

type fakeDeliverySubscriberSource struct {
	requests []runtimedelivery.SubscriberPageRequest
	pages    []runtimedelivery.UIDPage
}

type appStaticDeliveryPartitioner struct {
	partitions []runtimedelivery.Partition
}

func (p appStaticDeliveryPartitioner) Partitions(context.Context) ([]runtimedelivery.Partition, error) {
	return append([]runtimedelivery.Partition(nil), p.partitions...), nil
}

type appRecordingFanoutRunner struct {
	mu    sync.Mutex
	tasks []runtimedelivery.FanoutTask
}

type recordingWorkerRuntime struct {
	startCount int
	stopCount  int
	startErr   error
	stopErr    error
	calls      *[]string
	name       string
	onStart    func()
	onStop     func()
}

func (r *recordingWorkerRuntime) Start(context.Context) error {
	if r.onStart != nil {
		r.onStart()
	}
	if r.calls != nil && r.name != "" {
		*r.calls = append(*r.calls, r.name+".start")
	}
	r.startCount++
	return r.startErr
}

func (r *recordingWorkerRuntime) Stop(context.Context) error {
	if r.onStop != nil {
		r.onStop()
	}
	if r.calls != nil && r.name != "" {
		*r.calls = append(*r.calls, r.name+".stop")
	}
	r.stopCount++
	return r.stopErr
}

func (r *appRecordingFanoutRunner) RunTask(_ context.Context, task runtimedelivery.FanoutTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	return nil
}

func (r *appRecordingFanoutRunner) snapshot() []runtimedelivery.FanoutTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtimedelivery.FanoutTask(nil), r.tasks...)
}

func (s *fakeDeliverySubscriberSource) ListSubscribers(_ context.Context, req runtimedelivery.SubscriberPageRequest) (runtimedelivery.UIDPage, error) {
	s.requests = append(s.requests, req)
	if len(s.pages) == 0 {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func (r *recordingSessionHandle) WriteDelivery(payload any) error {
	r.writes = append(r.writes, payload)
	return r.writeErr
}

func (r *recordingSessionHandle) CloseSession(reason string) error {
	r.reason = reason
	return nil
}

func (r *recordingTouchAuthority) ResolveRouteTarget(uid string) (presence.RouteTarget, error) {
	r.mu.Lock()
	target, ok := r.targets[uid]
	hook := r.resolveHook
	r.mu.Unlock()
	if hook != nil {
		hook(uid)
	}
	if !ok {
		return presence.RouteTarget{}, errors.New("target not found")
	}
	return target, nil
}

func (r *recordingTouchAuthority) TouchRoutesTo(_ context.Context, target presence.RouteTarget, routes []presence.Route) error {
	r.mu.Lock()
	r.batches = append(r.batches, touchBatch{
		target: target,
		routes: append([]presence.Route(nil), routes...),
	})
	err := r.err
	r.mu.Unlock()
	return err
}

func routesForTarget(batches []touchBatch, target presence.RouteTarget) []presence.Route {
	for _, batch := range batches {
		if batch.target == target {
			return batch.routes
		}
	}
	return nil
}

func newAppDeliveryTestSession(id uint64, writes *sendackSmokeSessionWrites) session.Session {
	return session.New(session.Config{
		ID: id,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			writes.append(f)
			return nil
		},
	})
}

func activateAppDeliverySession(t *testing.T, app *App, sess session.Session, uid string) {
	t.Helper()
	sess.SetValue(gateway.SessionValueUID, uid)
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	_, err := app.Handler().OnSessionActivate(&gateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	})
	if err != nil {
		t.Fatalf("OnSessionActivate(%s) error = %v", uid, err)
	}
}

func (w *sendackSmokeSessionWrites) waitForRecvPacket(t *testing.T, timeout time.Duration) *frame.RecvPacket {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		for _, written := range w.frames {
			if recv, ok := written.(*frame.RecvPacket); ok {
				w.mu.Unlock()
				return recv
			}
		}
		count := len(w.frames)
		w.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for RecvPacket; written frame count=%d", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func requireConversationEventually(t *testing.T, app *App, uid, channelID string, channelType uint8) conversationusecase.Conversation {
	t.Helper()
	var got []conversationusecase.Conversation
	var lastErr error
	waitUntil(t, time.Second, func() bool {
		list, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: uid, Limit: 10})
		lastErr = err
		if err != nil {
			return false
		}
		got = list.Items
		if len(got) != 1 {
			return false
		}
		item := got[0]
		return item.ChannelID == channelID &&
			item.ChannelType == int64(channelType) &&
			item.ActiveAt > 0 &&
			!item.SparseActive
	})
	if lastErr != nil {
		t.Fatalf("Conversations().List(%s) error = %v", uid, lastErr)
	}
	item := got[0]
	if item.ChannelID != channelID || item.ChannelType != int64(channelType) || item.ActiveAt <= 0 || item.SparseActive {
		t.Fatalf("conversation list for %s = %#v, want dense row for %s/%d", uid, got, channelID, channelType)
	}
	return item
}

func freshReadyAppNodeHealth(revision uint64) control.NodeHealth {
	return control.NodeHealth{
		Status:                  control.NodeAlive,
		Freshness:               control.NodeHealthFresh,
		RuntimeReady:            true,
		ObservedControlRevision: revision,
		ReportTTL:               30 * time.Second,
	}
}

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok() {
		return
	}
	t.Fatalf("condition was not met within %v", timeout)
}

type fakeGateway struct {
	calls          *[]string
	startErr       error
	stopErr        error
	sessionSummary gatewaycore.SessionSummary
}

func (f *fakeGateway) Start() error {
	*f.calls = append(*f.calls, "gateway.start")
	return f.startErr
}

func (f *fakeGateway) Stop() error {
	*f.calls = append(*f.calls, "gateway.stop")
	return f.stopErr
}

func (f *fakeGateway) SessionSummary() gatewaycore.SessionSummary {
	return f.sessionSummary
}

type gatewayAdmissionStub struct {
	accepting        bool
	acceptingChanged bool
}

func (g *gatewayAdmissionStub) Start() error {
	return nil
}

func (g *gatewayAdmissionStub) Stop() error {
	return nil
}

func (g *gatewayAdmissionStub) SetAcceptingNewSessions(accepting bool) {
	g.acceptingChanged = true
	g.accepting = accepting
}

func (g *gatewayAdmissionStub) AcceptingNewSessions() bool {
	return g.accepting
}

func (g *gatewayAdmissionStub) SessionSummary() gatewaycore.SessionSummary {
	return gatewaycore.SessionSummary{AcceptingNewSessions: g.accepting}
}

type recordingAppLogger struct {
	syncCalls int
	entries   []recordedAppLogEntry
}

type recordedAppLogEntry struct {
	level  string
	fields []wklog.Field
}

func (r *recordingAppLogger) Debug(_ string, fields ...wklog.Field) { r.log("DEBUG", fields...) }
func (r *recordingAppLogger) Info(_ string, fields ...wklog.Field)  { r.log("INFO", fields...) }
func (r *recordingAppLogger) Warn(_ string, fields ...wklog.Field)  { r.log("WARN", fields...) }
func (r *recordingAppLogger) Error(_ string, fields ...wklog.Field) { r.log("ERROR", fields...) }
func (r *recordingAppLogger) Fatal(_ string, fields ...wklog.Field) { r.log("FATAL", fields...) }

func (r *recordingAppLogger) Named(string) wklog.Logger {
	return r
}

func (r *recordingAppLogger) With(...wklog.Field) wklog.Logger {
	return r
}

func (r *recordingAppLogger) log(level string, fields ...wklog.Field) {
	r.entries = append(r.entries, recordedAppLogEntry{
		level:  level,
		fields: append([]wklog.Field(nil), fields...),
	})
}

func (r *recordingAppLogger) Sync() error {
	r.syncCalls++
	return nil
}

type fakeAPI struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeAPI) Start() error {
	*f.calls = append(*f.calls, "api.start")
	return f.startErr
}

func (f *fakeAPI) Stop(context.Context) error {
	*f.calls = append(*f.calls, "api.stop")
	return f.stopErr
}

type fakeManager struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeManager) Start() error {
	*f.calls = append(*f.calls, "manager.start")
	return f.startErr
}

func (f *fakeManager) Stop(context.Context) error {
	*f.calls = append(*f.calls, "manager.stop")
	return f.stopErr
}

func joinCalls(calls []string) string {
	return strings.Join(calls, ",")
}

func waitAppClusterSnapshotsConverge(t *testing.T, nodes []*clusterv2.Node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []clusterv2.Snapshot
	for time.Now().Before(deadline) {
		snapshots := make([]clusterv2.Snapshot, 0, len(nodes))
		for _, node := range nodes {
			snapshots = append(snapshots, node.Snapshot())
		}
		last = snapshots
		if appClusterSnapshotsConverged(snapshots) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cluster snapshots did not converge: %#v", last)
}

func appClusterSnapshotsConverged(snapshots []clusterv2.Snapshot) bool {
	if len(snapshots) == 0 {
		return false
	}
	want := snapshots[0]
	if want.StateRevision == 0 || !want.RoutesReady || !want.SlotsReady || !want.ChannelsReady || want.ControllerLead == 0 {
		return false
	}
	for _, snapshot := range snapshots[1:] {
		if snapshot.StateRevision != want.StateRevision ||
			snapshot.SlotCount != want.SlotCount ||
			snapshot.HashSlotCount != want.HashSlotCount ||
			snapshot.ControllerLead != want.ControllerLead ||
			!snapshot.RoutesReady ||
			!snapshot.SlotsReady ||
			!snapshot.ChannelsReady {
			return false
		}
	}
	return true
}

type blockingCluster struct {
	startEntered chan struct{}
	releaseStart chan struct{}
}

func newBlockingCluster() *blockingCluster {
	return &blockingCluster{
		startEntered: make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
}

func (f *blockingCluster) Start(context.Context) error {
	close(f.startEntered)
	<-f.releaseStart
	return nil
}

func (f *blockingCluster) Stop(context.Context) error {
	return nil
}

type stateGateway struct {
	mu      sync.Mutex
	running bool
}

func newStateGateway() *stateGateway {
	return &stateGateway{}
}

func (f *stateGateway) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	return nil
}

func (f *stateGateway) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}

func (f *stateGateway) runningState() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
