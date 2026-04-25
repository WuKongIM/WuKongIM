package multiraft

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

func TestStepRoutesMessageToCorrectSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(100)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	if err := rt.Step(context.Background(), Envelope{
		SlotID:  100,
		Message: raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1},
	}); err != nil {
		t.Fatalf("Step() error = %v", err)
	}

	waitForCondition(t, func() bool { return slotRequestCount(rt, 100) == 1 })
}

func TestStepUnknownSlotReturnsErrSlotNotFound(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.Step(context.Background(), Envelope{SlotID: 404})
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestRuntimeTickLoopEnqueuesOpenSlots(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(101)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool { return slotTickCount(rt, 101) > 0 })
}

func TestStatusIsRaceFree(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 102)

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_, _ = rt.Status(slotID)
			}
		}
	}()

	for i := 0; i < 5; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString("status"))
		if err != nil {
			t.Fatalf("Propose() error = %v", err)
		}
		if _, err := fut.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}
}

func TestCloseSlotStopsFurtherProcessing(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(103)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), slotID, proposalString("slow"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), slotID)
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("CloseSlot() returned before in-flight apply finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	fsm.unblock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("Wait() error = %v, want %v", err, ErrSlotClosed)
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseSlot() did not return after apply completed")
	}
}

func TestCloseSlotBlocksNewAdmissions(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(104)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	g := slotFor(rt, 104)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	if err := rt.CloseSlot(context.Background(), 104); err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}

	if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat}); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("enqueueRequest() error = %v, want %v", err, ErrSlotClosed)
	}
	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("enqueueControl() error = %v, want %v", err, ErrSlotClosed)
	}
}

func TestRuntimeTickLoopDoesNotHoldLockAcrossEnqueue(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Millisecond)
	blockedSlotID := SlotID(105)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           blockedSlotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(blockedSlotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), blockedSlotID, proposalString("slow"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	for i := 0; i < cap(rt.scheduler.ch)+1; i++ {
		if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(SlotID(2000+i))); err != nil {
			t.Fatalf("OpenSlot(%d) error = %v", 2000+i, err)
		}
	}

	waitForCondition(t, func() bool { return len(rt.scheduler.ch) == cap(rt.scheduler.ch) })
	time.Sleep(20 * time.Millisecond)

	openDone := make(chan error, 1)
	go func() {
		openDone <- rt.OpenSlot(context.Background(), newInternalSlotOptions(5000))
	}()

	select {
	case err := <-openDone:
		if err != nil {
			t.Fatalf("OpenSlot() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OpenSlot() blocked behind scheduler enqueue while ticker was running")
	}

	fsm.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestSchedulerBackpressureDoesNotBlockRuntime(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Millisecond)
	blockedSlotID := SlotID(106)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           blockedSlotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(blockedSlotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), blockedSlotID, proposalString("slow"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	targetSlotID := SlotID(3000)
	for i := 0; i < cap(rt.scheduler.ch)+1; i++ {
		id := SlotID(3000 + i)
		if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(id)); err != nil {
			t.Fatalf("OpenSlot(%d) error = %v", id, err)
		}
	}

	waitForCondition(t, func() bool { return len(rt.scheduler.ch) == cap(rt.scheduler.ch) })
	time.Sleep(20 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), targetSlotID)
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("CloseSlot() blocked behind scheduler enqueue while ticker was running")
	}

	fsm.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestWrapMessagesIntoReusesDestinationSlice(t *testing.T) {
	backing := make([]Envelope, 4)
	dst := backing[:0]
	msgs := []raftpb.Message{{
		Type: raftpb.MsgHeartbeat,
		From: 1,
		To:   2,
	}}

	batch := wrapMessagesInto(dst, 42, msgs)

	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}
	if cap(batch) != cap(dst) {
		t.Fatalf("cap(batch) = %d, want %d", cap(batch), cap(dst))
	}
	if &batch[0] != &backing[0] {
		t.Fatal("expected destination backing slice to be reused")
	}
}

func TestWrapMessagesIntoStillClonesRaftMessagePayloads(t *testing.T) {
	msgs := []raftpb.Message{{
		Type:    raftpb.MsgApp,
		From:    1,
		To:      2,
		Context: []byte("ctx"),
		Entries: []raftpb.Entry{{
			Index: 1,
			Term:  1,
			Data:  []byte("entry"),
		}},
		Snapshot: &raftpb.Snapshot{
			Data: []byte("snap"),
			Metadata: raftpb.SnapshotMetadata{
				Index: 1,
				Term:  1,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2, 3},
				},
			},
		},
	}}

	batch := wrapMessagesInto(nil, 42, msgs)

	msgs[0].Context[0] = 'x'
	msgs[0].Entries[0].Data[0] = 'X'
	msgs[0].Snapshot.Data[0] = 'Y'
	msgs[0].Snapshot.Metadata.ConfState.Voters[0] = 99

	got := batch[0].Message
	if string(got.Context) != "ctx" {
		t.Fatalf("Context = %q", got.Context)
	}
	if string(got.Entries[0].Data) != "entry" {
		t.Fatalf("Entries[0].Data = %q", got.Entries[0].Data)
	}
	if got.Snapshot == nil || string(got.Snapshot.Data) != "snap" {
		t.Fatalf("Snapshot.Data = %v", got.Snapshot)
	}
	if got.Snapshot.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("Snapshot.Metadata.ConfState.Voters[0] = %d", got.Snapshot.Metadata.ConfState.Voters[0])
	}
}

func TestRequestDrainHelperMovesQueuedMessagesIntoReusableWorkSlice(t *testing.T) {
	g := newTestSlotForDrain()
	if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1}); err != nil {
		t.Fatalf("enqueueRequest() error = %v", err)
	}

	batch := g.takeRequestBatch()
	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.requests) != 0 {
		t.Fatalf("len(g.requests) = %d, want 0", len(g.requests))
	}
}

func TestControlDrainHelperMovesQueuedActionsIntoReusableWorkSlice(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); err != nil {
		t.Fatalf("enqueueControl() error = %v", err)
	}

	batch := g.takeControlBatch()
	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.controls) != 0 {
		t.Fatalf("len(g.controls) = %d, want 0", len(g.controls))
	}
}

func TestResolutionBufferHelpersReuseBackingSlice(t *testing.T) {
	g := newTestSlotForDrain()

	buf := g.takeResolutionBuffer()
	buf = append(buf, futureResolution{index: 1})
	g.releaseResolutionBuffer(buf)

	reused := g.takeResolutionBuffer()
	if len(reused) != 0 {
		t.Fatalf("len(reused) = %d, want 0", len(reused))
	}
	if cap(reused) == 0 {
		t.Fatal("expected reused capacity to be retained")
	}
}

func newStartedRuntime(t *testing.T) *Runtime {
	return newStartedRuntimeWithTick(t, 10*time.Millisecond)
}

func newStartedRuntimeWithTick(t *testing.T, tickInterval time.Duration) *Runtime {
	t.Helper()

	rt, err := New(Options{
		NodeID:       1,
		TickInterval: tickInterval,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return rt
}

func newInternalSlotOptions(id SlotID) SlotOptions {
	return SlotOptions{
		ID:           id,
		Storage:      &internalFakeStorage{},
		StateMachine: &internalFakeStateMachine{},
	}
}

func newTestSlotForDrain() *slot {
	g := &slot{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func newLeaderTestSlotForDrain() *slot {
	g := newTestSlotForDrain()
	g.status.Role = RoleLeader
	return g
}

func proposalPayload(hashSlot uint16, data []byte) []byte {
	payload := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(payload[:2], hashSlot)
	copy(payload[2:], data)
	return payload
}

func proposalString(data string) []byte {
	return proposalPayload(0, []byte(data))
}

func waitForCondition(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func slotRequestCount(rt *Runtime, id SlotID) int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	g := rt.slots[id]
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requestCount
}

func slotTickCount(rt *Runtime, id SlotID) int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	g := rt.slots[id]
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tickCount
}

type internalFakeTransport struct{}

func (f *internalFakeTransport) Send(ctx context.Context, batch []Envelope) error {
	return nil
}

type blockingStateMachine struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStateMachine() *blockingStateMachine {
	return &blockingStateMachine{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (f *blockingStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return append([]byte("ok:"), cmd.Data...), nil
}

func (f *blockingStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	return nil
}

func (f *blockingStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}

func (f *blockingStateMachine) unblock() {
	f.once.Do(func() {
		close(f.release)
	})
}

type internalFakeStorage struct {
	mu               sync.Mutex
	state            BootstrapState
	entries          []raftpb.Entry
	snapshot         raftpb.Snapshot
	saveCount        int
	saveErr          error
	lastSavedIndex   uint64
	lastApplied      uint64
	markAppliedCount int
	markAppliedErr   error
}

func (f *internalFakeStorage) InitialState(ctx context.Context) (BootstrapState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *internalFakeStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []raftpb.Entry
	for _, entry := range f.entries {
		if entry.Index >= lo && entry.Index < hi {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *internalFakeStorage) Term(ctx context.Context, index uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, entry := range f.entries {
		if entry.Index == index {
			return entry.Term, nil
		}
	}
	return 0, nil
}

func (f *internalFakeStorage) FirstIndex(ctx context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.entries) == 0 {
		return 1, nil
	}
	return f.entries[0].Index, nil
}

func (f *internalFakeStorage) LastIndex(ctx context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.entries) == 0 {
		return f.snapshot.Metadata.Index, nil
	}
	return f.entries[len(f.entries)-1].Index, nil
}

func (f *internalFakeStorage) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot, nil
}

func (f *internalFakeStorage) Save(ctx context.Context, st PersistentState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}

	f.saveCount++
	if st.HardState != nil {
		f.state.HardState = *st.HardState
		if st.HardState.Commit > f.lastSavedIndex {
			f.lastSavedIndex = st.HardState.Commit
		}
	}
	if len(st.Entries) > 0 {
		f.entries = append([]raftpb.Entry(nil), st.Entries...)
		f.lastSavedIndex = st.Entries[len(st.Entries)-1].Index
	}
	if st.Snapshot != nil {
		f.snapshot = *st.Snapshot
		f.lastSavedIndex = st.Snapshot.Metadata.Index
	}
	return nil
}

func (f *internalFakeStorage) MarkApplied(ctx context.Context, index uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markAppliedErr != nil {
		return f.markAppliedErr
	}

	f.markAppliedCount++
	f.lastApplied = index
	f.state.AppliedIndex = index
	return nil
}

type internalFakeStateMachine struct {
	mu           sync.Mutex
	applied      [][]byte
	commands     []Command
	applyErr     error
	restoreCount int
	lastSnapshot Snapshot
	restoreErr   error
}

func (f *internalFakeStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.applied = append(f.applied, append([]byte(nil), cmd.Data...))
	f.commands = append(f.commands, Command{
		SlotID:   cmd.SlotID,
		HashSlot: cmd.HashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	})
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	return append([]byte("ok:"), cmd.Data...), nil
}

func (f *internalFakeStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCount++
	f.lastSnapshot = snap
	if f.restoreErr != nil {
		return f.restoreErr
	}
	return nil
}

func (f *internalFakeStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}
