package raft

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

type fakeBatchApplier struct {
	mu      sync.Mutex
	batches [][]fsm.AppliedCommand
	reject  map[uint64]string
	results []fsm.ApplyResult
}

func (f *fakeBatchApplier) ApplyBatch(ctx context.Context, cmds []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
	_ = ctx
	f.mu.Lock()
	f.batches = append(f.batches, append([]fsm.AppliedCommand(nil), cmds...))
	f.mu.Unlock()
	if len(f.results) > 0 {
		return fsm.BatchApplyResult{Results: append([]fsm.ApplyResult(nil), f.results...)}, nil
	}
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
	marks  []uint64
	onMark func(uint64)
	err    error
}

func (s *fakeAppliedStore) MarkAppliedBatch(ctx context.Context, index uint64) error {
	_ = ctx
	s.marks = append(s.marks, index)
	if s.onMark != nil {
		s.onMark(index)
	}
	if s.err != nil {
		return s.err
	}
	return nil
}

func TestApplySchedulerBatchesContiguousNormalEntries(t *testing.T) {
	applier := &fakeBatchApplier{}
	store := &fakeAppliedStore{}
	completions := make(map[uint64]proposalResponse)
	sched := newApplyScheduler(applySchedulerConfig{MaxEntries: 4, MaxBytes: 1 << 20, MaxDelay: time.Hour}, applier, store, func(index uint64, result ProposalResult, err error) {
		completions[index] = proposalResponse{result: result, err: err}
	})
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
	}

	require.NoError(t, sched.applyEntries(context.Background(), entries, nil))
	require.Len(t, applier.batches, 1)
	require.Equal(t, uint64(2), store.marks[0])
	require.Contains(t, completions, uint64(1))
	require.Contains(t, completions, uint64(2))
	require.True(t, completions[1].result.Changed)
	require.Equal(t, uint64(1), completions[1].result.AppliedRaftIndex)
}

func TestApplySchedulerCompletesSemanticRejectForMatchingIndex(t *testing.T) {
	applier := &fakeBatchApplier{reject: map[uint64]string{2: "bad"}}
	store := &fakeAppliedStore{}
	completions := make(map[uint64]proposalResponse)
	sched := newApplyScheduler(applySchedulerConfig{MaxEntries: 4, MaxBytes: 1 << 20}, applier, store, func(index uint64, result ProposalResult, err error) {
		completions[index] = proposalResponse{result: result, err: err}
	})
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)},
	}

	require.NoError(t, sched.applyEntries(context.Background(), entries, nil))
	require.NoError(t, completions[1].err)
	require.True(t, completions[1].result.Changed)
	require.ErrorIs(t, completions[2].err, ErrProposalRejected)
	require.True(t, completions[2].result.Rejected)
	require.Equal(t, "bad", completions[2].result.Reason)
}

func TestApplySchedulerDispatchesTaskTransitionsAfterMarkApplied(t *testing.T) {
	applier := &fakeBatchApplier{results: []fsm.ApplyResult{{
		Changed:          true,
		AppliedRaftIndex: 7,
		TaskTransitions:  []fsm.TaskTransition{{AppliedRaftIndex: 7}},
	}}}
	store := &fakeAppliedStore{}
	var observed []string
	store.onMark = func(index uint64) {
		require.Equal(t, uint64(7), index)
		observed = append(observed, "mark")
	}
	sched := newApplyScheduler(applySchedulerConfig{}, applier, store, nil)
	sched.onTaskTransitions = func(items []fsm.TaskTransition) {
		require.Len(t, items, 1)
		require.Equal(t, uint64(7), items[0].AppliedRaftIndex)
		observed = append(observed, "observer")
	}

	err := sched.applyJob(context.Background(), toApply{
		entries: []raftpb.Entry{{Index: 7, Term: 2, Type: raftpb.EntryNormal, Data: mustEncodeSchedulerCommand(t)}},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mark", "observer"}, observed)
}

func TestApplySchedulerCompletesMembershipOnPostApplyFailure(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*applyScheduler, *fakeAppliedStore) error
	}{
		{
			name: "mark applied",
			setup: func(_ *applyScheduler, store *fakeAppliedStore) error {
				err := errors.New("mark failed")
				store.err = err
				return err
			},
		},
		{
			name: "notify applied",
			setup: func(sched *applyScheduler, _ *fakeAppliedStore) error {
				err := errors.New("notify failed")
				sched.onApplied = func(context.Context, uint64) error {
					return err
				}
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeAppliedStore{}
			sched := newApplyScheduler(applySchedulerConfig{}, &fakeBatchApplier{}, store, nil)
			expectedErr := tt.setup(sched, store)
			var completed proposalResponse
			sched.completeMembership = func(_ uint64, result MembershipChangeResult, err error) {
				completed = proposalResponse{membership: result, err: err}
			}
			confChangeC := make(chan confChangeRequest, 1)
			go func() {
				req := <-confChangeC
				req.resp <- confChangeResult{state: raftpb.ConfState{Voters: []uint64{1, 2, 3}, Learners: []uint64{4}}}
			}()

			err := sched.applyEntries(context.Background(), []raftpb.Entry{{Index: 7, Term: 2, Type: raftpb.EntryConfChange}}, confChangeC)

			require.ErrorIs(t, err, expectedErr)
			require.ErrorIs(t, completed.err, expectedErr)
			require.Equal(t, uint64(7), completed.membership.Index)
			require.Equal(t, []uint64{4}, completed.membership.ConfState.Learners)
		})
	}
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
