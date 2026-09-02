package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeChannelRuntimeFacadesProjectHostedState(t *testing.T) {
	id := channelruntime.ChannelID{ID: "runtime-state", Type: 2}
	service := &channelStateContractService{
		snapshot: channelruntime.RuntimeSnapshot{ActiveTotal: 3},
		probe: channelruntime.RuntimeProbeResult{
			Checked: 1, LoadedLeader: 1,
			Channels: []channelruntime.RuntimeProbeChannel{{ChannelID: id, Role: channelruntime.RoleLeader}},
		},
		evict: channelruntime.RuntimeEvictResult{Requested: 1, Evicted: 1},
	}
	node := &Node{cfg: Config{NodeID: 7}, channels: service}
	node.started.Store(true)

	snapshot, err := node.ChannelRuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ChannelRuntimeSnapshot() error = %v", err)
	}
	if snapshot.NodeID != 7 || snapshot.ActiveTotal != 3 || service.snapshotCalls != 1 {
		t.Fatalf("runtime snapshot = %#v calls=%d, want node fallback and hosted totals", snapshot, service.snapshotCalls)
	}
	selector := channelruntime.RuntimeSelector{ChannelIDs: []channelruntime.ChannelID{id}}
	probe, err := node.ChannelRuntimeProbe(context.Background(), selector)
	if err != nil {
		t.Fatalf("ChannelRuntimeProbe() error = %v", err)
	}
	if probe.Checked != 1 || len(probe.Channels) != 1 || probe.Channels[0].ChannelID != id || !reflect.DeepEqual(service.lastProbe, selector) {
		t.Fatalf("runtime probe = %#v selector=%#v", probe, service.lastProbe)
	}
	evict, err := node.ChannelRuntimeEvict(context.Background(), selector)
	if err != nil {
		t.Fatalf("ChannelRuntimeEvict() error = %v", err)
	}
	if evict.Requested != 1 || evict.Evicted != 1 || !reflect.DeepEqual(service.lastEvict, selector) {
		t.Fatalf("runtime evict = %#v selector=%#v", evict, service.lastEvict)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.ChannelRuntimeProbe(canceled, selector); !errors.Is(err, context.Canceled) {
		t.Fatalf("ChannelRuntimeProbe(canceled) error = %v, want context.Canceled", err)
	}
	node.stopping.Store(true)
	if _, err := node.ChannelRuntimeSnapshot(context.Background()); !errors.Is(err, ErrStopping) {
		t.Fatalf("ChannelRuntimeSnapshot(stopping) error = %v, want ErrStopping", err)
	}
}

func TestNodeChannelRetentionFacadesPreserveBoundedApplyOptions(t *testing.T) {
	id := channelruntime.ChannelID{ID: "retention-state", Type: 2}
	view := channelruntime.RetentionView{ChannelID: id, LocalRetentionThroughSeq: 9, PhysicalRetentionThroughSeq: 7}
	applyResult := channelruntime.RetentionApplyResult{ChannelID: id, ThroughSeq: 9, Deleted: 3, DeletedThroughSeq: 7}
	service := &channelStateContractService{retention: view, apply: applyResult}
	node := &Node{cfg: Config{NodeID: 1}, channels: service}
	node.started.Store(true)

	gotView, err := node.ChannelRetentionView(context.Background(), id)
	if err != nil {
		t.Fatalf("ChannelRetentionView() error = %v", err)
	}
	if !reflect.DeepEqual(gotView, view) || service.lastRetentionID != id {
		t.Fatalf("retention view = %#v id=%#v, want hosted view", gotView, service.lastRetentionID)
	}
	opts := channelruntime.RetentionApplyOptions{MaxTrimMessages: 128, MaxTrimBytes: 4096}
	gotApply, err := node.ApplyChannelRetentionBoundary(context.Background(), id, 9, opts)
	if err != nil {
		t.Fatalf("ApplyChannelRetentionBoundary() error = %v", err)
	}
	if !reflect.DeepEqual(gotApply, applyResult) || service.lastApply.ChannelID != id || service.lastApply.ThroughSeq != 9 || service.lastApply.Options != opts {
		t.Fatalf("retention apply=%#v request=%#v", gotApply, service.lastApply)
	}

	missing := &Node{}
	missing.started.Store(true)
	if _, err := missing.ChannelRetentionView(context.Background(), id); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ChannelRetentionView(without service) error = %v, want ErrNotStarted", err)
	}
	if _, err := missing.ApplyChannelRetentionBoundary(context.Background(), id, 9, opts); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ApplyChannelRetentionBoundary(without service) error = %v, want ErrNotStarted", err)
	}
}

func TestLocalRestoreCacheFenceStaysClosedUntilResume(t *testing.T) {
	node := &Node{messageEventStreamCache: newMessageEventStreamCache(2)}
	event := messageEventCacheContractAppend("restore-cache", "message-1", "event-1", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"before"}`))
	if _, err := node.messageEventStreamCache.appendCached(event); err != nil {
		t.Fatal(err)
	}

	node.PauseLocalRestoreRuntime()
	if observation := node.messageEventStreamCache.observation(); observation.Sessions != 0 || observation.OpenLanes != 0 {
		t.Fatalf("paused cache = %#v, want cleared", observation)
	}
	if _, err := node.messageEventStreamCache.appendCached(event); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("append during restore error = %v, want ErrMaintenance", err)
	}
	node.ResetLocalRestoreCaches()
	if _, err := node.messageEventStreamCache.appendCached(event); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("append after reset-before-resume error = %v, want ErrMaintenance", err)
	}
	node.ResumeLocalRestoreRuntime()
	if _, err := node.messageEventStreamCache.appendCached(event); err != nil {
		t.Fatalf("append after resume error = %v", err)
	}
}

func TestRestoreMaintenanceReadinessAndPlacementFailClosed(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	node.cfg.Slots.HashSlotCount = 4
	node.cfg.Channel.ReplicaCount = 2
	node.defaultSlotMetaDB = &metadb.DB{}
	node.defaultChannelStore = &channelstore.MessageDBFactory{}
	node.maintenance.Store(true)
	if !node.RestoreMaintenanceReady() {
		t.Fatal("RestoreMaintenanceReady() = false with active maintenance and both stores")
	}
	if err := node.validateRestoreStorage(0); err != nil {
		t.Fatalf("validateRestoreStorage(0) error = %v", err)
	}
	if err := node.validateRestoreStorage(4); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("validateRestoreStorage(out of range) error = %v, want ErrMaintenance", err)
	}

	resolver, err := node.restoreChannelPlacement(0)
	if err != nil {
		t.Fatalf("restoreChannelPlacement() error = %v", err)
	}
	target, err := resolver.ResolveChannelPlacement(context.Background(), channelruntime.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2})
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if len(target.Replicas) != 2 || target.MinISR <= 0 {
		t.Fatalf("restore placement = %#v, want two bounded replicas", target)
	}

	route, err := node.router.RouteHashSlot(0)
	if err != nil {
		t.Fatal(err)
	}
	dataNodes := restoreSlotDataNodes{revision: route.Revision, nodes: []uint64{1, 2}}
	if nodes, err := dataNodes.PlacementDataNodes(context.Background(), route.Revision); err != nil || !reflect.DeepEqual(nodes, []uint64{1, 2}) {
		t.Fatalf("PlacementDataNodes() = %#v err=%v", nodes, err)
	}
	if _, err := dataNodes.PlacementDataNodes(context.Background(), route.Revision+1); !errors.Is(err, channelruntime.ErrStaleMeta) {
		t.Fatalf("PlacementDataNodes(stale revision) error = %v, want ErrStaleMeta", err)
	}

	node.stopping.Store(true)
	if node.RestoreMaintenanceReady() {
		t.Fatal("RestoreMaintenanceReady() = true while stopping")
	}
	node.stopping.Store(false)
	node.cfg.Channel.ReplicaCount = 0
	if _, err := node.restoreChannelPlacement(0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("restoreChannelPlacement(replica count 0) error = %v, want ErrInvalidConfig", err)
	}
}

type channelStateContractService struct {
	noopChannelService
	snapshot  channelruntime.RuntimeSnapshot
	probe     channelruntime.RuntimeProbeResult
	evict     channelruntime.RuntimeEvictResult
	retention channelruntime.RetentionView
	apply     channelruntime.RetentionApplyResult

	snapshotCalls   int
	lastProbe       channelruntime.RuntimeSelector
	lastEvict       channelruntime.RuntimeSelector
	lastRetentionID channelruntime.ChannelID
	lastApply       channelruntime.RetentionApplyRequest
}

func (s *channelStateContractService) RuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error) {
	s.snapshotCalls++
	return s.snapshot, nil
}

func (s *channelStateContractService) RuntimeProbe(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	s.lastProbe = selector
	return s.probe, nil
}

func (s *channelStateContractService) RuntimeEvict(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	s.lastEvict = selector
	return s.evict, nil
}

func (s *channelStateContractService) RetentionView(_ context.Context, id channelruntime.ChannelID) (channelruntime.RetentionView, error) {
	s.lastRetentionID = id
	return s.retention, nil
}

func (s *channelStateContractService) ApplyRetentionBoundary(_ context.Context, req channelruntime.RetentionApplyRequest) (channelruntime.RetentionApplyResult, error) {
	s.lastApply = req
	return s.apply, nil
}
