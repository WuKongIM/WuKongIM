//go:build integration

package multiraft

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"go.etcd.io/raft/v3/raftpb"
)

// Failure matrix, written before observation hooks:
// fresh quorum phases disappear or invent a Save; a canceled queued read loses
// its unfinished control/Save interval; a late Ready mutates a published sample;
// confirmed proof becomes success before durable apply; parallel groups replace
// the longest group with their sum; phase capacity publishes a partial group.
// Existing fresh-quorum, current-term, batch cancellation and leadership tests
// remain the behavioral contracts. Safety deadlines never establish latency.
func TestReadBarrierPhaseTraceDisabledRuntimeLeavesObservationEmpty(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	const id SlotID = 302
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: &internalFakeStorage{}, StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1}}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		s, e := rt.Status(id)
		return e == nil && s.Role == RoleLeader && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
	})
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 2*time.Second)
	defer cancel()
	if err := rt.ReadBarrier(ctx, id); err != nil {
		t.Fatal(err)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	if len(sample.Spans) != 0 || sample.BarrierGroups != 0 || sample.DroppedSpans != 0 || sample.DroppedBarrierGroups != 0 {
		t.Fatalf("disabled runtime performed optional timing work: %+v", sample)
	}
}

func TestReadBarrierPhaseTraceFreshPublicRuntime(t *testing.T) {
	c := newAsyncTestClusterWithObserver(t, []NodeID{1, 2, 3}, asyncNetworkConfig{Seed: 951}, nil, true)
	const id SlotID = 299
	c.bootstrapSlot(t, id, []NodeID{1, 2, 3})
	rt := c.runtime(c.waitForLeader(t, id))
	f, err := rt.Propose(context.Background(), id, proposalString("trace-visible"))
	if err != nil {
		t.Fatal(err)
	}
	written := waitForFutureResult(t, f)
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 2*time.Second)
	defer cancel()
	if err := rt.ReadBarrier(ctx, id); err != nil {
		t.Fatal(err)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	coverage := requireCoverageWire(t, sample)
	for _, phase := range coverage.Phases {
		if !phase.Observed || phase.Save != (coverageWireOverlay{}) {
			t.Fatalf("fresh public read coverage invalid: %+v", phase)
		}
	}
	for _, stage := range []string{"slot_control_wait", "slot_read_eligibility", "slot_raw_quorum", "slot_ready_to_accept", "slot_apply_wait"} {
		span := requireBarrierPhase(t, sample, stage)
		if span.Incomplete || span.Result != rpctrace.ResultOK {
			t.Fatalf("unfinished successful %s: %+v", stage, span)
		}
	}
	for _, s := range sample.Spans {
		if strings.HasPrefix(string(s.Stage), "slot_ready_save_") {
			t.Fatalf("message-only read invented Save: %+v", s)
		}
	}
	status, err := rt.Status(id)
	if err != nil || status.CommitIndex != written.Index || status.AppliedIndex < written.Index {
		t.Fatalf("observation changed read semantics: %+v %v", status, err)
	}
}

func TestReadBarrierPhaseTraceCanceledDuringSynchronousSaveFallbackIsImmutable(t *testing.T) {
	rt := newStartedRuntimeWithOptions(t, time.Hour, nil, true)
	store := newBlockingSaveStorage()
	t.Cleanup(store.release)
	const id SlotID = 300
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: store, StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1}}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		s, e := rt.Status(id)
		return e == nil && s.Role == RoleLeader && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
	})
	store.armEntrySave()
	// A legal payload above the new 8 MiB per-Slot async ownership budget
	// deliberately exercises the single-Ready synchronous fallback. Ordinary
	// append Save no longer blocks unrelated fresh reads or queued controls.
	f, err := rt.Propose(context.Background(), id, proposalString(strings.Repeat("f", 9<<20)))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.started:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled Save not reached")
	}
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithCancel(rpctrace.With(context.Background(), trace))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- rt.ReadBarrier(ctx, id) }()
	g := rt.slots[id]
	waitForCondition(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return len(g.controls) > 0 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled caller did not return")
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	coverage := requireCoverageWire(t, sample)
	if !coverage.Phases[0].Observed || !coverage.Phases[0].Save.Unfinished || coverage.Phases[0].Save.Count != 1 {
		t.Fatalf("public canceled Save coverage missing: %+v", coverage)
	}
	before := sample.Clone()
	control := requireBarrierPhase(t, sample, "slot_control_wait")
	if !control.Incomplete {
		t.Fatalf("blocked control shown complete: %+v", control)
	}
	foundSave := false
	for _, s := range sample.Spans {
		if strings.HasPrefix(string(s.Stage), "slot_ready_save_") {
			foundSave = true
			if !s.Incomplete {
				t.Fatal("blocked Save shown complete")
			}
		}
	}
	if !foundSave {
		t.Fatal("in-progress prior Save overlay lost")
	}
	store.release()
	ctxJoin, cancelJoin := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelJoin()
	if _, err := f.Wait(ctxJoin); err != nil {
		t.Fatal(err)
	}
	if err := rt.ReadBarrier(ctxJoin, id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sample, &before) || r.Active() != 0 {
		t.Fatal("late completion changed published trace or retained observation credit")
	}
}

// A quorum proof for an index beyond the durable FSM watermark must still wait,
// independently of whether other Ready writes use asynchronous storage.
func TestReadBarrierPhaseTraceWaitsForDurableApply(t *testing.T) {
	rt := newStartedRuntimeWithOptions(t, time.Hour, nil, true)
	fsm := newBlockingStateMachine()
	t.Cleanup(fsm.unblock)
	const id SlotID = 301
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: &internalFakeStorage{}, StateMachine: fsm}, Voters: []NodeID{1}}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		s, e := rt.Status(id)
		return e == nil && s.Role == RoleLeader && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
	})
	f, err := rt.Propose(context.Background(), id, proposalString("hold-apply"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fsm.started:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled apply not reached")
	}
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- rt.ReadBarrier(ctx, id) }()
	g := rt.slots[id]
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, p := range g.pendingReads {
			if p.index > g.durableAppliedIndex {
				return true
			}
		}
		return false
	})
	select {
	case err := <-done:
		t.Fatalf("read passed blocked durable apply: %v", err)
	default:
	}
	released := time.Now()
	fsm.unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	span := requireBarrierPhase(t, sample, "slot_apply_wait")
	if span.Incomplete || sample.At.Add(span.Offset).After(released) || sample.At.Add(span.Offset+span.Duration).Before(released) {
		t.Fatalf("apply interval missed controlled fence: %+v", span)
	}
}

func requireBarrierPhase(t *testing.T, s *rpctrace.Sample, stage string) rpctrace.SpanSample {
	t.Helper()
	var found []rpctrace.SpanSample
	for _, p := range s.Spans {
		if string(p.Stage) == stage {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("phase %s count=%d, sample=%+v", stage, len(found), s)
	}
	return found[0]
}

// The read is a new quorum proof for an already durable current-term index.
// An unrelated, newly proposed uncommitted entry in the same Ready must not
// delay that proof; late success or failure of its Save cannot mutate the read.
// TestReadBarrierPhaseTraceWaitsForDurableApply separately retains the opposite
// case: the requested read index itself has not reached durable application.
func TestReadBarrierPhaseTraceFreshDurableReadDoesNotWaitForUnrelatedSave(t *testing.T) {
	for _, outcome := range []string{"success", "save_error"} {
		t.Run(outcome, func(t *testing.T) {
			g := newReadBarrierBatchLeader(t)
			g.readBarrierTracing = true
			store := &traceResultStorage{blockingSaveStorage: newBlockingSaveStorage()}
			t.Cleanup(store.release)
			g.storage = store
			g.storageView.storage = store
			g.stateMachine = &internalFakeStateMachine{}
			rt := &Runtime{slots: map[SlotID]*slot{1: g}, scheduler: newScheduler(nil)}
			r, _ := rpctrace.NewRecorder(1)
			trace := r.Start(context.Background(), rpctrace.SideServer, 13)
			ctx, cancel := context.WithTimeout(rpctrace.With(context.Background(), trace), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- rt.ReadBarrier(ctx, 1) }()
			select {
			case <-rt.scheduler.ch:
			case <-ctx.Done():
				t.Fatal("public read did not enqueue")
			}
			g.processControls(context.Background())
			transport := &traceReadTransport{}
			g.processReady(context.Background(), transport)
			if len(transport.key) == 0 {
				t.Fatal("no fresh heartbeat context")
			}
			readIndex := g.rawNode.BasicStatus().Commit
			if g.durableAppliedIndex < readIndex {
				t.Fatal("fixture read index is not durably applied")
			}
			if err := g.rawNode.Step(raftpb.Message{Type: raftpb.MsgHeartbeatResp, From: 2, To: 1, Term: g.status.Term, Context: transport.key}); err != nil {
				t.Fatal(err)
			}
			if err := g.rawNode.Propose(proposalString("unrelated-uncommitted-entry")); err != nil {
				t.Fatal(err)
			}
			if g.rawNode.BasicStatus().Commit != readIndex {
				t.Fatal("new proposal unexpectedly changed the read's committed boundary")
			}
			store.armEntrySave()
			if outcome == "save_error" {
				store.result = errors.New("unrelated controlled Save failure")
			}
			workerDone := make(chan struct{})
			go func() { g.processReady(context.Background(), transport); close(workerDone) }()
			t.Cleanup(func() {
				store.release()
				select {
				case <-workerDone:
				case <-time.After(2 * time.Second):
					t.Error("controlled worker failed to join during cleanup")
				}
			})
			select {
			case <-store.started:
			case <-ctx.Done():
				t.Fatal("unrelated append did not reach Save")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("fresh durable read failed behind unrelated Save: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("fresh durable read waited for unrelated uncommitted Save")
			}
			sample := trace.Finish(rpctrace.ResultOK)
			before := sample.Clone()
			for _, stage := range []string{"slot_raw_quorum", "slot_ready_to_accept", "slot_apply_wait"} {
				span := requireBarrierPhase(t, sample, stage)
				if span.Incomplete || span.Result != rpctrace.ResultOK {
					t.Fatalf("finished durable proof retained an unfinished phase: %+v", span)
				}
			}
			coverage := requireCoverageWire(t, sample)
			for _, phase := range coverage.Phases {
				if !phase.Observed {
					t.Fatalf("finished durable proof omitted phase coverage: %+v", phase)
				}
			}
			store.release()
			select {
			case <-workerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("worker failed to join")
			}
			if !reflect.DeepEqual(sample, &before) || r.Active() != 0 {
				t.Fatal("late unrelated Save changed the successful read or retained its observation")
			}
		})
	}
}

type traceResultStorage struct {
	*blockingSaveStorage
	result error
}

func (s *traceResultStorage) Save(ctx context.Context, state PersistentState) error {
	if err := s.blockingSaveStorage.Save(ctx, state); err != nil {
		return err
	}
	return s.result
}

type traceReadTransport struct {
	key    []byte
	result error
}

func (t *traceReadTransport) Send(_ context.Context, batch []Envelope) error {
	for _, e := range batch {
		if e.Message.Type == raftpb.MsgHeartbeat && len(e.Message.Context) > 0 {
			t.key = append([]byte(nil), e.Message.Context...)
		}
	}
	return t.result
}

func TestReadBarrierPhaseTraceUnconfirmedCancelKeepsRaftBudget(t *testing.T) {
	g := newReadBarrierBatchLeader(t)
	g.readBarrierTracing = true
	g.storage = &internalFakeStorage{}
	g.storageView.storage = g.storage
	rt := &Runtime{slots: map[SlotID]*slot{1: g}, scheduler: newScheduler(nil)}
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 13)
	ctx, cancel := context.WithCancel(rpctrace.With(context.Background(), trace))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- rt.ReadBarrier(ctx, 1) }()
	select {
	case <-rt.scheduler.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("read not enqueued")
	}
	g.processControls(context.Background())
	transport := &traceReadTransport{result: errors.New("controlled send busy")}
	g.processReady(context.Background(), transport)
	if len(transport.key) == 0 {
		t.Fatal("missing fresh read heartbeat")
	}
	// No follower acknowledged. Failed Send is a separate observation, never a
	// fabricated quorum response or a reason to release RawNode's pending read.
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(g.pendingReads) != 1 {
		t.Fatal("unconfirmed cancellation released Raft budget")
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	before := sample.Clone()
	raw := requireBarrierPhase(t, sample, "slot_raw_quorum")
	send := requireBarrierPhase(t, sample, "slot_raft_send")
	if !raw.Incomplete || send.Incomplete || send.Result != rpctrace.ResultError {
		t.Fatalf("quorum/send conflated: raw=%+v send=%+v", raw, send)
	}
	if err := g.rawNode.Step(raftpb.Message{Type: raftpb.MsgHeartbeatResp, From: 2, To: 1, Term: g.status.Term, Context: transport.key}); err != nil {
		t.Fatal(err)
	}
	g.processReady(context.Background(), transport)
	if len(g.pendingReads) != 0 || r.Active() != 0 || !reflect.DeepEqual(sample, &before) {
		t.Fatal("late quorum leaked budget or mutated sample")
	}
}

func TestReadBarrierPhaseTraceRegistryOverflowDoesNotRejectRead(t *testing.T) {
	g := newReadBarrierBatchLeader(t)
	g.readBarrierTracing = true
	rt := &Runtime{slots: map[SlotID]*slot{1: g}, scheduler: newScheduler(nil)}
	const readers = 33
	r, _ := rpctrace.NewRecorder(readers)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var traces [readers]*rpctrace.Trace
	done := make(chan error, readers)
	for i := range traces {
		traces[i] = r.Start(ctx, rpctrace.SideServer, 13)
		callCtx := rpctrace.With(ctx, traces[i])
		go func() { done <- rt.ReadBarrier(callCtx, 1) }()
	}
	waitForCondition(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return len(g.controls) == readers })
	cancel()
	for range readers {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("observer overflow rejected read: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("caller did not return")
		}
	}
	var groups, drops uint64
	for _, trace := range traces {
		sample := trace.Finish(rpctrace.ResultCanceled)
		groups += sample.BarrierGroups
		drops += sample.DroppedBarrierGroups
		if len(sample.Spans) > rpctrace.MaxSpans {
			t.Fatal("span cap exceeded")
		}
	}
	if groups != 32 || drops != 1 || r.Active() != 0 {
		t.Fatalf("fixed observer bound coverage: groups=%d drops=%d active=%d", groups, drops, r.Active())
	}
	g.processControls(context.Background())
	if len(g.pendingReads) != 0 {
		t.Fatal("already canceled control issued ReadIndex")
	}
}
