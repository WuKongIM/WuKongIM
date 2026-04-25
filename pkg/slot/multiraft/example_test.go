package multiraft_test

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func ExampleRuntime() {
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 100 * time.Millisecond,
		Workers:      1,
		Transport:    noopTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			PreVote:       true,
			CheckQuorum:   true,
		},
	})
	if err != nil {
		panic(err)
	}
	defer rt.Close()

	_ = rt.OpenSlot(context.Background(), multiraft.SlotOptions{
		ID:           1,
		Storage:      noopStorage{},
		StateMachine: noopStateMachine{},
	})

	// Output:
}

type noopTransport struct{}

func (noopTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	return nil
}

type noopStorage struct{}

func (noopStorage) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	return multiraft.BootstrapState{}, nil
}

func (noopStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (noopStorage) Term(ctx context.Context, index uint64) (uint64, error) {
	return 0, nil
}

func (noopStorage) FirstIndex(ctx context.Context) (uint64, error) {
	return 1, nil
}

func (noopStorage) LastIndex(ctx context.Context) (uint64, error) {
	return 0, nil
}

func (noopStorage) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	return raftpb.Snapshot{}, nil
}

func (noopStorage) Save(ctx context.Context, st multiraft.PersistentState) error {
	return nil
}

func (noopStorage) MarkApplied(ctx context.Context, index uint64) error {
	return nil
}

type noopStateMachine struct{}

func (noopStateMachine) Apply(ctx context.Context, cmd multiraft.Command) ([]byte, error) {
	return nil, nil
}

func (noopStateMachine) Restore(ctx context.Context, snap multiraft.Snapshot) error {
	return nil
}

func (noopStateMachine) Snapshot(ctx context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}
