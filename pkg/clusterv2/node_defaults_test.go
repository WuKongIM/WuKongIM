package clusterv2

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestNodeProposeDelegatesToService(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if proposer.calls != 1 || proposer.last.Key != "u1" {
		t.Fatalf("proposer calls=%d last=%#v, want key u1", proposer.calls, proposer.last)
	}
}

func TestNodeProbeWriteReadyProposesOneNoopPerPhysicalSlot(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 2},
		},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}, {SlotID: 2, Leader: 2}})
	node.snapshot = Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 2, HashSlotCount: 4}
	node.started.Store(true)

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}

	if proposer.calls != 2 {
		t.Fatalf("proposer calls=%d, want one probe per physical slot", proposer.calls)
	}
	got := map[uint32]uint16{}
	for _, req := range proposer.requests {
		if !reflect.DeepEqual(req.Command, metafsm.EncodeNoopCommand()) {
			t.Fatalf("probe command = %v, want noop command", req.Command)
		}
		if !req.Target.HasSlotID || !req.Target.HasHashSlot {
			t.Fatalf("probe target = %#v, want explicit slot and hash slot", req.Target)
		}
		got[req.Target.SlotID] = req.Target.HashSlot
	}
	want := map[uint32]uint16{1: 0, 2: 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe targets = %#v, want %#v", got, want)
	}
}

func TestNodeInitializesDefaultProposerWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t), withController(control.NewStaticController(nodeControlSnapshot())))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})

	err = node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("Propose() error = %v, want default proposer initialized", err)
	}
}

func TestNodeInitializesDefaultChannelsWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "missing", Type: 1}})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("AppendChannel() error = %v, want default channel service initialized", err)
	}
}

func TestStorageConfigDoesNotExposeCommitNoSync(t *testing.T) {
	_, ok := reflect.TypeOf(StorageConfig{}).FieldByName("CommitNoSync")
	if ok {
		t.Fatal("StorageConfig exposes CommitNoSync, want durable sync fixed on")
	}
}

func TestNodeStorageMetricsSnapshotAggregatesDefaultStores(t *testing.T) {
	base := t.TempDir()
	channelStore := channelstore.NewMessageDBFactory(filepath.Join(base, "messages"))
	metaDB, err := metadb.Open(filepath.Join(base, "slotmeta"))
	if err != nil {
		t.Fatalf("Open meta DB: %v", err)
	}
	raftDB, err := raftlog.Open(filepath.Join(base, "slotraft"), raftlog.Options{})
	if err != nil {
		t.Fatalf("Open raft DB: %v", err)
	}
	node := &Node{
		defaultChannelStore: channelStore,
		defaultSlotMetaDB:   metaDB,
		defaultSlotRaftDB:   raftDB,
	}
	t.Cleanup(func() {
		_ = channelStore.Close()
		_ = metaDB.Close()
		_ = raftDB.Close()
	})

	snapshot := node.StorageMetricsSnapshot()
	for _, store := range []string{"channel_log", "meta", "raft"} {
		metrics, ok := findStorageMetricsSnapshot(snapshot, store)
		if !ok {
			t.Fatalf("store %q missing from snapshot %#v", store, snapshot.Stores)
		}
		if metrics.DiskSpaceUsageBytes == 0 {
			t.Fatalf("store %q DiskSpaceUsageBytes = 0, want physical usage", store)
		}
	}
}

func TestNodeDefaultChannelsUseConfiguredCommitObserver(t *testing.T) {
	cfg := validNodeConfig(t)
	observer := recordingCommitCoordinatorObserver{}
	cfg.Storage.CommitObserver = observer
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if node.defaultChannelStore == nil {
		t.Fatal("defaultChannelStore = nil, want default store")
	}
	if got := node.defaultChannelStore.CommitCoordinatorConfig().Observer; got != observer {
		t.Fatalf("default channel store commit observer = %#v, want configured observer", got)
	}
}

func TestNodeDefaultChannelsUseConfiguredCommitCoordinatorTuning(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Storage.CommitFlushWindow = 750 * time.Microsecond
	cfg.Storage.CommitMaxRequests = 16
	cfg.Storage.CommitMaxRecords = 256
	cfg.Storage.CommitMaxBytes = 128 * 1024
	cfg.Storage.CommitShards = 4
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if node.defaultChannelStore == nil {
		t.Fatal("defaultChannelStore = nil, want default store")
	}
	commitCfg := node.defaultChannelStore.CommitCoordinatorConfig()
	if commitCfg.FlushWindow != 750*time.Microsecond {
		t.Fatalf("FlushWindow = %s, want 750us", commitCfg.FlushWindow)
	}
	if commitCfg.MaxRequests != 16 || commitCfg.MaxRecords != 256 || commitCfg.MaxBytes != 128*1024 || commitCfg.Shards != 4 {
		t.Fatalf("commit coordinator tuning = requests:%d records:%d bytes:%d shards:%d", commitCfg.MaxRequests, commitCfg.MaxRecords, commitCfg.MaxBytes, commitCfg.Shards)
	}
}

func TestChannelReplicaCountDefaultsToSlotReplicaCount(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 3 {
		t.Fatalf("Channel.ReplicaCount = %d, want slot replica count 3", cfg.Channel.ReplicaCount)
	}
}

func TestChannelReplicaCountPreservesExplicitValue(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.Channel.ReplicaCount = 2
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 2 {
		t.Fatalf("Channel.ReplicaCount = %d, want explicit value 2", cfg.Channel.ReplicaCount)
	}
}

func TestNodeInitializesDefaultControllerV2WhenOptionMissing(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "node-default-control"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	snap := node.Snapshot()
	if !snap.RoutesReady || snap.StateRevision == 0 || snap.SlotCount != 1 || snap.HashSlotCount != 4 {
		t.Fatalf("Snapshot() = %#v, want ControllerV2-backed ready routes", snap)
	}
	route, err := node.RouteHashSlot(0)
	if err != nil && !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want route or no slot leader", err)
	}
	if err == nil && route.SlotID != 1 {
		t.Fatalf("RouteHashSlot() = %#v, want slot 1", route)
	}
}

func TestNodeDefaultControllerV2ThreeVotersConvergeOverTransport(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "node-default-control-three"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startErrs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { startErrs <- node.Start(startCtx) }()
		t.Cleanup(func() { _ = node.Stop(context.Background()) })
	}
	for range nodes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitClusterReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitClusterReady() error = %v", err)
	}
}

func TestNodeDefaultSeedJoinMirrorSyncsFromSeedAddresses(t *testing.T) {
	seedAddr := freeTCPAddr(t)
	seed, err := New(Config{
		NodeID:     1,
		ListenAddr: seedAddr,
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID:      "seed-join-sync",
			Voters:         []ControlVoter{{NodeID: 1, Addr: seedAddr}},
			AllowBootstrap: true,
		},
		Slots: SlotConfig{InitialSlotCount: 1, HashSlotCount: 4, ReplicaCount: 1},
	})
	if err != nil {
		t.Fatalf("New(seed) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := seed.Start(ctx); err != nil {
		t.Fatalf("Start(seed) error = %v", err)
	}
	t.Cleanup(func() { _ = seed.Stop(context.Background()) })

	joinAddr := freeTCPAddr(t)
	joining, err := New(Config{
		NodeID:     4,
		ListenAddr: joinAddr,
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "seed-join-sync"},
		Join: JoinConfig{
			Seeds:         []string{seedAddr},
			AdvertiseAddr: joinAddr,
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 1},
	})
	if err != nil {
		t.Fatalf("New(joining) error = %v", err)
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer joinCancel()
	if err := joining.Start(joinCtx); err != nil {
		t.Fatalf("Start(joining) error = %v", err)
	}
	t.Cleanup(func() { _ = joining.Stop(context.Background()) })

	snapshot, err := joining.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot(joining) error = %v", err)
	}
	if snapshot.Revision == 0 || snapshot.ControllerID != 1 {
		t.Fatalf("joining snapshot = %#v, want seed control snapshot", snapshot)
	}
	conn, err := net.DialTimeout("tcp", joinAddr, time.Second)
	if err != nil {
		t.Fatalf("dial joining mirror transport %s: %v", joinAddr, err)
	}
	_ = conn.Close()
}

func TestNodeDefaultSeedJoinMirrorSyncsThroughFollowerSeedRedirect(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "seed-join-follower-sync"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(seed node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startErrs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { startErrs <- node.Start(startCtx) }()
		t.Cleanup(func() { _ = node.Stop(context.Background()) })
	}
	for range nodes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start(seed cluster) error = %v", err)
		}
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitControllerWriteReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitControllerWriteReady() error = %v", err)
	}

	var followerAddr string
	for _, node := range nodes {
		runtime, ok := node.control.(*control.Runtime)
		if !ok {
			t.Fatalf("node %d control = %T, want *control.Runtime", node.NodeID(), node.control)
		}
		if runtime.LeaderID() != node.NodeID() {
			followerAddr = node.cfg.ListenAddr
			break
		}
	}
	if followerAddr == "" {
		t.Fatal("no Controller follower found")
	}

	joinAddr := freeTCPAddr(t)
	joining, err := New(Config{
		NodeID:     4,
		ListenAddr: joinAddr,
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "seed-join-follower-sync"},
		Join: JoinConfig{
			Seeds:         []string{followerAddr},
			AdvertiseAddr: joinAddr,
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 3},
	})
	if err != nil {
		t.Fatalf("New(joining) error = %v", err)
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinCancel()
	if err := joining.Start(joinCtx); err != nil {
		t.Fatalf("Start(joining) error = %v", err)
	}
	t.Cleanup(func() { _ = joining.Stop(context.Background()) })
}

func TestNodeDefaultControllerV2ForwardsControlWriteOverTransport(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "node-default-control-write"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startErrs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { startErrs <- node.Start(startCtx) }()
		t.Cleanup(func() { _ = node.Stop(context.Background()) })
	}
	for range nodes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitControllerWriteReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitControllerWriteReady() error = %v", err)
	}

	var follower *control.Runtime
	for _, node := range nodes {
		runtime, ok := node.control.(*control.Runtime)
		if !ok {
			t.Fatalf("node %d control = %T, want *control.Runtime", node.NodeID(), node.control)
		}
		if runtime.LeaderID() != node.NodeID() {
			follower = runtime
			break
		}
	}
	if follower == nil {
		t.Fatal("no follower runtime found")
	}

	result, err := follower.JoinNode(context.Background(), control.JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "n4",
		Roles:          []control.Role{control.RoleData},
		CapacityWeight: 2,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.NodeID != 4 || result.Node.JoinState != control.NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want forwarded joining node creation", result)
	}
}

func TestNodeStartMarksDefaultChannelsReadyWithoutController(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if snap := node.Snapshot(); !snap.ChannelsReady {
		t.Fatalf("Snapshot() ChannelsReady = false, want true")
	}
}

func TestNodeStopDiscardsDefaultChannelsForRestart(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after Stop, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after Stop, want discarded")
	}
}

type recordingCommitCoordinatorObserver struct{}

func findStorageMetricsSnapshot(snapshot StorageMetricsSnapshot, store string) (StorageEngineMetrics, bool) {
	for _, item := range snapshot.Stores {
		if item.Store == store {
			return item.Engine, true
		}
	}
	return StorageEngineMetrics{}, false
}

func (recordingCommitCoordinatorObserver) SetCommitCoordinatorQueueDepth(int) {}

func (recordingCommitCoordinatorObserver) ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent) {
}

func TestNodeStartFailureDiscardsDefaultChannels(t *testing.T) {
	boom := errors.New("boom")
	var calls []string
	node, err := New(validNodeConfig(t), withResources(namedTestResource("boom", &recordingResource{calls: &calls, startErr: boom})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after failed Start, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after failed Start, want discarded")
	}
}

type recordingProposer struct {
	calls    int
	last     propose.Request
	requests []propose.Request
	ctx      context.Context
}

func (p *recordingProposer) Propose(ctx context.Context, req propose.Request) error {
	p.calls++
	p.ctx = ctx
	p.last = req
	p.requests = append(p.requests, req)
	return nil
}
