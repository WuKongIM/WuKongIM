package raft

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

type fakeBatchApplier struct {
	mu      sync.Mutex
	batches [][]fsm.AppliedCommand
	reject  map[uint64]string
}

func (f *fakeBatchApplier) ApplyBatch(ctx context.Context, cmds []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
	_ = ctx
	f.mu.Lock()
	f.batches = append(f.batches, append([]fsm.AppliedCommand(nil), cmds...))
	f.mu.Unlock()
	results := make([]fsm.ApplyResult, len(cmds))
	for i, cmd := range cmds {
		if reason := f.reject[cmd.Index]; reason != "" {
			results[i] = fsm.ApplyResult{Rejected: true, Reason: reason, AppliedRaftIndex: cmd.Index}
			continue
		}
		results[i] = fsm.ApplyResult{Changed: true, Revision: uint64(i + 1), AppliedRaftIndex: cmd.Index}
	}
	return fsm.BatchApplyResult{Results: results}, nil
}

type fakeAppliedStore struct {
	marks []uint64
}

func (s *fakeAppliedStore) MarkAppliedBatch(ctx context.Context, index uint64) error {
	_ = ctx
	s.marks = append(s.marks, index)
	return nil
}

func TestApplySchedulerBatchesContiguousNormalEntries(t *testing.T) {
	applier := &fakeBatchApplier{}
	store := &fakeAppliedStore{}
	completions := make(map[uint64]error)
	sched := newApplyScheduler(applySchedulerConfig{MaxEntries: 4, MaxBytes: 1 << 20, MaxDelay: time.Hour}, applier, store, func(index uint64, err error) { completions[index] = err })
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
	}

	require.NoError(t, sched.applyEntries(context.Background(), entries, nil))
	require.Len(t, applier.batches, 1)
	require.Equal(t, uint64(2), store.marks[0])
	require.Contains(t, completions, uint64(1))
	require.Contains(t, completions, uint64(2))
}

func TestApplySchedulerCompletesSemanticRejectForMatchingIndex(t *testing.T) {
	applier := &fakeBatchApplier{reject: map[uint64]string{2: "bad"}}
	store := &fakeAppliedStore{}
	completions := make(map[uint64]error)
	sched := newApplyScheduler(applySchedulerConfig{MaxEntries: 4, MaxBytes: 1 << 20}, applier, store, func(index uint64, err error) { completions[index] = err })
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
	}

	require.NoError(t, sched.applyEntries(context.Background(), entries, nil))
	require.NoError(t, completions[1])
	require.ErrorIs(t, completions[2], ErrProposalRejected)
}

func TestApplySchedulerRestoresReadySnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sm := newTestStateMachine(t, filepath.Join(dir, "cluster-state.json"))
	store, err := raftstore.Open(ctx, raftstore.Config{Dir: filepath.Join(dir, "controller-raft"), NodeID: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	source := newTestStateMachine(t, filepath.Join(t.TempDir(), "source-state.json"))
	_, err = source.Apply(ctx, 10, testInitCommand("wk-ready-snapshot", []Peer{{NodeID: 2, Addr: "n2"}}))
	require.NoError(t, err)
	restored := source.Snapshot(ctx)
	data, err := state.Encode(restored)
	require.NoError(t, err)

	sched := newApplyScheduler(applySchedulerConfig{}, sm, store, nil)

	err = sched.applyJob(ctx, toApply{
		snapshot: raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: 10, Term: 2}},
	})

	require.NoError(t, err)
	actual := sm.Snapshot(ctx)
	require.Equal(t, uint64(1), actual.Revision)
	require.Equal(t, uint64(10), actual.AppliedRaftIndex)
	require.Equal(t, "wk-ready-snapshot", actual.ClusterID)
	require.Equal(t, uint64(10), store.AppliedIndex())
}

func mustEncodeSchedulerCommand(t *testing.T) []byte {
	t.Helper()
	data, err := command.Encode(command.Command{Kind: command.KindUpdateControllerVoters})
	require.NoError(t, err)
	return data
}
