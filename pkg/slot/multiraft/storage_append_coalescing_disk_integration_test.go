//go:build integration

// Keep external package to avoid a multiraft -> raftlog -> multiraft import cycle.
package multiraft_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// This complements the three-voter public protocol RED with the production
// raftlog.ForSlot adapter. It does not replace quorum/term safety tests. The
// persisted suffix, HardState and durable apply must survive DB/Runtime reopen.
func TestRuntimeStorageCoalescedPrefixSurvivesRaftLogReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	dir := t.TempDir()
	dbPath, snapshots := filepath.Join(dir, "raftlog"), filepath.Join(dir, "snapshots")
	db := openExternalSnapshotDB(t, dbPath, snapshots)
	const id multiraft.SlotID = 604
	gate := &coalescedDiskGate{Storage: db.ForSlot(uint64(id))}
	for i := range gate.entered {
		gate.entered[i] = make(chan struct{})
		gate.release[i] = make(chan struct{})
	}
	rt := newExternalSnapshotRuntime(t)
	t.Cleanup(func() {
		gate.unblock(0)
		gate.unblock(1)
		cancel()
		if rt != nil {
			if err := rt.Close(); err != nil {
				t.Error(err)
			}
		}
		if db != nil {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot:   multiraft.SlotOptions{ID: id, Storage: gate, StateMachine: newFakeStateMachine()},
		Voters: []multiraft.NodeID{1}, Campaign: true,
	}); err != nil {
		t.Fatal(err)
	}
	var baseline multiraft.Status
	waitForExternalSnapshotCondition(t, func() bool {
		current, ready := coalescedDiskCurrentTermDurable(ctx, rt, id, gate, 0)
		if ready {
			baseline = current
		}
		return ready
	})
	for i := 0; i < 2; i++ {
		if _, err := rt.FreshStatus(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	gate.target.Store(baseline.CommitIndex + 1)
	futures := make([]multiraft.Future, 0, 3)
	for i := 1; i <= 3; i++ {
		f, err := rt.Propose(ctx, id, externalProposalPayload(0, []byte(fmt.Sprintf("coalesced-%d", i))))
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
		if i == 1 {
			// Freeze the first call before queueing successors; otherwise a
			// correct coalescer may absorb all three into this initial call.
			select {
			case <-gate.entered[0]:
			case <-ctx.Done():
				t.Fatal("first physical Save did not enter")
			}
			if gate.firstCount != 1 {
				t.Fatalf("first physical Save setup: entries=%d want1", gate.firstCount)
			}
		}
		// A three-owner test stays below the unchanged count cap of four.
		// Two synchronous controls ensure the preceding proposal/Ready turn
		// completed, rather than equating public queue admission with Ready.
		for pass := 0; pass < 2; pass++ {
			if _, err := rt.FreshStatus(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-gate.entered[0]:
	case <-ctx.Done():
		t.Fatal("first physical Save did not enter")
	}
	gate.unblock(0)
	select {
	case <-gate.entered[1]:
	case <-ctx.Done():
		gate.logDiagnostic(t, rt, id)
		t.Fatal("second physical Save did not enter")
	}
	if gate.secondCount != 2 || len(gate.second) != 2 {
		t.Fatalf("queued disk prefix entries=%d want2", gate.secondCount)
	}
	for i, e := range gate.second {
		want := externalProposalPayload(0, []byte(fmt.Sprintf("coalesced-%d", i+2)))
		if e.index != baseline.CommitIndex+uint64(i+2) || e.term != baseline.Term || e.digest != sha256.Sum256(want) {
			gate.logDiagnostic(t, rt, id)
			t.Logf("disk baseline: role=%v term=%d commit=%d applied=%d", baseline.Role, baseline.Term, baseline.CommitIndex, baseline.AppliedIndex)
			t.Fatalf("disk prefix %d index=%d term=%d digest=%x want_index=%d want_term=%d want_digest=%x", i, e.index, e.term, e.digest, baseline.CommitIndex+uint64(i+2), baseline.Term, sha256.Sum256(want))
		}
	}
	before, err := gate.InitialState(ctx)
	if err != nil || before.AppliedIndex > baseline.CommitIndex+1 {
		t.Fatalf("premature applied=%d err=%v", before.AppliedIndex, err)
	}
	gate.unblock(1)
	for i, f := range futures {
		result, err := f.Wait(ctx)
		if err != nil || result.Index != baseline.CommitIndex+uint64(i+1) || result.Term != baseline.Term {
			t.Fatalf("durable Future %d index=%d term=%d err=%v", i, result.Index, result.Term, err)
		}
	}
	if err := rt.ReadBarrier(ctx, id); err != nil {
		t.Fatal(err)
	}
	expected := baseline.CommitIndex + 3
	stable, err := gate.InitialState(ctx)
	if err != nil || stable.AppliedIndex != expected || stable.HardState.Commit != expected {
		t.Fatalf("stable boundary applied=%d commit=%d err=%v", stable.AppliedIndex, stable.HardState.Commit, err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt = nil
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil

	db = openExternalSnapshotDB(t, dbPath, snapshots)
	store := db.ForSlot(uint64(id))
	restored, err := store.InitialState(ctx)
	if err != nil || restored.AppliedIndex != stable.AppliedIndex || restored.HardState != stable.HardState {
		t.Fatalf("reopen boundary applied=%d commit=%d term=%d err=%v", restored.AppliedIndex, restored.HardState.Commit, restored.HardState.Term, err)
	}
	entries, err := store.Entries(ctx, baseline.CommitIndex+1, expected+1, ^uint64(0))
	if err != nil || len(entries) != 3 {
		t.Fatalf("reopen suffix entries=%d err=%v", len(entries), err)
	}
	for i, e := range entries {
		if e.Index != baseline.CommitIndex+uint64(i+1) || e.Term != baseline.Term || sha256.Sum256(e.Data) != sha256.Sum256(externalProposalPayload(0, []byte(fmt.Sprintf("coalesced-%d", i+1)))) {
			t.Fatalf("reopen suffix %d index=%d term=%d digest=%x", i, e.Index, e.Term, sha256.Sum256(e.Data))
		}
	}
	rt = newExternalSnapshotRuntime(t)
	fsm := &coalescedReopenFSM{applied: make(chan [32]byte, 1)}
	if err := rt.OpenSlot(ctx, multiraft.SlotOptions{ID: id, Storage: store, StateMachine: fsm}); err != nil {
		t.Fatal(err)
	}
	// The durable applied boundary is not a certificate to replay into a
	// fresh empty FSM. This test asserts disk continuity, not historical replay.
	waitForExternalSnapshotCondition(t, func() bool {
		_, ready := coalescedDiskCurrentTermDurable(ctx, rt, id, store, expected)
		return ready
	})
	if err := rt.ReadBarrier(ctx, id); err != nil {
		t.Fatal(err)
	}
	nextPayload := []byte("after-reopen")
	next, err := rt.Propose(ctx, id, externalProposalPayload(0, nextPayload))
	if err != nil {
		t.Fatal(err)
	}
	nextResult, err := next.Wait(ctx)
	if err != nil || nextResult.Index <= expected || string(nextResult.Data) != "applied" {
		t.Fatalf("reopened command index=%d err=%v", nextResult.Index, err)
	}
	select {
	case digest := <-fsm.applied:
		if digest != sha256.Sum256(nextPayload) {
			t.Fatalf("reopened FSM digest=%x", digest)
		}
	case <-ctx.Done():
		t.Fatal("reopened FSM did not apply new command")
	}
	nextState, err := store.InitialState(ctx)
	if err != nil || nextState.AppliedIndex < nextResult.Index {
		t.Fatalf("reopened durable applied=%d want>=%d err=%v", nextState.AppliedIndex, nextResult.Index, err)
	}
	if err := rt.ReadBarrier(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Log("production raftlog reopen preserved suffix/whole HardState/durable apply and processed a new command with a fresh read")
}

type coalescedDiskEntry struct {
	index, term uint64
	digest      [32]byte
}

// Decorate the public production Storage port; keep its optional durable
// membership-index port rather than accidentally changing recovery semantics.
type coalescedDiskGate struct {
	multiraft.Storage
	target         atomic.Uint64
	calls          atomic.Int64
	entered        [2]chan struct{}
	release        [2]chan struct{}
	once           [2]sync.Once
	firstCount     int
	secondCount    int
	second         []coalescedDiskEntry
	witnessMu      sync.Mutex
	witnesses      []coalescedDiskSaveWitness
	witnessDropped int
}

func (s *coalescedDiskGate) unblock(i int) { s.once[i].Do(func() { close(s.release[i]) }) }
func (s *coalescedDiskGate) MarkConfigApplied(ctx context.Context, index uint64) error {
	port, ok := s.Storage.(multiraft.ConfigAppliedIndexStorage)
	if !ok {
		return fmt.Errorf("production Storage missing durable config index port")
	}
	return port.MarkConfigApplied(ctx, index)
}
func (s *coalescedDiskGate) Save(ctx context.Context, state multiraft.PersistentState) (saveErr error) {
	relevant := false
	target := s.target.Load()
	witnessIndex := -1
	if target != 0 {
		w := coalescedDiskSaveWitness{Entries: len(state.Entries)}
		if len(state.Entries) > 0 {
			w.First = state.Entries[0].Index
			w.Last = state.Entries[len(state.Entries)-1].Index
			w.EntryTerm = state.Entries[0].Term
		}
		if state.HardState != nil {
			w.HasHS = true
			w.Term = state.HardState.Term
			w.Vote = state.HardState.Vote
			w.Commit = state.HardState.Commit
		}
		s.witnessMu.Lock()
		if len(s.witnesses) < 8 {
			witnessIndex = len(s.witnesses)
			s.witnesses = append(s.witnesses, w)
		} else {
			s.witnessDropped++
		}
		s.witnessMu.Unlock()
	}
	defer func() {
		if witnessIndex < 0 {
			return
		}
		code := "ok"
		if saveErr != nil {
			code = fmt.Sprintf("%T", saveErr)
		}
		s.witnessMu.Lock()
		s.witnesses[witnessIndex].Returned = true
		s.witnesses[witnessIndex].ErrorClass = code
		s.witnessMu.Unlock()
	}()
	for _, e := range state.Entries {
		relevant = relevant || (target != 0 && e.Index >= target)
	}
	if relevant {
		n := s.calls.Add(1)
		if n == 1 {
			s.firstCount = len(state.Entries)
		}
		if n == 2 {
			s.secondCount = len(state.Entries)
			for i, e := range state.Entries {
				if i == 16 {
					break
				}
				s.second = append(s.second, coalescedDiskEntry{e.Index, e.Term, sha256.Sum256(e.Data)})
			}
		}
		if n <= 2 {
			close(s.entered[n-1])
			select {
			case <-s.release[n-1]:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.Storage.Save(ctx, state)
}

// Publish only a bounded digest; never read the test FSM concurrently without
// synchronization or assume durable-applied historical commands replay.
type coalescedReopenFSM struct{ applied chan [32]byte }

func (s *coalescedReopenFSM) Apply(_ context.Context, c multiraft.Command) ([]byte, error) {
	select {
	case s.applied <- sha256.Sum256(c.Data):
		return []byte("applied"), nil
	default:
		return nil, fmt.Errorf("unexpected extra command during reopen witness")
	}
}
func (s *coalescedReopenFSM) Restore(context.Context, multiraft.Snapshot) error { return nil }
func (s *coalescedReopenFSM) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

// Diagnostics retain only eight metadata records, never command payloads or
// storage paths. They do not alter gates, deadlines, or the failing assertion.
type coalescedDiskSaveWitness struct {
	Entries                int
	First, Last, EntryTerm uint64
	HasHS                  bool
	Term, Vote, Commit     uint64
	Returned               bool
	ErrorClass             string
}

func (s *coalescedDiskGate) logDiagnostic(t *testing.T, rt *multiraft.Runtime, id multiraft.SlotID) {
	t.Helper()
	s.witnessMu.Lock()
	for i, w := range s.witnesses {
		t.Logf("bounded disk Save[%d]: %+v", i, w)
	}
	t.Logf("bounded disk entry Save calls=%d dropped=%d", s.calls.Load(), s.witnessDropped)
	s.witnessMu.Unlock()
	st, err := rt.Status(id)
	t.Logf("bounded disk Runtime status: role=%v term=%d commit=%d applied=%d error_class=%T", st.Role, st.Term, st.CommitIndex, st.AppliedIndex, err)
}

// A new leader may still expose the previous term's applied commit. This public
// predicate establishes one current-term committed and durable-applied boundary
// before arming a Storage gate or issuing the single fresh ReadBarrier.
func coalescedDiskCurrentTermDurable(ctx context.Context, rt *multiraft.Runtime, id multiraft.SlotID, store multiraft.Storage, after uint64) (multiraft.Status, bool) {
	current, err := rt.Status(id)
	if err != nil || current.Role != multiraft.RoleLeader || current.CommitIndex <= after || current.AppliedIndex < current.CommitIndex {
		return current, false
	}
	term, termErr := store.Term(ctx, current.CommitIndex)
	state, stateErr := store.InitialState(ctx)
	ready := termErr == nil && stateErr == nil && term == current.Term && state.HardState.Commit >= current.CommitIndex && state.AppliedIndex >= current.CommitIndex
	return current, ready
}
