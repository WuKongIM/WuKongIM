package cluster

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
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
	node.channelDataNodes.Update([]uint64{1})
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

func TestNodeProbeWriteReadyBoundsPhysicalSlotProbes(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	slots := make([]control.SlotAssignment, 0, 6)
	ranges := make([]control.HashSlotRange, 0, 6)
	statuses := make([]routing.SlotStatus, 0, 6)
	for slotID := uint32(1); slotID <= 6; slotID++ {
		slots = append(slots, control.SlotAssignment{SlotID: slotID, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 2})
		ranges = append(ranges, control.HashSlotRange{From: uint16(slotID - 1), To: uint16(slotID - 1), SlotID: slotID})
		leader := uint64(2)
		if slotID == 3 {
			leader = 1
		}
		statuses = append(statuses, routing.SlotStatus{SlotID: slotID, Leader: leader})
	}
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     slots,
		HashSlots: control.HashSlotTable{Revision: 1, Count: 6, Ranges: ranges},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders(statuses)
	node.snapshot = Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 6, HashSlotCount: 6}
	node.channelDataNodes.Update([]uint64{1})
	node.started.Store(true)

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}
	if proposer.calls != maxWriteProbePhysicalSlots {
		t.Fatalf("proposer calls=%d, want bounded probe count %d", proposer.calls, maxWriteProbePhysicalSlots)
	}
	got := make([]uint32, 0, len(proposer.requests))
	for _, req := range proposer.requests {
		got = append(got, req.Target.SlotID)
	}
	want := []uint32{1, 2, 3, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe slot ids = %v, want %v", got, want)
	}
}

func TestNodeProbeWriteReadyRequiresChannelPlacementDataNodes(t *testing.T) {
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
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1},
		},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{
			{From: 0, To: 0, SlotID: 1},
		}},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	node.snapshot = Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 1, HashSlotCount: 1}
	node.started.Store(true)

	err = node.ProbeWriteReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "channel placement candidates 0 below replica count 1") {
		t.Fatalf("ProbeWriteReady() error = %v, want channel placement candidate readiness error", err)
	}
	if proposer.calls != 0 {
		t.Fatalf("proposer calls = %d, want no Slot probe before channel placement candidates are ready", proposer.calls)
	}
}

func TestNodeProbeWriteReadyMarksChannelDataPlaneLeaseAfterSuccessfulProbe(t *testing.T) {
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
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1},
		},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{
			{From: 0, To: 0, SlotID: 1},
		}},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	node.snapshot = Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 1, HashSlotCount: 1}
	node.channelDataNodes.Update([]uint64{1})
	node.channels = noopChannelService{}
	node.started.Store(true)

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls = %d, want one Slot probe before marking channel data plane lease", proposer.calls)
	}
	if lease := node.channelDataPlaneLease.snapshot(); !lease.ready {
		t.Fatalf("channel data plane lease ready = false, want ProbeWriteReady to mark it visible")
	}
}

func TestNodeProbeWriteReadyRefreshesControlSnapshotForChannelPlacementDataNodes(t *testing.T) {
	proposer := &recordingProposer{}
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1},
		},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{
			{From: 0, To: 0, SlotID: 1},
		}},
	}
	controller := newLocalSnapshotController(snapshot)
	node, err := New(validNodeConfig(t), WithProposer(proposer), withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.channels = noopChannelService{}
	if err := node.applySnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	node.channelDataPlaneLease.MarkVisible(time.Now())
	node.started.Store(true)

	ready := snapshot.Clone()
	ready.Nodes[0].Health = control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}
	controller.setSnapshot(ready)

	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatalf("ProbeWriteReady() error = %v", err)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposer calls = %d, want Slot probe after refreshed channel placement candidates", proposer.calls)
	}
	if got := node.channelDataNodes.DataNodes(); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("channel data nodes = %v, want refreshed candidate", got)
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

func TestNodeDefaultChannelsReceiveDataPlaneLeaseGuard(t *testing.T) {
	node, err := New(validNodeConfig(t), withController(control.NewStaticController(nodeControlSnapshot())), WithProposer(&recordingProposer{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node.ensureDefaultRuntime()
	if err != nil {
		t.Fatalf("ensureDefaultRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if node.channels != nil {
			_ = node.channels.Close()
		}
		if node.defaultChannelStore != nil {
			_ = node.defaultChannelStore.Close()
		}
	})
	service, ok := node.channels.(*channelwrapper.Service)
	if !ok {
		t.Fatalf("default channels = %T, want *channels.Service", node.channels)
	}
	meta := channelv2.Meta{
		Key:         channelv2.ChannelKey("1:lease-default"),
		ID:          channelv2.ChannelID{ID: "lease-default", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1},
		ISR:         []channelv2.NodeID{1},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}
	if err := service.ApplyMeta(meta); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	_, err = service.Runtime().Append(context.Background(), channelv2.AppendRequest{ChannelID: meta.ID, Message: channelv2.Message{MessageID: 1, Payload: []byte("blocked")}, CommitMode: channelv2.CommitModeLocal})
	if !errors.Is(err, channelv2.ErrNotReady) {
		t.Fatalf("Append() before lease mark error = %v, want ErrNotReady", err)
	}

	node.channelDataPlaneLease.MarkVisible(time.Now())
	res, err := service.Runtime().Append(context.Background(), channelv2.AppendRequest{ChannelID: meta.ID, Message: channelv2.Message{MessageID: 2, Payload: []byte("allowed")}, CommitMode: channelv2.CommitModeLocal})
	if err != nil {
		t.Fatalf("Append() after lease mark error = %v", err)
	}
	if res.MessageSeq != 1 {
		t.Fatalf("Append() seq = %d, want 1", res.MessageSeq)
	}
}

func TestNodeDefaultChannelsExposeMigrationStore(t *testing.T) {
	node, err := New(validNodeConfig(t), withController(control.NewStaticController(nodeControlSnapshot())))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node.ensureDefaultRuntime()
	if err != nil {
		t.Fatalf("ensureDefaultRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if node.channels != nil {
			_ = node.channels.Close()
		}
		if node.defaultChannelStore != nil {
			_ = node.defaultChannelStore.Close()
		}
	})
	service, ok := node.channels.(*channelwrapper.Service)
	if !ok {
		t.Fatalf("default channels = %T, want *channels.Service", node.channels)
	}
	if service.MigrationStore() == nil {
		t.Fatal("MigrationStore() = nil, want default Slot-backed migration facade")
	}
	if got := node.ChannelMigrationStore(); got != service.MigrationStore() {
		t.Fatalf("ChannelMigrationStore() = %p, want service migration store %p", got, service.MigrationStore())
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

func TestChannelMigrationDefaultsEnabledWithBoundedWork(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	if !cfg.ChannelMigration.Enabled {
		t.Fatal("ChannelMigration.Enabled = false, want enabled by default")
	}
	if cfg.ChannelMigration.ScanInterval <= 0 {
		t.Fatalf("ChannelMigration.ScanInterval = %v, want positive", cfg.ChannelMigration.ScanInterval)
	}
	if cfg.ChannelMigration.ScanLimit <= 0 {
		t.Fatalf("ChannelMigration.ScanLimit = %d, want positive", cfg.ChannelMigration.ScanLimit)
	}
	if cfg.ChannelMigration.MaxTasksPerTick <= 0 {
		t.Fatalf("ChannelMigration.MaxTasksPerTick = %d, want positive", cfg.ChannelMigration.MaxTasksPerTick)
	}
}

func TestChannelMigrationConfigRejectsNegativeBounds(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.ChannelMigration.ScanInterval = -time.Second

	if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestNodeStartHostsChannelMigrationLoopWhenEnabled(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Hour
	cfg.ChannelMigration.Enabled = true
	cfg.ChannelMigration.ScanInterval = time.Hour
	cfg.ChannelMigration.ScanLimit = 1
	cfg.ChannelMigration.MaxTasksPerTick = 1

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if node.channelMigrationCancel == nil {
		t.Fatal("channelMigrationCancel = nil, want migration loop running")
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if node.channelMigrationCancel != nil {
		t.Fatal("channelMigrationCancel retained after Stop, want nil")
	}
}

func TestNodeInitializesDefaultControllerWhenOptionMissing(t *testing.T) {
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
		t.Fatalf("Snapshot() = %#v, want Controller-backed ready routes", snap)
	}
	route, err := node.RouteHashSlot(0)
	if err != nil && !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want route or no slot leader", err)
	}
	if err == nil && route.SlotID != 1 {
		t.Fatalf("RouteHashSlot() = %#v, want slot 1", route)
	}
}

func TestNodeDefaultControllerThreeVotersConvergeOverTransport(t *testing.T) {
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

func TestNodeDefaultControllerForwardsControlWriteOverTransport(t *testing.T) {
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

type localSnapshotController struct {
	mu       sync.Mutex
	snapshot control.Snapshot
	watch    chan control.SnapshotEvent
}

func newLocalSnapshotController(snapshot control.Snapshot) *localSnapshotController {
	return &localSnapshotController{snapshot: snapshot.Clone(), watch: make(chan control.SnapshotEvent)}
}

func (c *localSnapshotController) setSnapshot(snapshot control.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = snapshot.Clone()
}

func (c *localSnapshotController) Start(context.Context) error { return nil }

func (c *localSnapshotController) Stop(context.Context) error { return nil }

func (c *localSnapshotController) LocalSnapshot(context.Context) (control.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot.Clone(), nil
}

func (c *localSnapshotController) LeaderID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot.ControllerID
}

func (c *localSnapshotController) ReportNode(context.Context, control.NodeReport) error { return nil }

func (c *localSnapshotController) ReportSlots(context.Context, control.SlotRuntimeReport) error {
	return nil
}

func (c *localSnapshotController) CompleteTask(context.Context, control.TaskResult) error { return nil }

func (c *localSnapshotController) FailTask(context.Context, control.TaskResult) error { return nil }

func (c *localSnapshotController) ReportTaskProgress(context.Context, control.TaskProgress) error {
	return nil
}

func (c *localSnapshotController) AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error {
	return nil
}

func (c *localSnapshotController) CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error {
	return nil
}

func (c *localSnapshotController) RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	return control.SlotLeaderTransferResult{}, nil
}

func (c *localSnapshotController) RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	return control.SlotReplicaMoveResult{}, nil
}

func (c *localSnapshotController) Watch() <-chan control.SnapshotEvent { return c.watch }

type noopChannelService struct{}

func (noopChannelService) Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error) {
	return channelv2.AppendResult{}, nil
}

func (noopChannelService) AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	return channelv2.AppendBatchResult{}, nil
}

func (noopChannelService) ResolveAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error) {
	return channelv2.Meta{}, nil
}

func (noopChannelService) ReadChannelLastVisible(context.Context, channelv2.ChannelID, uint64) (channelv2.Message, bool, error) {
	return channelv2.Message{}, false, nil
}

func (noopChannelService) RetentionView(context.Context, channelv2.ChannelID) (channelv2.RetentionView, error) {
	return channelv2.RetentionView{}, nil
}

func (noopChannelService) ApplyRetentionBoundary(context.Context, channelv2.RetentionApplyRequest) (channelv2.RetentionApplyResult, error) {
	return channelv2.RetentionApplyResult{}, nil
}

func (noopChannelService) RuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error) {
	return channelv2.RuntimeSnapshot{}, nil
}

func (noopChannelService) RuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	return channelv2.RuntimeProbeResult{}, nil
}

func (noopChannelService) RuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	return channelv2.RuntimeEvictResult{}, nil
}

func (noopChannelService) DrainChannel(context.Context, channelv2.DrainChannelRequest) (channelv2.DrainChannelResult, error) {
	return channelv2.DrainChannelResult{}, nil
}

func (noopChannelService) Tick(context.Context) error { return nil }

func (noopChannelService) Close() error { return nil }
