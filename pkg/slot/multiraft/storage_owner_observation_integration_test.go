//go:build integration

package multiraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix precedes instrumentation: asynchronous physical Save must not
// become owner waiting; byte saturation must expose the actual join; an owner
// waiting for the shared Save permit must be distinguishable from that join;
// legal oversized Ready is a non-budget fence. Cancellation clips all owner
// subsets once, and releasing physical gates cannot mutate a published sample.
func TestRuntimeOwnerStorageOrdinaryAsyncSaveExcluded(t *testing.T) {
	gate := newStorageFenceGate()
	rt, ctx := ownerObservationRuntime(t, 1, 4, map[SlotID]Storage{501: gate}, gate.release)
	ownerObservationArm(t, rt, 501, gate)
	f, err := rt.Propose(ctx, 501, proposalString("held-ordinary"))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, gate.entered)
	sample, err := ownerObservationRead(rt, ctx, 501, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("fresh durable read blocked by unrelated Save: %v", err)
	}
	save := requireCoverageWire(t, sample)
	var sum time.Duration
	for _, p := range save.Phases {
		sum += p.Save.Duration
	}
	if sum <= 0 {
		t.Fatal("held physical Save not witnessed")
	}
	owner := ownerObservationWire(t, sample)
	for _, p := range owner.Phases {
		if p.Budget.Duration != 0 || p.Other.Duration != 0 {
			t.Fatalf("async Save attributed to owner: %+v", p)
		}
	}
	gate.release()
	if _, err = f.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeOwnerStorageBudgetDrainClipsAndIsImmutable(t *testing.T) {
	gate := newStorageFenceGate()
	rt, ctx := ownerObservationRuntime(t, 1, 4, map[SlotID]Storage{502: gate}, gate.release)
	ownerObservationArm(t, rt, 502, gate)
	// Both entries fit individually, but their retained ownership exceeds 8 MiB.
	// This remains a real byte fallback when count admission pauses before Ready.
	first, err := rt.Propose(ctx, 502, proposalPayload(0, bytes.Repeat([]byte{'a'}, 5<<20)))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, gate.entered)
	second := ownerObservationPropose(t, rt, ctx, 502, string(bytes.Repeat([]byte{'b'}, 4<<20)))
	sample, err := ownerObservationRead(rt, ctx, 502, 40*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read escaped held owner join: %v", err)
	}
	owner := ownerObservationWire(t, sample)
	p := owner.Phases[0]
	if p.Budget.Duration <= 0 || p.Budget.DrainWait <= 0 || p.Budget.RejectMask&4 == 0 || !p.Budget.Unfinished || p.Other.Duration != 0 {
		t.Fatalf("missing budget join witness: %+v", p)
	}
	before, _ := json.Marshal(sample)
	gate.release()
	for _, f := range []Future{first, second} {
		if _, err = f.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err = rt.FreshStatus(ctx, 502); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := json.Marshal(sample)
	if !bytes.Equal(before, after) {
		t.Fatal("late owner completion mutated published sample")
	}
}

func TestRuntimeOwnerStorageBudgetSavePermit(t *testing.T) {
	target := newBlockingMarkAppliedStorage()
	a, b := newStorageFenceGate(), newStorageFenceGate()
	rt, ctx := ownerObservationRuntime(t, 2, 4, map[SlotID]Storage{503: target, 504: a, 505: b}, func() { target.unblock(); a.release(); b.release() })
	baseline, _ := rt.Status(503)
	target.armAfter(baseline.CommitIndex + 1)
	first, err := rt.Propose(ctx, 503, proposalPayload(0, bytes.Repeat([]byte{'a'}, 3<<20)))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, target.started)
	var peers []Future
	for id, gate := range map[SlotID]*storageAsyncGate{504: a, 505: b} {
		ownerObservationArm(t, rt, id, gate)
		f, e := rt.Propose(ctx, id, proposalString("occupy-save-permit"))
		if e != nil {
			t.Fatal(e)
		}
		peers = append(peers, f)
		storageFenceEntered(t, ctx, gate.entered)
	}
	second := ownerObservationPropose(t, rt, ctx, 503, string(bytes.Repeat([]byte{'b'}, 6<<20)))
	blocked, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = rt.FreshStatus(blocked, 503)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("target owner did not join held apply: %v", err)
	}
	target.unblock()
	if _, err = first.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	sample, err := ownerObservationRead(rt, ctx, 503, 60*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read escaped occupied Save permits: %v", err)
	}
	p := ownerObservationWire(t, sample).Phases[0]
	if p.Budget.SavePermit <= 0 || p.Budget.RejectMask&4 == 0 || p.Other.Duration != 0 {
		t.Fatalf("missing owner Save permit witness: %+v", p)
	}
	a.release()
	b.release()
	peers = append(peers, second)
	for _, f := range peers {
		if _, err = f.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeOwnerStorageOversizeIsOther(t *testing.T) {
	gate := newStorageFenceGate()
	rt, ctx := ownerObservationRuntime(t, 1, 4, map[SlotID]Storage{506: gate}, gate.release)
	ownerObservationArm(t, rt, 506, gate)
	payload := proposalPayload(0, bytes.Repeat([]byte{'o'}, 9<<20))
	f, err := rt.Propose(ctx, 506, payload)
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, gate.entered)
	sample, err := ownerObservationRead(rt, ctx, 506, 40*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read escaped synchronous Save: %v", err)
	}
	p := ownerObservationWire(t, sample).Phases[0]
	if p.Other.Duration <= 0 || p.Other.CauseMask&1 == 0 || !p.Other.Unfinished || p.Budget.Duration != 0 {
		t.Fatalf("oversized legal Ready mislabeled: %+v", p)
	}
	gate.release()
	if _, err = f.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func ownerObservationRuntime(t *testing.T, workers, maxApplying int, stores map[SlotID]Storage, release func()) (*Runtime, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	rt, err := New(Options{NodeID: 1, Workers: workers, TickInterval: time.Hour, ReadBarrierTracing: true, Transport: &internalFakeTransport{}, Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, MaxApplyingTasks: maxApplying}})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release()
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
		cancel()
	})
	for id, store := range stores {
		if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: store, StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1}}); err != nil {
			t.Fatal(err)
		}
		storageAsyncWait(t, ctx, func() bool {
			s, e := rt.Status(id)
			return e == nil && s.Role == RoleLeader && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
		})
		if err := rt.ReadBarrier(ctx, id); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := rt.FreshStatus(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	return rt, ctx
}
func ownerObservationArm(t *testing.T, rt *Runtime, id SlotID, gate *storageAsyncGate) {
	t.Helper()
	s, e := rt.Status(id)
	if e != nil {
		t.Fatal(e)
	}
	gate.target.Store(s.CommitIndex + 1)
	gate.armed.Store(true)
}
func ownerObservationPropose(t *testing.T, rt *Runtime, ctx context.Context, id SlotID, body string) Future {
	t.Helper()
	o := &storageCapacityObserver{reached: make(chan struct{})}
	f, e := rt.Propose(WithProposalStageObserver(ctx, o), id, proposalString(body))
	if e != nil {
		t.Fatal(e)
	}
	storageFenceEntered(t, ctx, o.reached)
	return f
}
func ownerObservationRead(rt *Runtime, ctx context.Context, id SlotID, bound time.Duration) (*rpctrace.Sample, error) {
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(ctx, rpctrace.SideServer, 13)
	c, cancel := context.WithTimeout(rpctrace.With(ctx, trace), bound)
	defer cancel()
	err := rt.ReadBarrier(c, id)
	return trace.Finish(rpctrace.ResultFor(err)), err
}

// Decode through the existing JSON boundary so the baseline fails on missing
// observations, not references to product types that do not yet exist.
type ownerObservationPhase struct {
	Budget struct {
		Duration   time.Duration
		Count      uint64
		Unfinished bool
		DrainWait  time.Duration `json:"drain_wait"`
		SavePermit time.Duration `json:"save_permit"`
		SyncWork   time.Duration `json:"sync_work"`
		RejectMask uint8         `json:"reject_mask"`
	}
	Other struct {
		Duration   time.Duration
		Count      uint64
		Unfinished bool
		CauseMask  uint8 `json:"cause_mask"`
	}
}
type ownerObservationCoverage struct{ Phases [5]ownerObservationPhase }

func ownerObservationWire(t *testing.T, sample *rpctrace.Sample) *ownerObservationCoverage {
	t.Helper()
	raw, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Coverage *struct {
			Owner *ownerObservationCoverage `json:"owner_storage"`
		} `json:"barrier_coverage"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Coverage == nil || wire.Coverage.Owner == nil {
		t.Fatal("same-request owner coverage missing after proven public Runtime setup")
	}
	return wire.Coverage.Owner
}
