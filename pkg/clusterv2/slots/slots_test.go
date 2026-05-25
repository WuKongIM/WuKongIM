package slots

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestBootstrapOwnerUsesPreferredLeader(t *testing.T) {
	assignment := Assignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2}
	if got := BootstrapOwner(assignment); got != 2 {
		t.Fatalf("BootstrapOwner() = %d, want 2", got)
	}
}

func TestBootstrapOwnerFallsBackToLowestPeer(t *testing.T) {
	assignment := Assignment{SlotID: 1, DesiredPeers: []uint64{3, 1, 2}}
	if got := BootstrapOwner(assignment); got != 1 {
		t.Fatalf("BootstrapOwner() = %d, want 1", got)
	}
}

func TestManagerBootstrapsOwnerWithEmptyState(t *testing.T) {
	runtime := newFakeRuntime()
	manager := newTestManager(1, runtime, fakeStorageFactory(fakeStorage{}))
	if err := manager.Ensure(context.Background(), Assignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if runtime.bootstrapCalls != 1 || runtime.openCalls != 0 {
		t.Fatalf("bootstrap=%d open=%d, want bootstrap=1 open=0", runtime.bootstrapCalls, runtime.openCalls)
	}
}

func TestManagerNonOwnerWaitsWithoutBootstrapEvidence(t *testing.T) {
	runtime := newFakeRuntime()
	manager := newTestManager(2, runtime, fakeStorageFactory(fakeStorage{}))
	if err := manager.Ensure(context.Background(), Assignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if runtime.bootstrapCalls != 0 || runtime.openCalls != 0 {
		t.Fatalf("bootstrap=%d open=%d, want no action", runtime.bootstrapCalls, runtime.openCalls)
	}
}

func TestManagerNonOwnerOpensAfterBootstrapEvidence(t *testing.T) {
	runtime := newFakeRuntime()
	manager := newTestManager(2, runtime, fakeStorageFactory(fakeStorage{}))
	if err := manager.Ensure(context.Background(), Assignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1, Bootstrapped: true}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if runtime.openCalls != 1 || runtime.bootstrapCalls != 0 {
		t.Fatalf("open=%d bootstrap=%d, want open=1 bootstrap=0", runtime.openCalls, runtime.bootstrapCalls)
	}
}

func TestManagerExistingHardStateAlwaysOpens(t *testing.T) {
	runtime := newFakeRuntime()
	store := fakeStorage{state: multiraft.BootstrapState{HardState: raftpb.HardState{Term: 1, Vote: 1, Commit: 1}}}
	manager := newTestManager(2, runtime, fakeStorageFactory(store))
	if err := manager.Ensure(context.Background(), Assignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if runtime.openCalls != 1 || runtime.bootstrapCalls != 0 {
		t.Fatalf("open=%d bootstrap=%d, want open=1 bootstrap=0", runtime.openCalls, runtime.bootstrapCalls)
	}
}

func TestReconcilerSkipsUnassignedLocalNode(t *testing.T) {
	runtime := newFakeRuntime()
	manager := newTestManager(4, runtime, fakeStorageFactory(fakeStorage{}))
	reconciler := NewReconciler(4, manager)
	snap := control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}}}}
	if err := reconciler.Reconcile(context.Background(), snap); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if runtime.openCalls != 0 || runtime.bootstrapCalls != 0 {
		t.Fatalf("open=%d bootstrap=%d, want no action", runtime.openCalls, runtime.bootstrapCalls)
	}
}

func TestStatusSnapshotMapsRuntimeStatus(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.status[1] = multiraft.Status{SlotID: 1, LeaderID: 2, CurrentVoters: []multiraft.NodeID{1, 2, 3}}
	got := StatusSnapshot(runtime, []uint32{1})
	if len(got) != 1 || got[0].SlotID != 1 || got[0].Leader != 2 || len(got[0].Peers) != 3 {
		t.Fatalf("StatusSnapshot() = %#v, want one mapped status", got)
	}
}

func newTestManager(local uint64, runtime *fakeRuntime, factory StorageFactory) *Manager {
	return NewManager(Config{LocalNode: local, Runtime: runtime, Storage: factory, StateMachine: func(uint32, []uint16) (multiraft.StateMachine, error) { return fakeStateMachine{}, nil }})
}

type fakeRuntime struct {
	status         map[uint32]multiraft.Status
	bootstrapCalls int
	openCalls      int
	lastVoters     []multiraft.NodeID
}

func newFakeRuntime() *fakeRuntime { return &fakeRuntime{status: make(map[uint32]multiraft.Status)} }
func (r *fakeRuntime) OpenSlot(context.Context, multiraft.SlotOptions) error {
	r.openCalls++
	return nil
}
func (r *fakeRuntime) BootstrapSlot(_ context.Context, req multiraft.BootstrapSlotRequest) error {
	r.bootstrapCalls++
	r.lastVoters = append([]multiraft.NodeID(nil), req.Voters...)
	return nil
}
func (r *fakeRuntime) Propose(context.Context, multiraft.SlotID, []byte) (multiraft.Future, error) {
	return nil, nil
}
func (r *fakeRuntime) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	st, ok := r.status[uint32(slotID)]
	if !ok {
		return multiraft.Status{}, multiraft.ErrSlotNotFound
	}
	return st, nil
}
func (r *fakeRuntime) Step(context.Context, multiraft.Envelope) error    { return nil }
func (r *fakeRuntime) CloseSlot(context.Context, multiraft.SlotID) error { return nil }

type fakeStorage struct{ state multiraft.BootstrapState }

func fakeStorageFactory(store fakeStorage) StorageFactory {
	return func(uint32) (multiraft.Storage, error) { return store, nil }
}
func (s fakeStorage) InitialState(context.Context) (multiraft.BootstrapState, error) {
	return s.state, nil
}
func (s fakeStorage) Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error) {
	return nil, nil
}
func (s fakeStorage) Term(context.Context, uint64) (uint64, error) { return 0, nil }
func (s fakeStorage) FirstIndex(context.Context) (uint64, error)   { return 1, nil }
func (s fakeStorage) LastIndex(context.Context) (uint64, error)    { return 0, nil }
func (s fakeStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	return raftpb.Snapshot{}, nil
}
func (s fakeStorage) Save(context.Context, multiraft.PersistentState) error { return nil }
func (s fakeStorage) MarkApplied(context.Context, uint64) error             { return nil }

type fakeStateMachine struct{}

func (fakeStateMachine) Apply(context.Context, multiraft.Command) ([]byte, error) { return nil, nil }
func (fakeStateMachine) Restore(context.Context, multiraft.Snapshot) error        { return nil }
func (fakeStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

var _ = errors.Is
