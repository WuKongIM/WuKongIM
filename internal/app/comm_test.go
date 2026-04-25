package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

const multinodeAppReadTimeout = 20 * time.Second
const appReadTimeout = 2 * time.Second
const threeNodeHarnessStartAttempts = 3
const threeNodeHarnessRetryBackoff = 300 * time.Millisecond

var appWKProtoClients sync.Map

type sendStressAcceptanceSpec struct {
	Benchmark                        sendStressConfig
	GatewaySendTimeout               time.Duration
	FollowerReplicationRetryInterval time.Duration
	AppendGroupCommitMaxWait         time.Duration
	AppendGroupCommitMaxRecords      int
	AppendGroupCommitMaxBytes        int
	DataPlanePoolSize                int
	DataPlaneMaxFetchInflight        int
	DataPlaneMaxPendingFetch         int
	MinISR                           int
}

func sendStressAcceptancePreset() sendStressAcceptanceSpec {
	return sendStressAcceptanceSpec{
		Benchmark: sendStressConfig{
			Mode:                 sendStressModeThroughput,
			Duration:             15 * time.Second,
			Workers:              16,
			Senders:              32,
			MessagesPerWorker:    50,
			DialTimeout:          3 * time.Second,
			AckTimeout:           20 * time.Second,
			MaxInflightPerWorker: 64,
			Seed:                 20260408,
		},
		GatewaySendTimeout:               25 * time.Second,
		FollowerReplicationRetryInterval: 250 * time.Millisecond,
		AppendGroupCommitMaxWait:         2 * time.Millisecond,
		AppendGroupCommitMaxRecords:      128,
		AppendGroupCommitMaxBytes:        256 * 1024,
		DataPlanePoolSize:                8,
		DataPlaneMaxFetchInflight:        16,
		DataPlaneMaxPendingFetch:         16,
		MinISR:                           2,
	}
}

func envBool(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()

	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()

	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func envInt64(t *testing.T, name string, fallback int64) int64 {
	t.Helper()

	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func newThreeNodeConversationSyncHarness(t *testing.T) *threeNodeAppHarness {
	t.Helper()

	clusterAddrs := reserveTestTCPAddrs(t, 3)
	gatewayAddrs := reserveTestTCPAddrs(t, 3)
	apiAddrs := reserveTestTCPAddrs(t, 3)
	clusterNodes := make([]NodeConfigRef, 0, 3)
	for i := 0; i < 3; i++ {
		clusterNodes = append(clusterNodes, NodeConfigRef{
			ID:   uint64(i + 1),
			Addr: clusterAddrs[uint64(i+1)],
		})
	}

	root := t.TempDir()
	apps := make(map[uint64]*App, 3)
	for i := 0; i < 3; i++ {
		nodeID := uint64(i + 1)
		cfg := validConfig()
		cfg.Node.ID = nodeID
		cfg.Node.Name = fmt.Sprintf("node-%d", nodeID)
		cfg.Node.DataDir = fmt.Sprintf("%s/node-%d", root, nodeID)
		cfg.Storage = StorageConfig{}
		cfg.Cluster.ListenAddr = clusterAddrs[nodeID]
		cfg.Cluster.Nodes = append([]NodeConfigRef(nil), clusterNodes...)
		cfg.Cluster.SlotCount = 1
		cfg.Cluster.ControllerReplicaN = 3
		cfg.Cluster.SlotReplicaN = 3
		cfg.Cluster.Slots = nil
		cfg.Cluster.TickInterval = 10 * time.Millisecond
		cfg.Cluster.ElectionTick = 10
		cfg.Cluster.HeartbeatTick = 1
		cfg.Cluster.ForwardTimeout = 2 * time.Second
		cfg.Cluster.DialTimeout = 2 * time.Second
		cfg.Cluster.PoolSize = 1
		cfg.API.ListenAddr = apiAddrs[nodeID]
		cfg.Gateway.Listeners = []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto", gatewayAddrs[nodeID]),
		}

		app, err := New(cfg)
		require.NoError(t, err)
		apps[nodeID] = app
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(apps))
	for _, app := range []*App{apps[1], apps[2], apps[3]} {
		wg.Add(1)
		go func(app *App) {
			defer wg.Done()
			errCh <- app.Start()
		}(app)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		for _, app := range []*App{apps[3], apps[2], apps[1]} {
			require.NoError(t, app.Stop())
		}
	})
	return &threeNodeAppHarness{apps: apps}
}

type threeNodeAppHarness struct {
	apps  map[uint64]*App
	specs map[uint64]appNodeSpec
}

type appNodeSpec struct {
	cfg Config
}

func newThreeNodeAppHarness(t *testing.T) *threeNodeAppHarness {
	return newThreeNodeAppHarnessWithOptions(t, 1, 3, nil)
}

func newThreeNodeManagedAppHarness(t *testing.T) *threeNodeAppHarness {
	return newThreeNodeManagedAppHarnessWithLayout(t, 1, 3)
}

func newThreeNodeManagedAppHarnessWithLayout(t *testing.T, slotCount uint32, slotReplicaN int) *threeNodeAppHarness {
	return newThreeNodeAppHarnessWithOptions(t, slotCount, slotReplicaN, nil)
}

func newThreeNodeAppHarnessWithConfigMutator(t *testing.T, mutate func(*Config)) *threeNodeAppHarness {
	return newThreeNodeAppHarnessWithOptions(t, 1, 3, mutate)
}

func newThreeNodeAppHarnessWithOptions(t *testing.T, slotCount uint32, slotReplicaN int, mutate func(*Config)) *threeNodeAppHarness {
	t.Helper()

	var lastErr error
	for attempt := 1; attempt <= threeNodeHarnessStartAttempts; attempt++ {
		harness, cleanup, err := buildThreeNodeAppHarness(t, slotCount, slotReplicaN, mutate)
		if err == nil {
			t.Cleanup(func() {
				require.NoError(t, cleanup())
			})
			return harness
		}

		lastErr = err
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Logf("cleanup failed after three-node harness startup error: %v", cleanupErr)
		}
		if attempt == threeNodeHarnessStartAttempts {
			break
		}

		t.Logf("retrying three-node harness startup (%d/%d): %v", attempt, threeNodeHarnessStartAttempts, err)
		time.Sleep(time.Duration(attempt) * threeNodeHarnessRetryBackoff)
	}

	require.NoError(t, lastErr)
	return nil
}

func buildThreeNodeAppHarness(t *testing.T, slotCount uint32, slotReplicaN int, mutate func(*Config)) (*threeNodeAppHarness, func() error, error) {
	t.Helper()

	clusterAddrs := reserveTestTCPAddrs(t, 3)
	gatewayAddrs := reserveTestTCPAddrs(t, 3)
	apiAddrs := reserveTestTCPAddrs(t, 3)
	clusterNodes := make([]NodeConfigRef, 0, 3)
	for i := 0; i < 3; i++ {
		clusterNodes = append(clusterNodes, NodeConfigRef{
			ID:   uint64(i + 1),
			Addr: clusterAddrs[uint64(i+1)],
		})
	}

	root := t.TempDir()
	apps := make(map[uint64]*App, 3)
	specs := make(map[uint64]appNodeSpec, 3)
	for i := 0; i < 3; i++ {
		nodeID := uint64(i + 1)
		cfg := validConfig()
		cfg.Node.ID = nodeID
		cfg.Node.Name = fmt.Sprintf("node-%d", nodeID)
		cfg.Node.DataDir = filepath.Join(root, fmt.Sprintf("node-%d", nodeID))
		cfg.Storage = StorageConfig{}
		cfg.Cluster.ListenAddr = clusterAddrs[nodeID]
		cfg.Cluster.Nodes = append([]NodeConfigRef(nil), clusterNodes...)
		cfg.Cluster.SlotCount = slotCount
		cfg.Cluster.ControllerReplicaN = 3
		cfg.Cluster.SlotReplicaN = slotReplicaN
		cfg.Cluster.Slots = nil
		cfg.Cluster.TickInterval = 10 * time.Millisecond
		cfg.Cluster.ElectionTick = 10
		cfg.Cluster.HeartbeatTick = 1
		cfg.Cluster.ForwardTimeout = 2 * time.Second
		cfg.Cluster.DialTimeout = 2 * time.Second
		cfg.Cluster.PoolSize = 1
		cfg.Cluster.DataPlanePoolSize = 8
		cfg.Cluster.DataPlaneMaxFetchInflight = 16
		cfg.Cluster.DataPlaneMaxPendingFetch = 16
		cfg.API.ListenAddr = apiAddrs[nodeID]
		cfg.Gateway.Listeners = []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto", gatewayAddrs[nodeID]),
		}
		if mutate != nil {
			mutate(&cfg)
		}

		specs[nodeID] = appNodeSpec{cfg: cfg}

		app, err := New(cfg)
		if err != nil {
			return nil, func() error { return stopThreeNodeApps(apps) }, err
		}
		apps[nodeID] = app
	}

	if err := startThreeNodeApps(apps); err != nil {
		return nil, func() error { return stopThreeNodeApps(apps) }, err
	}

	harness := &threeNodeAppHarness{apps: apps, specs: specs}
	return harness, func() error { return stopThreeNodeApps(apps) }, nil
}

func startThreeNodeApps(apps map[uint64]*App) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(apps))
	for i := 1; i <= len(apps); i++ {
		app := apps[uint64(i)]
		if app == nil {
			continue
		}
		wg.Add(1)
		go func(app *App) {
			defer wg.Done()
			errCh <- app.Start()
		}(app)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stopThreeNodeApps(apps map[uint64]*App) error {
	var errs []error
	for i := len(apps); i >= 1; i-- {
		app := apps[uint64(i)]
		if app == nil {
			continue
		}
		if err := app.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func applySendPathTuning(t *testing.T, cfg *Config, preset sendStressAcceptanceSpec) {
	t.Helper()

	setClusterConfigDurationField(t, &cfg.Cluster, "FollowerReplicationRetryInterval", preset.FollowerReplicationRetryInterval)
	setClusterConfigDurationField(t, &cfg.Cluster, "AppendGroupCommitMaxWait", preset.AppendGroupCommitMaxWait)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxRecords", preset.AppendGroupCommitMaxRecords)
	setClusterConfigIntField(t, &cfg.Cluster, "AppendGroupCommitMaxBytes", preset.AppendGroupCommitMaxBytes)
	cfg.Cluster.DataPlanePoolSize = preset.DataPlanePoolSize
	cfg.Cluster.DataPlaneMaxFetchInflight = preset.DataPlaneMaxFetchInflight
	cfg.Cluster.DataPlaneMaxPendingFetch = preset.DataPlaneMaxPendingFetch
	cfg.Gateway.SendTimeout = preset.GatewaySendTimeout
}

func TestThreeNodeAppHarnessUsesSendPathTuning(t *testing.T) {
	preset := sendStressAcceptancePreset()
	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(cfg *Config) {
		applySendPathTuning(t, cfg, preset)
	})

	require.Equal(t, 15*time.Second, preset.Benchmark.Duration)
	require.Equal(t, 16, preset.Benchmark.Workers)
	require.Equal(t, 32, preset.Benchmark.Senders)
	require.Equal(t, 64, preset.Benchmark.MaxInflightPerWorker)
	require.Equal(t, 20*time.Second, preset.Benchmark.AckTimeout)
	require.Equal(t, 25*time.Second, preset.GatewaySendTimeout)

	for nodeID, app := range harness.apps {
		require.NotNil(t, app, "node %d app should be running", nodeID)
		require.NotNil(t, app.dataPlanePool, "node %d should wire data plane pool", nodeID)
		require.NotNil(t, app.isrTransport, "node %d should wire transport", nodeID)
		require.Equal(t, preset.DataPlanePoolSize, appDataPlanePoolSize(t, app), "node %d pool size", nodeID)
		require.Equal(t, preset.DataPlaneMaxFetchInflight, appISRMaxFetchInflightPeerLimit(t, app), "node %d fetch inflight", nodeID)
		require.Equal(t, preset.DataPlaneMaxPendingFetch, appDataPlaneAdapterMaxPendingFetch(t, app), "node %d pending fetch", nodeID)
		require.Equal(t, preset.FollowerReplicationRetryInterval, appISRFollowerReplicationRetryInterval(t, app), "node %d retry interval", nodeID)
		maxWait, maxRecords, maxBytes := appReplicaAppendGroupCommitConfig(t, app)
		require.Equal(t, preset.AppendGroupCommitMaxWait, maxWait, "node %d append group wait", nodeID)
		require.Equal(t, preset.AppendGroupCommitMaxRecords, maxRecords, "node %d append group records", nodeID)
		require.Equal(t, preset.AppendGroupCommitMaxBytes, maxBytes, "node %d append group bytes", nodeID)
		require.Equal(t, preset.GatewaySendTimeout, appGatewayHandlerDurationField(t, app.GatewayHandler(), "sendTimeout"), "node %d gateway send timeout", nodeID)
	}
}

func TestThreeNodeAppHarnessRestartNodeRestartsClusterTransportOnConfiguredAddr(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	const nodeID = uint64(3)

	wantAddr := harness.specs[nodeID].cfg.Cluster.ListenAddr
	harness.stopNode(t, nodeID)

	app := harness.restartNode(t, nodeID)
	server := app.Cluster().Server()
	require.NotNil(t, server)
	require.NotNil(t, server.Listener())
	require.Equal(t, wantAddr, server.Listener().Addr().String())

	conn, err := net.DialTimeout("tcp", wantAddr, time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestThreeNodeAppHarnessRestartedFormerLeaderAcceptsClusterDialsAfterLeaderChange(t *testing.T) {
	harness := newThreeNodeAppHarness(t)

	leaderID := harness.waitForStableLeader(t, 1)
	wantAddr := harness.specs[leaderID].cfg.Cluster.ListenAddr

	harness.stopNode(t, leaderID)
	newLeaderID := harness.waitForLeaderChange(t, 1, leaderID)
	harness.restartNode(t, leaderID)

	addr, err := harness.apps[newLeaderID].Cluster().Discovery().Resolve(leaderID)
	require.NoError(t, err)
	require.Equal(t, wantAddr, addr)

	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", wantAddr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 50*time.Millisecond)
}

func (h *threeNodeAppHarness) runningApps() []*App {
	apps := make([]*App, 0, len(h.apps))
	for _, nodeID := range []uint64{1, 2, 3} {
		if app := h.apps[nodeID]; app != nil {
			apps = append(apps, app)
		}
	}
	return apps
}

func (h *threeNodeAppHarness) orderedApps() []*App {
	return h.runningApps()
}

func (h *threeNodeAppHarness) appsWithLeaderFirst(leaderID uint64) []*App {
	apps := make([]*App, 0, len(h.apps))
	if leader := h.apps[leaderID]; leader != nil {
		apps = append(apps, leader)
	}
	for _, app := range h.runningApps() {
		if app.cfg.Node.ID == leaderID {
			continue
		}
		apps = append(apps, app)
	}
	return apps
}

func (h *threeNodeAppHarness) stopNode(t *testing.T, nodeID uint64) {
	t.Helper()

	app := h.apps[nodeID]
	require.NotNil(t, app, "node %d is not running", nodeID)
	require.NoError(t, app.Stop())
	h.apps[nodeID] = nil
}

func (h *threeNodeAppHarness) restartNode(t *testing.T, nodeID uint64) *App {
	t.Helper()

	require.Nil(t, h.apps[nodeID], "node %d is already running", nodeID)

	spec, ok := h.specs[nodeID]
	require.True(t, ok, "missing spec for node %d", nodeID)

	app, err := New(spec.cfg)
	require.NoError(t, err)
	require.NoError(t, app.Start())
	h.apps[nodeID] = app
	return app
}

func (h *threeNodeAppHarness) waitForLeaderChange(t *testing.T, slotID uint64, oldLeader uint64) uint64 {
	t.Helper()

	require.Eventually(t, func() bool {
		leader, ok := h.consensusLeader(multiraft.SlotID(slotID))
		return ok && leader != 0 && uint64(leader) != oldLeader
	}, 10*time.Second, 50*time.Millisecond)
	return h.waitForStableLeader(t, slotID)
}

func (h *threeNodeAppHarness) waitForStableLeader(t *testing.T, slotID uint64) uint64 {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var stable multiraft.NodeID
	stableCount := 0
	for time.Now().Before(deadline) {
		leader, ok := h.consensusLeader(multiraft.SlotID(slotID))
		if ok && leader != 0 {
			if leader == stable {
				stableCount++
			} else {
				stable = leader
				stableCount = 1
			}
			if stableCount >= 5 {
				return uint64(stable)
			}
		} else {
			stable = 0
			stableCount = 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for stable leader for slot %d", slotID)
	return 0
}

func (h *threeNodeAppHarness) consensusLeader(slotID multiraft.SlotID) (multiraft.NodeID, bool) {
	var leader multiraft.NodeID
	for _, app := range h.runningApps() {
		current, err := app.Cluster().LeaderOf(slotID)
		if err != nil {
			return 0, false
		}
		if leader == 0 {
			leader = current
			continue
		}
		if current != leader {
			return 0, false
		}
	}
	return leader, true
}

func reserveTestTCPAddrs(t *testing.T, count int) map[uint64]string {
	t.Helper()

	addrs := make(map[uint64]string, count)
	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addrs[uint64(i+1)] = ln.Addr().String()
		require.NoError(t, ln.Close())
	}
	return addrs
}

func connectMultinodeWKProtoClient(t *testing.T, app *App, uid, deviceID string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", app.Gateway().ListenerAddr("tcp-wkproto"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	sendAppWKProtoFrame(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             uid,
		DeviceID:        deviceID,
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	connack, ok := readAppWKProtoFrameWithin(t, conn, multinodeAppReadTimeout).(*frame.ConnackPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)
	require.NoError(t, appWKProtoClientForConn(t, conn).ApplyConnack(connack))
	return conn
}

func readAppWKProtoFrameWithin(t *testing.T, conn net.Conn, timeout time.Duration) frame.Frame {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(timeout)))
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	require.NoError(t, err)
	return f
}

func waitForAppCommittedMessage(t *testing.T, app *App, id channel.ChannelID, seq uint64, timeout time.Duration) channel.Message {
	t.Helper()

	store := channelStoreForID(app.ChannelLogDB(), id)
	key := channelhandler.KeyFromChannelID(id)
	var msg channel.Message
	deadline := time.Now().Add(timeout)
	lastState := "replica state unavailable"
	for time.Now().Before(deadline) {
		handle, ok := app.ISRRuntime().Channel(key)
		if !ok {
			lastState = fmt.Sprintf("channel=%s/%d node=%d missing runtime", id.ID, id.Type, app.cfg.Node.ID)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		state := handle.Status()
		if !state.CommitReady || seq > state.HW {
			lastState = fmt.Sprintf("channel=%s/%d node=%d commit_ready=%t hw=%d want_seq=%d leo=%d leader=%d epoch=%d", id.ID, id.Type, app.cfg.Node.ID, state.CommitReady, state.HW, seq, state.LEO, state.Leader, state.Epoch)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		loaded, err := channelhandler.LoadMsg(store, state.HW, seq)
		if err != nil {
			lastState = fmt.Sprintf("channel=%s/%d node=%d load seq=%d with hw=%d failed: %v", id.ID, id.Type, app.cfg.Node.ID, seq, state.HW, err)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		msg = loaded
		return msg
	}
	require.Failf(t, "wait for committed message", "timeout after %s: %s", timeout, lastState)
	return msg
}

func channelStoreForID(db *channelstore.Engine, id channel.ChannelID) *channelstore.ChannelStore {
	return db.ForChannel(channelhandler.KeyFromChannelID(id), id)
}

func sendAppWKProtoFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()

	switch pkt := f.(type) {
	case *frame.ConnectPacket:
		client := appWKProtoClientForConn(t, conn)
		var err error
		f, err = client.UseClientKey(pkt)
		require.NoError(t, err)
	case *frame.SendPacket:
		client := appWKProtoClientForConn(t, conn)
		cloned := *pkt
		require.NoError(t, client.EncryptSendPacket(&cloned))
		f = &cloned
	}

	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	require.NoError(t, err)

	_, err = conn.Write(payload)
	require.NoError(t, err)
}

func readAppWKProtoFrame(t *testing.T, conn net.Conn) frame.Frame {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(appReadTimeout)))
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	require.NoError(t, err)
	if value, ok := appWKProtoClients.Load(conn); ok {
		client := value.(*testkit.WKProtoClient)
		switch pkt := f.(type) {
		case *frame.ConnackPacket:
			require.NoError(t, client.ApplyConnack(pkt))
		case *frame.RecvPacket:
			require.NoError(t, client.DecryptRecvPacket(pkt))
		}
	}
	return f
}

func connectAppWKProtoClient(t *testing.T, app *App, uid string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", app.Gateway().ListenerAddr("tcp-wkproto"))
	require.NoError(t, err)

	sendAppWKProtoFrame(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             uid,
		DeviceID:        uid + "-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})

	pkt := readAppWKProtoFrame(t, conn)
	connack, ok := pkt.(*frame.ConnackPacket)
	require.True(t, ok, "expected *frame.ConnackPacket, got %T", pkt)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)

	return conn
}

func readAppRecvPacket(t *testing.T, conn net.Conn) *frame.RecvPacket {
	t.Helper()

	pkt := readAppWKProtoFrame(t, conn)
	recv, ok := pkt.(*frame.RecvPacket)
	require.True(t, ok, "expected *frame.RecvPacket, got %T", pkt)
	return recv
}

func appWKProtoClientForConn(t *testing.T, conn net.Conn) *testkit.WKProtoClient {
	t.Helper()

	client, err := appWKProtoClientForConnErr(conn)
	require.NoError(t, err)
	return client
}

func appWKProtoClientForConnErr(conn net.Conn) (*testkit.WKProtoClient, error) {
	if value, ok := appWKProtoClients.Load(conn); ok {
		return value.(*testkit.WKProtoClient), nil
	}
	client, err := testkit.NewWKProtoClient()
	if err != nil {
		return nil, err
	}
	appWKProtoClients.Store(conn, client)
	return client, nil
}

func waitForPresenceSessionID(t *testing.T, app *App, uid string) uint64 {
	t.Helper()

	var sessionID uint64
	require.Eventually(t, func() bool {
		routes, err := app.presenceApp.EndpointsByUID(context.Background(), uid)
		if err != nil || len(routes) == 0 {
			return false
		}
		sessionID = routes[0].SessionID
		return sessionID != 0
	}, time.Second, 10*time.Millisecond)
	return sessionID
}

func seedChannelRuntimeMeta(t *testing.T, app *App, channelID string, channelType uint8) {
	t.Helper()

	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  int64(channelType),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     []uint64{app.cfg.Node.ID},
		ISR:          []uint64{app.cfg.Node.ID},
		Leader:       app.cfg.Node.ID,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, app.DB().ForSlot(1).UpsertChannelRuntimeMeta(context.Background(), meta))
}
