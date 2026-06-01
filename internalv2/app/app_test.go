package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestStartOrderIsClusterThenGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
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

func TestGatewayStartFailureStopsCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
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
}

func TestStartOrderIncludesAPIBeforeGatewayWhenConfigured(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := New(Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
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

func TestNewWiresPresenceWhenGatewayEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	gatewayRuntime := &fakeGateway{calls: &[]string{}}

	app, err := New(
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

func TestPresenceBenchSnapshotAggregatesOwnerAndAuthorityState(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := New(Config{Cluster: clusterv2.Config{NodeID: 1}}, WithCluster(cluster))
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
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,presence.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,presence.start,gateway.start", got)
	}
}

func TestStartSeedsPresenceAuthorityFromCurrentRoutes(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent)
	cluster := newFakePresenceCluster(1, events)
	cluster.snapshot = clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 10}
	app, err := New(Config{}, WithCluster(cluster), WithGateway(&fakeGateway{calls: &[]string{}}))
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
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		BatchSize: 10,
	})

	worker.flushOnce(context.Background(), time.Now())

	requeued := reg.DrainTouched(10)
	if len(requeued) != 1 {
		t.Fatalf("requeued dirty routes = %d, want 1", len(requeued))
	}
	if requeued[0].SessionID != conn.SessionID || requeued[0].UID != conn.UID {
		t.Fatalf("requeued route = %#v, want session %d uid %s", requeued[0], conn.SessionID, conn.UID)
	}
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

func TestPresenceTouchWorkerKeepsEpochFenceAcrossNoLeaderAuthority(t *testing.T) {
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
		LeaderNodeID:   0,
		RouteRevision:  4,
		AuthorityEpoch: 4,
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
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 5,
	})

	got := directory.becomeSnapshot()
	if len(got) != 2 || got[0].AuthorityEpoch != 3 || got[1].AuthorityEpoch != 5 {
		t.Fatalf("become targets = %#v, want epochs 3 then 5", got)
	}
	if lost := directory.loseSnapshot(); !reflect.DeepEqual(lost, []uint16{9}) {
		t.Fatalf("lost slots = %v, want one no-leader clear", lost)
	}
}

func TestCurrentPresenceAuthoritiesIncludesNoLeaderRoutes(t *testing.T) {
	cluster := &fakeWriteReadyCluster{
		snapshots: []clusterv2.Snapshot{{HashSlotCount: 1}},
		routes: map[uint16]clusterv2.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 0, Revision: 4, AuthorityEpoch: 3},
		},
	}
	app := &App{cluster: cluster}

	got := app.currentPresenceAuthorities()

	if len(got) != 1 {
		t.Fatalf("authorities len = %d, want 1", len(got))
	}
	if got[0].LeaderNodeID != 0 || got[0].RouteRevision != 4 || got[0].AuthorityEpoch != 3 {
		t.Fatalf("authority = %#v, want no-leader revision 4 epoch 3", got[0])
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
	app, err := New(
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

func TestGatewayStartFailureStopsAPIThenCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 5)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := New(Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
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
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,cluster.route,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,cluster.route,gateway.start", got)
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
	app, err := New(Config{Cluster: clusterv2.Config{Timeouts: clusterv2.TimeoutConfig{Start: time.Millisecond}}}, WithCluster(cluster), WithGateway(gateway))
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
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
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
	app, err := New(Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
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
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
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
	app, err := New(Config{}, WithCluster(cluster), WithGateway(gateway))
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
	app, err := New(Config{Cluster: clusterv2.Config{NodeID: 7}}, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ids := messageIDAllocatorFromApp(t, app)
	if got, want := ids.Next(), uint64(7<<48)+1; got != want {
		t.Fatalf("first message id = %d, want %d", got, want)
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
		app, err := New(cfg)
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
	snapshots []clusterv2.Snapshot
	routes    map[uint16]clusterv2.Route
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

type fakePresenceCluster struct {
	fakeCluster
	nodeID             uint64
	events             <-chan clusterv2.RouteAuthorityEvent
	snapshot           clusterv2.Snapshot
	registeredService  uint8
	registeredHandler  clusterv2.NodeRPCHandler
	registeredHandlers map[uint8]clusterv2.NodeRPCHandler
}

func newFakePresenceCluster(nodeID uint64, events <-chan clusterv2.RouteAuthorityEvent) *fakePresenceCluster {
	return &fakePresenceCluster{nodeID: nodeID, events: events}
}

func (f *fakePresenceCluster) NodeID() uint64 {
	return f.nodeID
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
	reason string
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
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeGateway) Start() error {
	*f.calls = append(*f.calls, "gateway.start")
	return f.startErr
}

func (f *fakeGateway) Stop() error {
	*f.calls = append(*f.calls, "gateway.stop")
	return f.stopErr
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

type messageIDAllocator interface {
	Next() uint64
}

func messageIDAllocatorFromApp(t *testing.T, app *App) messageIDAllocator {
	t.Helper()
	messages := app.Messages()
	if messages == nil {
		t.Fatalf("Messages() = nil")
	}
	field := reflect.ValueOf(messages).Elem().FieldByName("messageID")
	if !field.IsValid() {
		t.Fatalf("message app has no messageID field")
	}
	ids, ok := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(messageIDAllocator)
	if !ok {
		t.Fatalf("messageID field does not implement Next() uint64")
	}
	return ids
}
