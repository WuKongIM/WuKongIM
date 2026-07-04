package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	controllerraft "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestControllerRaftStatusOnNodeMergesLocalServiceStatusAndDurableIndexes(t *testing.T) {
	db, err := raftstorage.Open(filepath.Join(t.TempDir(), "controller-raft"), raftstorage.Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.ForController()
	hs := raftpb.HardState{Term: 2, Commit: 4}
	snap := raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1}}
	if err := store.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries: []raftpb.Entry{
			{Index: 3, Term: 2, Type: raftpb.EntryNormal},
			{Index: 4, Term: 2, Type: raftpb.EntryNormal},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(context.Background(), 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	service := controllerraft.NewService(controllerraft.Config{
		NodeID: 1,
		LogCompaction: controllerraft.LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 10,
			CheckInterval:  time.Second,
		},
	})
	cluster := &Cluster{
		cfg:    Config{NodeID: 1},
		router: NewRouter(NewHashSlotTable(4, 1), 1, nil),
		controllerResources: controllerResources{
			controllerHost: &controllerHost{raftDB: db, service: service},
		},
	}

	got, err := cluster.ControllerRaftStatusOnNode(context.Background(), 1)
	if err != nil {
		t.Fatalf("ControllerRaftStatusOnNode() error = %v", err)
	}
	if got.NodeID != 1 || got.FirstIndex != 3 || got.LastIndex != 4 || got.CommitIndex != 4 || got.AppliedIndex != 3 || got.SnapshotIndex != 2 || got.SnapshotTerm != 1 {
		t.Fatalf("ControllerRaftStatusOnNode() = %+v", got)
	}
	if !got.Compaction.Enabled || got.Compaction.TriggerEntries != 10 || got.Compaction.CheckInterval != time.Second {
		t.Fatalf("Compaction = %+v", got.Compaction)
	}
}

func TestControllerRaftStatusReadsSnapshotMetadataWithoutPayload(t *testing.T) {
	sentinel := errors.New("snapshot payload unavailable")
	store := &controllerStatusMetadataOnlyStorage{
		state: multiraft.BootstrapState{
			HardState:    raftpb.HardState{Commit: 12},
			AppliedIndex: 11,
		},
		firstIndex:  11,
		lastIndex:   12,
		snapshotErr: sentinel,
		terms:       map[uint64]uint64{10: 7},
	}
	base := ControllerRaftStatus{
		NodeID: 1,
		Peers:  []ControllerRaftPeerProgress{{NodeID: 2, Next: 9}},
	}

	got, err := controllerRaftStatusWithDurableIndexes(context.Background(), base, store)
	if err != nil {
		t.Fatalf("controllerRaftStatusWithDurableIndexes() error = %v", err)
	}
	if got.FirstIndex != 11 || got.LastIndex != 12 || got.CommitIndex != 12 || got.AppliedIndex != 11 || got.SnapshotIndex != 10 || got.SnapshotTerm != 7 {
		t.Fatalf("controllerRaftStatusWithDurableIndexes() = %+v", got)
	}
	if !got.Peers[0].NeedsSnapshot {
		t.Fatalf("peer NeedsSnapshot = false, want true")
	}
	if store.snapshotCalls != 0 {
		t.Fatalf("Snapshot() calls = %d, want 0", store.snapshotCalls)
	}
	if store.firstIndexCalls == 0 || store.termCalls == 0 {
		t.Fatalf("metadata calls = FirstIndex:%d Term:%d, want both used", store.firstIndexCalls, store.termCalls)
	}
}

func TestControllerRaftStatusDerivesPeerSnapshotFlags(t *testing.T) {
	st := ControllerRaftStatus{
		FirstIndex: 10,
		Peers: []ControllerRaftPeerProgress{
			{NodeID: 2, Next: 9},
			{NodeID: 3, Next: 10, PendingSnapshot: 12},
		},
	}

	deriveControllerRaftPeerStatus(&st)

	if !st.Peers[0].NeedsSnapshot {
		t.Fatalf("peer 2 NeedsSnapshot = false, want true")
	}
	if st.Peers[0].SnapshotTransferring {
		t.Fatalf("peer 2 SnapshotTransferring = true, want false")
	}
	if st.Peers[1].NeedsSnapshot {
		t.Fatalf("peer 3 NeedsSnapshot = true, want false")
	}
	if !st.Peers[1].SnapshotTransferring {
		t.Fatalf("peer 3 SnapshotTransferring = false, want true")
	}
}

func TestControllerHandlerServesControllerRaftStatusWithoutLeaderRedirect(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, false)
	handler := &controllerHandler{cluster: cluster}
	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCControllerRaftStatus})
	if err != nil {
		t.Fatalf("encodeControllerRequest() error = %v", err)
	}

	respBody, err := handler.Handle(context.Background(), body)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	resp, err := decodeControllerResponse(controllerRPCControllerRaftStatus, respBody)
	if err != nil {
		t.Fatalf("decodeControllerResponse() error = %v", err)
	}
	if resp.NotLeader {
		t.Fatal("ControllerRaftStatus NotLeader = true, want false")
	}
	if resp.ControllerRaftStatus == nil || resp.ControllerRaftStatus.NodeID != uint64(host.localNode) {
		t.Fatalf("ControllerRaftStatus = %+v", resp.ControllerRaftStatus)
	}
}

func TestControllerRaftStatusOnNodeReturnsRemoteTargetError(t *testing.T) {
	cluster := &Cluster{
		cfg:    Config{NodeID: 1},
		router: NewRouter(NewHashSlotTable(4, 1), 1, nil),
	}
	cluster.stopped.Store(true)

	_, err := cluster.ControllerRaftStatusOnNode(context.Background(), 2)
	if !errors.Is(err, transport.ErrStopped) {
		t.Fatalf("ControllerRaftStatusOnNode() error = %v, want %v", err, transport.ErrStopped)
	}
}

type controllerStatusMetadataOnlyStorage struct {
	state multiraft.BootstrapState

	firstIndex uint64
	lastIndex  uint64
	terms      map[uint64]uint64

	snapshotErr     error
	firstIndexCalls int
	termCalls       int
	snapshotCalls   int
}

func (s *controllerStatusMetadataOnlyStorage) InitialState(context.Context) (multiraft.BootstrapState, error) {
	return s.state, nil
}

func (s *controllerStatusMetadataOnlyStorage) Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (s *controllerStatusMetadataOnlyStorage) Term(_ context.Context, index uint64) (uint64, error) {
	s.termCalls++
	return s.terms[index], nil
}

func (s *controllerStatusMetadataOnlyStorage) FirstIndex(context.Context) (uint64, error) {
	s.firstIndexCalls++
	return s.firstIndex, nil
}

func (s *controllerStatusMetadataOnlyStorage) LastIndex(context.Context) (uint64, error) {
	return s.lastIndex, nil
}

func (s *controllerStatusMetadataOnlyStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	s.snapshotCalls++
	return raftpb.Snapshot{}, s.snapshotErr
}

func (s *controllerStatusMetadataOnlyStorage) Save(context.Context, multiraft.PersistentState) error {
	return nil
}

func (s *controllerStatusMetadataOnlyStorage) MarkApplied(context.Context, uint64) error {
	return nil
}
