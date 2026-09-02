package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestNodeBackupAndOpsMCPStateUseRevisionFencedController(t *testing.T) {
	state := controller.ClusterState{Revision: 9, ClusterID: "backup-state-contract"}
	controlSource := &backupStateController{
		StaticController: control.NewStaticController(control.Snapshot{ControllerID: 3}),
		state:            state,
		raftStatus:       control.ControllerRaftStatus{LeaderID: 3, Term: 7},
	}
	node := &Node{control: controlSource}
	node.started.Store(true)

	if got := node.BackupControllerLeaderID(); got != 3 {
		t.Fatalf("BackupControllerLeaderID() = %d, want 3", got)
	}
	leader, term, err := node.BackupControllerFence(context.Background())
	if err != nil {
		t.Fatalf("BackupControllerFence() error = %v", err)
	}
	if leader != 3 || term != 7 {
		t.Fatalf("BackupControllerFence() = (%d,%d), want (3,7)", leader, term)
	}

	gotState, err := node.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if gotState.Revision != state.Revision || gotState.ClusterID != state.ClusterID {
		t.Fatalf("LocalState() = %#v, want revision-fenced state", gotState)
	}
	backupReplacement := controller.ScheduledBackupState{}
	if err := node.ReplaceScheduledBackupState(context.Background(), 9, backupReplacement); err != nil {
		t.Fatalf("ReplaceScheduledBackupState() error = %v", err)
	}
	if controlSource.backupRevision != 9 || !reflect.DeepEqual(controlSource.backupReplacement, backupReplacement) {
		t.Fatalf("backup replacement revision=%d state=%#v", controlSource.backupRevision, controlSource.backupReplacement)
	}

	gotMCPState, err := node.LoadOpsMCPState(context.Background())
	if err != nil {
		t.Fatalf("LoadOpsMCPState() error = %v", err)
	}
	if gotMCPState.Revision != state.Revision {
		t.Fatalf("LoadOpsMCPState().Revision = %d, want %d", gotMCPState.Revision, state.Revision)
	}
	mcpReplacement := controller.OpsMCPState{Enabled: true, OwnerNodeID: 2}
	if err := node.ReplaceOpsMCPState(context.Background(), 9, mcpReplacement); err != nil {
		t.Fatalf("ReplaceOpsMCPState() error = %v", err)
	}
	if controlSource.mcpRevision != 9 || !reflect.DeepEqual(controlSource.mcpReplacement, mcpReplacement) {
		t.Fatalf("MCP replacement revision=%d state=%#v", controlSource.mcpRevision, controlSource.mcpReplacement)
	}

	controlSource.mcpErr = controller.ErrNotLeader
	if err := node.ReplaceOpsMCPState(context.Background(), 10, mcpReplacement); !errors.Is(err, ErrNotLeader) || !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("ReplaceOpsMCPState(not leader) error = %v, want normalized and preserved leadership error", err)
	}
}

func TestNodeBackupAndOpsMCPStateFailClosedAtLifecycleBoundary(t *testing.T) {
	unsupported := &Node{control: control.NewStaticController(control.Snapshot{})}
	unsupported.started.Store(true)
	if _, err := unsupported.LocalState(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("LocalState(unsupported) error = %v", err)
	}
	if err := unsupported.ReplaceScheduledBackupState(context.Background(), 1, controller.ScheduledBackupState{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("ReplaceScheduledBackupState(unsupported) error = %v", err)
	}
	if _, err := unsupported.LoadOpsMCPState(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("LoadOpsMCPState(unsupported) error = %v", err)
	}
	if err := unsupported.ReplaceOpsMCPState(context.Background(), 1, controller.OpsMCPState{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("ReplaceOpsMCPState(unsupported) error = %v", err)
	}

	controlSource := &backupStateController{
		StaticController: control.NewStaticController(control.Snapshot{}),
		raftStatus:       control.ControllerRaftStatus{},
	}
	node := &Node{control: controlSource}
	node.started.Store(true)
	if _, _, err := node.BackupControllerFence(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("BackupControllerFence(without term) error = %v, want ErrNotLeader", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := node.BackupControllerFence(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("BackupControllerFence(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := node.LoadOpsMCPState(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadOpsMCPState(canceled) error = %v, want context.Canceled", err)
	}
	if err := node.ReplaceOpsMCPState(canceled, 1, controller.OpsMCPState{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReplaceOpsMCPState(canceled) error = %v, want context.Canceled", err)
	}

	node.stopping.Store(true)
	if _, _, err := node.BackupControllerFence(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("BackupControllerFence(stopping) error = %v, want ErrNotStarted", err)
	}
	if got := (*Node)(nil).BackupControllerLeaderID(); got != 0 {
		t.Fatalf("nil BackupControllerLeaderID() = %d, want 0", got)
	}
}

func TestOpenBackupMessageSnapshotUsesExactRuntimeAndDurableCuts(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	channelIDs := distinctChannelIDsForHashSlot(t, 4, 0, 2)
	first := channelruntime.ChannelID{ID: channelIDs[0], Type: 2}
	second := channelruntime.ChannelID{ID: channelIDs[1], Type: 2}
	factory := newRecordingBackupFactory(t, map[channelruntime.ChannelID]backupStoreState{
		first:  {records: 5, checkpointHW: 4, retentionThrough: 2},
		second: {records: 2, checkpointHW: 1, retentionThrough: 9},
	})
	factory.stats = channelstore.BackupSnapshotStats{
		HashSlot: 0, ChannelCount: 2, MessageCount: 5, MaxMessageID: 105,
	}
	runtime := &backupProbeChannelService{probe: channelruntime.RuntimeProbeResult{
		Checked: 2, LoadedLeader: 1,
		Channels: []channelruntime.RuntimeProbeChannel{{
			ChannelID: first, ChannelEpoch: 11, LeaderEpoch: 4,
			Role: channelruntime.RoleLeader, Status: channelruntime.StatusActive,
			LEO: 5, HW: 3, CheckpointHW: 4,
		}},
		Missing: []channelruntime.ChannelID{second},
	}}
	node.channels = runtime
	node.channelStoreFactory = factory

	snapshot, err := node.OpenBackupMessageSnapshot(context.Background(), 0, []BackupChannelFence{
		{ChannelID: first.ID, ChannelType: first.Type, LeaderNodeID: 1, ChannelEpoch: 11, LeaderEpoch: 4, MinISR: 2, RetentionThroughSeq: 2},
		{ChannelID: second.ID, ChannelType: second.Type, LeaderNodeID: 1, ChannelEpoch: 12, LeaderEpoch: 5, MinISR: 1, RetentionThroughSeq: 9},
	})
	if err != nil {
		t.Fatalf("OpenBackupMessageSnapshot() error = %v", err)
	}
	if runtime.calls != 1 || !reflect.DeepEqual(runtime.selector.ChannelIDs, []channelruntime.ChannelID{first, second}) {
		t.Fatalf("runtime probe calls=%d selector=%#v", runtime.calls, runtime.selector)
	}
	wantCuts := []channelstore.BackupChannelCut{
		{Key: channelruntime.ChannelKeyForID(first), ID: first, Epoch: 11, LogStartOffset: 2, HW: 3},
		{Key: channelruntime.ChannelKeyForID(second), ID: second, Epoch: 12, LogStartOffset: 9, HW: 9},
	}
	if factory.calls != 1 || factory.request.HashSlot != 0 || !reflect.DeepEqual(factory.request.Channels, wantCuts) {
		t.Fatalf("backup request calls=%d request=%#v, want cuts %#v", factory.calls, factory.request, wantCuts)
	}
	wantBoundaries := []BackupChannelBoundary{
		{ChannelID: first.ID, ChannelType: first.Type, Epoch: 11, LogStartOffset: 2, HW: 3},
		{ChannelID: second.ID, ChannelType: second.Type, Epoch: 12, LogStartOffset: 9, HW: 9},
	}
	if !reflect.DeepEqual(snapshot.Boundaries, wantBoundaries) || snapshot.MessageRecords != 5 || snapshot.MaxMessageID != 105 {
		t.Fatalf("backup snapshot = %#v, want exact boundaries and stats", snapshot)
	}
	if snapshot.Reader != factory.reader {
		t.Fatal("backup reader identity changed")
	}
	if err := snapshot.Reader.Close(); err != nil {
		t.Fatalf("snapshot reader close error = %v", err)
	}
}

func TestOpenBackupMessageSnapshotRejectsDriftAndClosesUntrustedStream(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	channelID := channelruntime.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	factory := newRecordingBackupFactory(t, map[channelruntime.ChannelID]backupStoreState{
		channelID: {records: 3, checkpointHW: 2, retentionThrough: 1},
	})
	node.channelStoreFactory = factory
	node.channels = &backupProbeChannelService{probe: channelruntime.RuntimeProbeResult{Channels: []channelruntime.RuntimeProbeChannel{{
		ChannelID: channelID, ChannelEpoch: 7, LeaderEpoch: 2,
		Role: channelruntime.RoleFollower, LEO: 3, HW: 2,
	}}}}
	fence := BackupChannelFence{
		ChannelID: channelID.ID, ChannelType: channelID.Type, LeaderNodeID: 1,
		ChannelEpoch: 7, LeaderEpoch: 2, MinISR: 2,
	}
	if _, err := node.OpenBackupMessageSnapshot(context.Background(), 0, []BackupChannelFence{fence}); !errors.Is(err, channelruntime.ErrStaleMeta) {
		t.Fatalf("OpenBackupMessageSnapshot(follower runtime) error = %v, want ErrStaleMeta", err)
	}
	if factory.calls != 0 {
		t.Fatalf("snapshot factory calls after runtime drift = %d, want 0", factory.calls)
	}

	node.channels = &backupProbeChannelService{probe: channelruntime.RuntimeProbeResult{Channels: []channelruntime.RuntimeProbeChannel{{
		ChannelID: channelID, ChannelEpoch: 7, LeaderEpoch: 2,
		Role: channelruntime.RoleLeader, LEO: 3, HW: 2,
	}}}}
	factory.reader = &trackingReadCloser{Reader: bytes.NewReader([]byte("untrusted"))}
	factory.stats = channelstore.BackupSnapshotStats{HashSlot: 0, ChannelCount: 2}
	if _, err := node.OpenBackupMessageSnapshot(context.Background(), 0, []BackupChannelFence{fence}); !errors.Is(err, channelruntime.ErrStaleMeta) {
		t.Fatalf("OpenBackupMessageSnapshot(mismatched stats) error = %v, want ErrStaleMeta", err)
	}
	if !factory.reader.closed {
		t.Fatal("mismatched snapshot stream was not closed")
	}
}

func TestOpenBackupMessageSnapshotValidatesFenceBeforeReadingStores(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	channelID := keyForNodeHashSlot(t, 4, 0)
	factory := newRecordingBackupFactory(t, nil)
	node.channelStoreFactory = factory
	node.channels = &backupProbeChannelService{}

	for _, tc := range []struct {
		name     string
		hashSlot uint16
		fence    BackupChannelFence
		want     error
	}{
		{name: "empty identity", hashSlot: 0, fence: BackupChannelFence{LeaderNodeID: 1, ChannelEpoch: 1, LeaderEpoch: 1, MinISR: 1}, want: channelruntime.ErrStaleMeta},
		{name: "wrong leader", hashSlot: 0, fence: BackupChannelFence{ChannelID: channelID, ChannelType: 2, LeaderNodeID: 2, ChannelEpoch: 1, LeaderEpoch: 1, MinISR: 1}, want: channelruntime.ErrStaleMeta},
		{name: "wrong hash slot", hashSlot: 3, fence: BackupChannelFence{ChannelID: channelID, ChannelType: 2, LeaderNodeID: 1, ChannelEpoch: 1, LeaderEpoch: 1, MinISR: 1}, want: channelruntime.ErrStaleMeta},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := node.OpenBackupMessageSnapshot(context.Background(), tc.hashSlot, []BackupChannelFence{tc.fence}); !errors.Is(err, tc.want) {
				t.Fatalf("OpenBackupMessageSnapshot() error = %v, want %v", err, tc.want)
			}
		})
	}
	if factory.calls != 0 {
		t.Fatalf("invalid fences opened %d backup streams, want 0", factory.calls)
	}
}

type backupStateController struct {
	*control.StaticController
	state controller.ClusterState

	raftStatus control.ControllerRaftStatus
	raftErr    error

	backupRevision    uint64
	backupReplacement controller.ScheduledBackupState
	backupErr         error
	mcpRevision       uint64
	mcpReplacement    controller.OpsMCPState
	mcpErr            error
}

func (c *backupStateController) ControllerRaftStatus(context.Context) (control.ControllerRaftStatus, error) {
	return c.raftStatus, c.raftErr
}

func (c *backupStateController) CompactControllerRaftLog(context.Context) (control.ControllerRaftCompactionResult, error) {
	return control.ControllerRaftCompactionResult{}, nil
}

func (c *backupStateController) LocalControllerState(ctx context.Context) (controller.ClusterState, error) {
	if err := ctx.Err(); err != nil {
		return controller.ClusterState{}, err
	}
	return c.state.Clone(), nil
}

func (c *backupStateController) ReplaceScheduledBackupState(_ context.Context, expectedRevision uint64, replacement controller.ScheduledBackupState) error {
	c.backupRevision = expectedRevision
	c.backupReplacement = replacement.Clone()
	return c.backupErr
}

func (c *backupStateController) ReplaceOpsMCPState(_ context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	c.mcpRevision = expectedRevision
	c.mcpReplacement = replacement.Clone()
	return c.mcpErr
}

type backupProbeChannelService struct {
	noopChannelService
	probe    channelruntime.RuntimeProbeResult
	err      error
	calls    int
	selector channelruntime.RuntimeSelector
}

func (s *backupProbeChannelService) RuntimeProbe(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	s.calls++
	s.selector = selector
	return s.probe, s.err
}

type backupStoreState struct {
	records          int
	checkpointHW     uint64
	retentionThrough uint64
}

type recordingBackupFactory struct {
	stores  map[channelruntime.ChannelID]channelstore.ChannelStore
	reader  *trackingReadCloser
	stats   channelstore.BackupSnapshotStats
	err     error
	calls   int
	request channelstore.BackupSnapshotRequest
}

func newRecordingBackupFactory(t *testing.T, states map[channelruntime.ChannelID]backupStoreState) *recordingBackupFactory {
	t.Helper()
	factory := &recordingBackupFactory{
		stores: make(map[channelruntime.ChannelID]channelstore.ChannelStore),
		reader: &trackingReadCloser{Reader: bytes.NewReader([]byte("backup"))},
	}
	for id, state := range states {
		memory := channelstore.NewMemoryFactory()
		store, err := memory.ChannelStore(channelruntime.ChannelKeyForID(id), id)
		if err != nil {
			t.Fatalf("ChannelStore(%v) error = %v", id, err)
		}
		records := make([]channelruntime.Record, state.records)
		for index := range records {
			records[index] = channelruntime.Record{ID: uint64(100 + index), Payload: []byte{byte(index)}}
		}
		if _, err := store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: records}); err != nil {
			t.Fatalf("AppendLeader(%v) error = %v", id, err)
		}
		if err := store.StoreCheckpoint(context.Background(), channelruntime.Checkpoint{HW: state.checkpointHW}); err != nil {
			t.Fatalf("StoreCheckpoint(%v) error = %v", id, err)
		}
		if state.retentionThrough > 0 {
			if _, err := store.AdoptRetentionBoundary(context.Background(), state.retentionThrough, "backup"); err != nil {
				t.Fatalf("AdoptRetentionBoundary(%v) error = %v", id, err)
			}
		}
		factory.stores[id] = store
	}
	return factory
}

func (f *recordingBackupFactory) ChannelStore(_ channelruntime.ChannelKey, id channelruntime.ChannelID) (channelstore.ChannelStore, error) {
	store := f.stores[id]
	if store == nil {
		return nil, channelruntime.ErrChannelNotFound
	}
	return store, nil
}

func (f *recordingBackupFactory) OpenBackupSnapshotWithStats(_ context.Context, request channelstore.BackupSnapshotRequest) (io.ReadCloser, channelstore.BackupSnapshotStats, error) {
	f.calls++
	f.request = request
	return f.reader, f.stats, f.err
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func distinctChannelIDsForHashSlot(t *testing.T, count, want uint16, needed int) []string {
	t.Helper()
	result := make([]string, 0, needed)
	for index := 0; len(result) < needed && index < 100_000; index++ {
		candidate := fmt.Sprintf("backup-channel-%d", index)
		if routing.HashSlotForKey(candidate, count) == want {
			result = append(result, candidate)
		}
	}
	if len(result) != needed {
		t.Fatalf("found %d channel IDs for hash slot %d, want %d", len(result), want, needed)
	}
	return result
}
