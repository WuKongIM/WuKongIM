package channels

import (
	"context"
	"slices"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestRepairScannerScansOnlyLocalLeaderSlots(t *testing.T) {
	id := ch.ChannelID{ID: "scan-local", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.localSlots = []uint32{2}
	source.slotPages[2] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	store := &fakeRepairScannerStore{}

	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 10}, source, store)
	_, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, []uint32{2}, source.scannedSlots)
	require.Len(t, store.requests, 1)
}

func TestRepairScannerUsesBoundedPageSizeAndMaxPages(t *testing.T) {
	id := ch.ChannelID{ID: "scan-pages", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.localSlots = []uint32{1}
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{
		{failoverPlannerMeta(ch.ChannelID{ID: "scan-page-a", Type: 1})},
		{failoverPlannerMeta(ch.ChannelID{ID: "scan-page-b", Type: 1})},
		{failoverPlannerMeta(ch.ChannelID{ID: "scan-page-c", Type: 1})},
	}

	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 3, MaxPagesPerTick: 2, MaxTasksPerTick: 10}, source, &fakeRepairScannerStore{})
	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 2, result.PagesScanned)
	require.Equal(t, []int{3, 3}, source.pageLimits)
}

func TestRepairScannerSuppressesDuplicateActiveMigration(t *testing.T) {
	id := ch.ChannelID{ID: "scan-active", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.active[id] = true
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	store := &fakeRepairScannerStore{}

	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 10}, source, store)
	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, result.TasksCreated)
	require.Empty(t, store.requests)
}

func TestRepairScannerEmitsBlockedReasonsWithoutCreatingTasks(t *testing.T) {
	id := ch.ChannelID{ID: "scan-blocked", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	source.probes[2] = ch.RuntimeProbeChannel{}
	source.probes[3] = ch.RuntimeProbeChannel{}
	store := &fakeRepairScannerStore{}

	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 10}, source, store)
	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, result.TasksCreated)
	require.Len(t, result.Blocked, 1)
	require.Equal(t, "no_safe_candidate", result.Blocked[0].Reason)
	require.Empty(t, store.requests)
}

func TestRepairScannerRespectsMaxTasksPerTick(t *testing.T) {
	first := ch.ChannelID{ID: "scan-limit-a", Type: 1}
	second := ch.ChannelID{ID: "scan-limit-b", Type: 1}
	source := newFakeRepairScannerSource(first, second)
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(first), failoverPlannerMeta(second)}}
	store := &fakeRepairScannerStore{}

	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)
	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, result.TasksCreated)
	require.Len(t, store.requests, 1)
}

func TestRepairScannerConfigKeepsTickIntervalForSchedulerLoop(t *testing.T) {
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, TickInterval: 3 * time.Second}, newFakeRepairScannerSource(), &fakeRepairScannerStore{})

	require.Equal(t, 3*time.Second, scanner.cfg.TickInterval)
}

type fakeRepairScannerSource struct {
	localSlots   []uint32
	slotPages    map[uint32][][]metadb.ChannelRuntimeMeta
	active       map[ch.ChannelID]bool
	probes       map[uint64]ch.RuntimeProbeChannel
	scannedSlots []uint32
	pageLimits   []int
}

func newFakeRepairScannerSource(ids ...ch.ChannelID) *fakeRepairScannerSource {
	source := &fakeRepairScannerSource{
		localSlots: []uint32{1},
		slotPages:  make(map[uint32][][]metadb.ChannelRuntimeMeta),
		active:     make(map[ch.ChannelID]bool),
		probes:     make(map[uint64]ch.RuntimeProbeChannel),
	}
	for _, id := range ids {
		source.probes[2] = failoverProbe(id, 2, 11, 20, 10, 10).Probe
		source.probes[3] = failoverProbe(id, 3, 11, 20, 9, 9).Probe
	}
	return source
}

func (s *fakeRepairScannerSource) LocalLeaderSlotIDs(context.Context) ([]uint32, error) {
	return append([]uint32(nil), s.localSlots...), nil
}

func (s *fakeRepairScannerSource) ListChannelRuntimeMetaPage(_ context.Context, slotID uint32, cursor metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if !slices.Contains(s.scannedSlots, slotID) {
		s.scannedSlots = append(s.scannedSlots, slotID)
	}
	s.pageLimits = append(s.pageLimits, limit)
	pages := s.slotPages[slotID]
	index := int(cursor.ChannelType)
	if index >= len(pages) {
		return nil, metadb.ChannelRuntimeMetaCursor{}, true, nil
	}
	next := metadb.ChannelRuntimeMetaCursor{ChannelType: int64(index + 1)}
	return pages[index], next, index == len(pages)-1, nil
}

func (s *fakeRepairScannerSource) ActiveChannelMigration(_ context.Context, id ch.ChannelID) (bool, error) {
	return s.active[id], nil
}

func (s *fakeRepairScannerSource) ProbeChannel(_ context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error) {
	probe := s.probes[nodeID]
	probe.ChannelID = ch.ChannelID{ID: channelID, Type: channelType}
	return probe, nil
}

func (s *fakeRepairScannerSource) ControlSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{Nodes: failoverHealthyNodes(2, 3)}, nil
}

type fakeRepairScannerStore struct {
	requests []CreateLeaderFailoverRequest
}

func (s *fakeRepairScannerStore) CreateLeaderFailover(_ context.Context, req CreateLeaderFailoverRequest) (metadb.ChannelMigrationTask, error) {
	s.requests = append(s.requests, req)
	return metadb.ChannelMigrationTask{TaskID: "task-" + req.ChannelID.ID}, nil
}
