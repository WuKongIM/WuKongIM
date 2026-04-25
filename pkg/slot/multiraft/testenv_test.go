package multiraft_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

type fakeTransport struct {
	sent []multiraft.Envelope
}

func (f *fakeTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	f.sent = append(f.sent, batch...)
	return nil
}

type fakeStorage struct {
	state   multiraft.BootstrapState
	applied uint64
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{}
}

func (f *fakeStorage) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	return f.state, nil
}

func (f *fakeStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (f *fakeStorage) Term(ctx context.Context, index uint64) (uint64, error) {
	return 0, nil
}

func (f *fakeStorage) FirstIndex(ctx context.Context) (uint64, error) {
	return 0, nil
}

func (f *fakeStorage) LastIndex(ctx context.Context) (uint64, error) {
	return 0, nil
}

func (f *fakeStorage) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	return raftpb.Snapshot{}, nil
}

func (f *fakeStorage) Save(ctx context.Context, st multiraft.PersistentState) error {
	if st.HardState != nil {
		f.state.HardState = *st.HardState
	}
	if st.Snapshot != nil {
		f.state.HardState.Commit = st.Snapshot.Metadata.Index
	}
	return nil
}

func (f *fakeStorage) MarkApplied(ctx context.Context, index uint64) error {
	f.applied = index
	f.state.AppliedIndex = index
	return nil
}

type fakeStateMachine struct {
	applied [][]byte
}

func newFakeStateMachine() *fakeStateMachine {
	return &fakeStateMachine{}
}

func (f *fakeStateMachine) Apply(ctx context.Context, cmd multiraft.Command) ([]byte, error) {
	f.applied = append(f.applied, append([]byte(nil), cmd.Data...))
	return nil, nil
}

func (f *fakeStateMachine) Restore(ctx context.Context, snap multiraft.Snapshot) error {
	return nil
}

func (f *fakeStateMachine) Snapshot(ctx context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

func newTestRuntime(t *testing.T) *multiraft.Runtime {
	t.Helper()

	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: time.Second,
		Workers:      1,
		Transport:    &fakeTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return rt
}

func newSlotOptions(id multiraft.SlotID) multiraft.SlotOptions {
	return multiraft.SlotOptions{
		ID:           id,
		Storage:      newFakeStorage(),
		StateMachine: newFakeStateMachine(),
	}
}
