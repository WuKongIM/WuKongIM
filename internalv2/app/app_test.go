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

func TestNewRejectsUnsupportedPresenceMaxInflight(t *testing.T) {
	_, err := New(Config{Presence: PresenceConfig{RehydrateMaxInflightPerTarget: 2}}, WithCluster(&fakeCluster{}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
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
	if cluster.registeredService != accessnode.PresenceAuthorityRPCServiceID {
		t.Fatalf("registered presence rpc service = %d, want %d", cluster.registeredService, accessnode.PresenceAuthorityRPCServiceID)
	}
	if app.Handler() == nil {
		t.Fatal("gateway handler was not wired")
	}
	if _, err := app.Handler().OnSessionActivate(nil); !errors.Is(err, accessgateway.ErrUnauthenticatedSession) {
		t.Fatalf("OnSessionActivate(nil) error = %v, want unauthenticated session instead of missing presence", err)
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

func TestPresenceRehydrateWorkerPagesAffectedHashSlot(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent, 1)
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	for _, conn := range []online.OnlineConn{
		{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1001},
		{UID: "u2", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 12, SessionID: 102, DeviceID: "d2", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1002},
		{UID: "u3", HashSlot: 8, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 13, SessionID: 103, DeviceID: "d3", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1003},
	} {
		if err := reg.RegisterPending(conn); err != nil {
			t.Fatalf("RegisterPending(%d) error = %v", conn.SessionID, err)
		}
		if err := reg.MarkActive(conn.SessionID); err != nil {
			t.Fatalf("MarkActive(%d) error = %v", conn.SessionID, err)
		}
	}
	authority := &recordingRehydrateAuthority{done: make(chan struct{}), wantBatches: 2}
	worker := newPresenceRehydrateWorker(presenceRehydrateWorkerOptions{
		NodeID:    1,
		Events:    events,
		Local:     reg,
		Authority: authority,
		BatchSize: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Stop(context.Background())

	events <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	}}}

	select {
	case <-authority.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rehydrate")
	}

	if len(authority.batches) != 2 {
		t.Fatalf("rehydrate batches = %d, want 2", len(authority.batches))
	}
	for i, batch := range authority.batches {
		if len(batch.routes) != 1 {
			t.Fatalf("batch %d route count = %d, want 1", i, len(batch.routes))
		}
		if batch.target.HashSlot != 9 || batch.target.LeaderNodeID != 1 || batch.target.AuthorityEpoch != 2 {
			t.Fatalf("batch %d target = %#v, want hashSlot=9 leader=1 epoch=2", i, batch.target)
		}
	}
	gotSessions := []uint64{authority.batches[0].routes[0].SessionID, authority.batches[1].routes[0].SessionID}
	if !reflect.DeepEqual(gotSessions, []uint64{101, 102}) {
		t.Fatalf("rehydrated session IDs = %v, want [101 102]", gotSessions)
	}
}

func TestPresenceRehydrateWorkerIgnoresStaleAuthority(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceRehydrateWorker(presenceRehydrateWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})
	newer := clusterv2.RouteAuthority{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 3}
	older := clusterv2.RouteAuthority{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 2}

	worker.handleAuthority(context.Background(), newer)
	worker.handleAuthority(context.Background(), older)

	if len(directory.becomeTargets) != 1 {
		t.Fatalf("BecomeAuthority calls = %d, want 1", len(directory.becomeTargets))
	}
	if got := directory.becomeTargets[0].AuthorityEpoch; got != 3 {
		t.Fatalf("accepted authority epoch = %d, want 3", got)
	}
}

func TestPresenceRehydrateWorkerAppliesActionsAndCommitsPending(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent, 1)
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	conn := online.OnlineConn{
		UID:           "u1",
		HashSlot:      9,
		OwnerNodeID:   1,
		OwnerBootID:   7,
		OwnerSeq:      11,
		SessionID:     101,
		DeviceID:      "d1",
		DeviceFlag:    1,
		DeviceLevel:   1,
		Listener:      "tcp",
		ConnectedUnix: 1001,
		Session:       session,
	}
	if err := reg.RegisterPending(conn); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(conn.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	authority := &recordingRehydrateAuthority{
		done:        make(chan struct{}),
		wantBatches: 1,
		wantCommits: 1,
		results: []presence.RehydrateResult{{
			Route:        presence.RouteIdentity{UID: conn.UID, OwnerNodeID: conn.OwnerNodeID, OwnerBootID: conn.OwnerBootID, SessionID: conn.SessionID},
			Accepted:     true,
			PendingToken: "pending-1",
			Actions: []presence.RouteAction{{
				UID:         conn.UID,
				OwnerNodeID: conn.OwnerNodeID,
				OwnerBootID: conn.OwnerBootID,
				SessionID:   conn.SessionID,
				Reason:      "presence_conflict",
			}},
		}},
	}
	worker := newPresenceRehydrateWorker(presenceRehydrateWorkerOptions{
		NodeID:    1,
		Events:    events,
		Local:     reg,
		Authority: authority,
		BatchSize: 1,
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
	}}}

	select {
	case <-authority.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rehydrate commit")
	}

	if session.reason != "presence_conflict" {
		t.Fatalf("close reason = %q, want presence_conflict", session.reason)
	}
	if _, ok := reg.Connection(conn.SessionID); ok {
		t.Fatalf("connection %d remains after conflict action", conn.SessionID)
	}
	if !reflect.DeepEqual(authority.commitTokens, []string{"pending-1"}) {
		t.Fatalf("commit tokens = %v, want [pending-1]", authority.commitTokens)
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
	nodeID            uint64
	events            <-chan clusterv2.RouteAuthorityEvent
	snapshot          clusterv2.Snapshot
	registeredService uint8
	registeredHandler clusterv2.NodeRPCHandler
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

type rehydrateBatch struct {
	target presence.RouteTarget
	routes []presence.Route
}

type recordingPresenceDirectory struct {
	becomeTargets []presence.RouteTarget
	lostSlots     []uint16
}

func (r *recordingPresenceDirectory) BecomeAuthority(target presence.RouteTarget) {
	r.becomeTargets = append(r.becomeTargets, target)
}

func (r *recordingPresenceDirectory) LoseAuthority(hashSlot uint16) {
	r.lostSlots = append(r.lostSlots, hashSlot)
}

type recordingRehydrateAuthority struct {
	mu           sync.Mutex
	batches      []rehydrateBatch
	results      []presence.RehydrateResult
	commitTokens []string
	abortTokens  []string
	done         chan struct{}
	closed       bool
	wantBatches  int
	wantCommits  int
}

func (r *recordingRehydrateAuthority) RehydrateRoutes(_ context.Context, target presence.RouteTarget, routes []presence.Route) ([]presence.RehydrateResult, error) {
	r.mu.Lock()
	r.batches = append(r.batches, rehydrateBatch{
		target: target,
		routes: append([]presence.Route(nil), routes...),
	})
	results := append([]presence.RehydrateResult(nil), r.results...)
	r.maybeDoneLocked()
	r.mu.Unlock()
	return results, nil
}

func (r *recordingRehydrateAuthority) CommitRoute(_ context.Context, _ presence.RouteTarget, token string) error {
	r.mu.Lock()
	r.commitTokens = append(r.commitTokens, token)
	r.maybeDoneLocked()
	r.mu.Unlock()
	return nil
}

func (r *recordingRehydrateAuthority) AbortRoute(_ context.Context, _ presence.RouteTarget, token string) error {
	r.mu.Lock()
	r.abortTokens = append(r.abortTokens, token)
	r.mu.Unlock()
	return nil
}

func (r *recordingRehydrateAuthority) maybeDoneLocked() {
	if r.done == nil || r.closed {
		return
	}
	if r.wantBatches > 0 && len(r.batches) < r.wantBatches {
		return
	}
	if r.wantCommits > 0 && len(r.commitTokens) < r.wantCommits {
		return
	}
	close(r.done)
	r.closed = true
}

type recordingSessionHandle struct {
	reason string
}

func (r *recordingSessionHandle) CloseSession(reason string) error {
	r.reason = reason
	return nil
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
