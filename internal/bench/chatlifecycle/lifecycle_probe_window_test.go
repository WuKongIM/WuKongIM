package chatlifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// Exercise the real production polling seam with a queued tick and an RPC
// interval; no real sleep or server clock assumption is needed for this race.
func TestProductionLifecycleProbeObservationWindow(t *testing.T) {
	base := time.Unix(1_720_700_000, 0).UTC()
	for _, tc := range []struct {
		name          string
		start, end    time.Duration
		wantCold      uint64
		wantPremature bool
	}{
		{"queued tick before boundary actual probe after", time.Millisecond, 2 * time.Millisecond, 1, false},
		{"request crosses lower boundary remains unproven", -time.Millisecond, time.Millisecond, 0, false},
		{"definite early absence still fails", -2 * time.Millisecond, -time.Millisecond, 0, true},
		{"exact lower boundary is eligible", 0, time.Millisecond, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := lifecycleTestCandidates(t, base)[0]
			proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err != nil {
				t.Fatal(err)
			}
			if err := proof.Observe(base, lifecycleRows(candidate, "active", 10, 10)); err != nil {
				t.Fatal(err)
			}
			clock := newProductionLifecycleClock(candidate.QuietNotBefore.Add(tc.start))
			prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{candidate.QuietNotBefore.Add(tc.end)}}
			worker := &productionLifecycleRotatingWorker{workerID: 0}
			runner := &ProductionLifecycle{options: ProductionLifecycleOptions{
				Clock: clock, Prober: prober, Workers: [3]ProductionLifecycleWorker{worker, worker, worker},
				ProbeOptions: LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1, RequestTimeout: time.Second},
			}}
			cohort := &productionLifecycleCohort{proof: proof, candidates: []LifecycleCandidate{candidate},
				owner: map[string]uint64{candidate.ChannelID: 0}, approved: make(map[string]bool)}
			err = runner.pollCohorts(context.Background(), WorkerFence{}, []*productionLifecycleCohort{cohort})
			if tc.wantPremature {
				if !errors.Is(err, ErrLifecycleProductFailure) || proof.Snapshot().ProductFailureReasons.PrematureAbsence != 1 {
					t.Fatalf("error=%v snapshot=%+v, want original premature-absence gate", err, proof.Snapshot())
				}
				return
			}
			if err != nil {
				t.Fatalf("error=%v snapshot=%+v; ticker is not an observation timestamp", err, proof.Snapshot())
			}
			if got := proof.Snapshot(); got.ColdEligible != tc.wantCold || got.Reheated != tc.wantCold || got.ProductFailures != 0 || got.HarnessFailures != 0 {
				t.Fatalf("snapshot=%+v, want cold/reheated=%d", got, tc.wantCold)
			}
		})
	}
}

func TestLifecycleProbeWindowsStayBoundToEachBatch(t *testing.T) {
	base := time.Unix(1_720_800_000, 0).UTC()
	candidates := lifecycleTestCandidates(t, base)[:2]
	proof, _ := NewLifecycleProof(candidates)
	// Establish loaded state using the same real batch normalization path.
	loaded := &fakeLifecycleProber{nodes: 3, sequence: 10}
	options := LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}
	if _, err := proof.Poll(context.Background(), loaded, newProductionLifecycleClock(base), options); err != nil {
		t.Fatal(err)
	}
	n := candidates[0].QuietNotBefore
	clock := newProductionLifecycleClock(n.Add(-time.Millisecond))
	prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{n.Add(time.Millisecond), n.Add(2 * time.Millisecond)}}
	if _, err := proof.Poll(context.Background(), prober, clock, options); err != nil {
		t.Fatal(err)
	}
	if got := proof.Snapshot(); got.ColdEligible != 1 || got.ProductFailures != 0 {
		t.Fatalf("snapshot=%+v, only the wholly post-boundary batch can qualify", got)
	}
	prober.ends = append(prober.ends, n.Add(3*time.Millisecond), n.Add(4*time.Millisecond))
	if _, err := proof.Poll(context.Background(), prober, clock, options); err != nil || proof.Snapshot().ColdEligible != 2 {
		t.Fatalf("fresh probe error=%v snapshot=%+v", err, proof.Snapshot())
	}
}

func TestLifecycleProbeWindowPreservesNegativeAndUnknownEvidence(t *testing.T) {
	base := time.Unix(1_720_900_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	for _, tc := range []struct {
		name       string
		start, end time.Time
		phase      productionLifecyclePhase
		want       error
	}{
		{"zero clock", time.Time{}, candidate.QuietNotBefore, productionLifecycleMissing, ErrLifecycleHarnessInvalid},
		{"regressing clock", candidate.QuietNotBefore, candidate.QuietNotBefore.Add(-time.Millisecond), productionLifecycleMissing, ErrLifecycleHarnessInvalid},
		{"upper boundary straddle missing stays unknown", candidate.QuietDeadline.Add(-time.Millisecond), candidate.QuietDeadline.Add(time.Millisecond), productionLifecycleMissing, nil},
		{"upper boundary straddle loaded stays unknown", candidate.QuietDeadline.Add(-time.Millisecond), candidate.QuietDeadline.Add(time.Millisecond), productionLifecycleLoaded, nil},
		{"definitely late missing fails original deadline", candidate.QuietDeadline.Add(time.Millisecond), candidate.QuietDeadline.Add(2 * time.Millisecond), productionLifecycleMissing, ErrLifecycleProductFailure},
		{"definitely late loaded fails original deadline", candidate.QuietDeadline, candidate.QuietDeadline.Add(time.Millisecond), productionLifecycleLoaded, ErrLifecycleProductFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err := proof.Observe(base, lifecycleRows(candidate, "active", 10, 10)); err != nil {
				t.Fatal(err)
			}
			clock := newProductionLifecycleClock(tc.start)
			prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{tc.end}, phases: []productionLifecyclePhase{tc.phase}}
			_, err := proof.Poll(context.Background(), prober, clock, LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1})
			if !errors.Is(err, tc.want) || proof.Snapshot().ColdEligible != 0 {
				t.Fatalf("error=%v snapshot=%+v, want %v without qualification", err, proof.Snapshot(), tc.want)
			}
		})
	}
}

func TestLifecycleProbeStraddleCannotHideUnexpectedReload(t *testing.T) {
	base := time.Unix(1_721_000_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err := proof.Observe(base, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatal(err)
	}
	clock := newProductionLifecycleClock(candidate.QuietNotBefore.Add(-time.Millisecond))
	prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{candidate.QuietNotBefore.Add(time.Millisecond)}}
	if _, err := proof.Poll(context.Background(), prober, clock, LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if err := proof.Observe(clock.Now(), lifecycleRows(candidate, "active", 10, 10)); !errors.Is(err, ErrLifecycleProductFailure) || proof.Snapshot().ProductFailureReasons.UnexpectedReload != 1 {
		t.Fatalf("error=%v snapshot=%+v, uncertain timing cannot erase observed absence", err, proof.Snapshot())
	}
}

func TestLifecycleProbeEarlyAbsenceRollsBackOtherBatch(t *testing.T) {
	base := time.Unix(1_721_100_000, 0).UTC()
	candidates := lifecycleTestCandidates(t, base)[:2]
	candidates[1].QuietNotBefore = candidates[1].QuietNotBefore.Add(time.Minute)
	proof, _ := NewLifecycleProof(candidates)
	options := LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}
	if _, err := proof.Poll(context.Background(), &fakeLifecycleProber{nodes: 3, sequence: 10}, newProductionLifecycleClock(base), options); err != nil {
		t.Fatal(err)
	}
	n := candidates[0].QuietNotBefore
	clock := newProductionLifecycleClock(n.Add(time.Millisecond))
	prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{n.Add(2 * time.Millisecond), n.Add(3 * time.Millisecond)}}
	_, err := proof.Poll(context.Background(), prober, clock, options)
	if got := proof.Snapshot(); !errors.Is(err, ErrLifecycleProductFailure) || got.ColdEligible != 0 || got.ProductFailureReasons.PrematureAbsence != 1 {
		t.Fatalf("error=%v snapshot=%+v, definite early absence must roll back the whole proof", err, got)
	}
}

func TestLifecycleProbeReheatObservationWindow(t *testing.T) {
	base := time.Unix(1_721_200_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	for _, tc := range []struct {
		name       string
		start, end time.Time
		phase      productionLifecyclePhase
		want       error
		completed  uint64
	}{
		{"definite early sequence fails", candidate.ReheatAt.Add(-2 * time.Millisecond), candidate.ReheatAt.Add(-time.Millisecond), productionLifecycleReloaded, ErrLifecycleProductFailure, 0},
		{"due straddle cannot complete", candidate.ReheatAt.Add(-time.Millisecond), candidate.ReheatAt.Add(time.Millisecond), productionLifecycleReloaded, nil, 0},
		{"post due sequence completes", candidate.ReheatAt, candidate.ReheatAt.Add(time.Millisecond), productionLifecycleReloaded, nil, 1},
		{"timeout straddle stays unproven", candidate.ReheatAt.Add(lifecycleReheatDeadline - time.Millisecond), candidate.ReheatAt.Add(lifecycleReheatDeadline + time.Millisecond), productionLifecycleMissing, nil, 0},
		{"definite timeout still fails", candidate.ReheatAt.Add(lifecycleReheatDeadline + time.Millisecond), candidate.ReheatAt.Add(lifecycleReheatDeadline + 2*time.Millisecond), productionLifecycleMissing, ErrLifecycleProductFailure, 0},
		{"late valid sequence still completes", candidate.ReheatAt.Add(lifecycleReheatDeadline + time.Millisecond), candidate.ReheatAt.Add(lifecycleReheatDeadline + 2*time.Millisecond), productionLifecycleReloaded, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err := proof.Observe(base, lifecycleRows(candidate, "active", 10, 10)); err != nil {
				t.Fatal(err)
			}
			if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
				t.Fatal(err)
			}
			if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
				t.Fatal(err)
			}
			clock := newProductionLifecycleClock(tc.start)
			prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{tc.end}, phases: []productionLifecyclePhase{tc.phase}}
			_, err := proof.Poll(context.Background(), prober, clock, LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1})
			if !errors.Is(err, tc.want) || proof.Snapshot().Completed != tc.completed {
				t.Fatalf("error=%v snapshot=%+v, want %v completed=%d", err, proof.Snapshot(), tc.want, tc.completed)
			}
		})
	}
}

func TestProductionLifecycleTimelyApprovalCanReturnAfterDue(t *testing.T) {
	base := time.Unix(1_721_300_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err := proof.Observe(base, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatal(err)
	}
	clock := newProductionLifecycleClock(candidate.ReheatAt.Add(-2 * time.Millisecond))
	// Cold proof is already established well before the approval boundary.
	if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
		t.Fatal(err)
	}
	worker := &lifecycleWindowApprovalWorker{clock: clock, responseAt: candidate.ReheatAt.Add(time.Millisecond)}
	runner := &ProductionLifecycle{options: ProductionLifecycleOptions{Clock: clock,
		Workers: [3]ProductionLifecycleWorker{worker, worker, worker}, ProbeOptions: LifecycleProbeOptions{RequestTimeout: time.Second}}}
	cohort := &productionLifecycleCohort{proof: proof, candidates: []LifecycleCandidate{candidate},
		owner: map[string]uint64{candidate.ChannelID: 0}, approved: make(map[string]bool)}
	for _, err := range runner.reheatColdCandidates(context.Background(), WorkerFence{}, []*productionLifecycleCohort{cohort}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := proof.Snapshot(); got.Reheated != 1 || got.HarnessFailures != 0 {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestLifecycleProbeClockCannotRegressBetweenRounds(t *testing.T) {
	base := time.Unix(1_721_400_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	clock := newProductionLifecycleClock(candidate.QuietNotBefore.Add(-2 * time.Second))
	prober := &lifecycleWindowProber{clock: clock,
		ends:   []time.Time{candidate.QuietNotBefore.Add(-time.Second), candidate.QuietNotBefore.Add(-2500 * time.Millisecond)},
		phases: []productionLifecyclePhase{productionLifecycleLoaded, productionLifecycleMissing}}
	options := LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}
	if _, err := proof.Poll(context.Background(), prober, clock, options); err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	clock.now = candidate.QuietNotBefore.Add(-3 * time.Second)
	clock.mu.Unlock()
	_, err := proof.Poll(context.Background(), prober, clock, options)
	if !errors.Is(err, ErrLifecycleHarnessInvalid) || proof.Snapshot().ProductFailures != 0 || proof.Snapshot().ColdEligible != 0 {
		t.Fatalf("error=%v snapshot=%+v; invalid clock cannot become product evidence", err, proof.Snapshot())
	}
}

func TestLifecycleProbeClockCannotRegressBetweenSerialBatches(t *testing.T) {
	base := time.Unix(1_721_600_000, 0).UTC()
	candidates := lifecycleTestCandidates(t, base)[:2]
	proof, _ := NewLifecycleProof(candidates)
	options := LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}
	if _, err := proof.Poll(context.Background(), &fakeLifecycleProber{nodes: 3, sequence: 10}, newProductionLifecycleClock(base), options); err != nil {
		t.Fatal(err)
	}
	n := candidates[0].QuietNotBefore
	clock := &lifecycleScriptClock{ObserverClock: newProductionLifecycleClock(base), values: []time.Time{
		n.Add(time.Millisecond), n.Add(2 * time.Millisecond), n.Add(-2 * time.Millisecond), n.Add(-time.Millisecond),
	}}
	_, err := proof.Poll(context.Background(), &fakeLifecycleProber{nodes: 3, status: "missing"}, clock, options)
	if got := proof.Snapshot(); !errors.Is(err, ErrLifecycleHarnessInvalid) || got.ProductFailures != 0 || got.ColdEligible != 0 {
		t.Fatalf("error=%v snapshot=%+v; same-round clock regression is invalid evidence", err, got)
	}
}

type lifecycleScriptClock struct {
	ObserverClock
	values []time.Time
	next   int
}

func (c *lifecycleScriptClock) Now() time.Time {
	now := c.values[c.next]
	c.next++
	return now
}

func TestLifecycleProbeDueStraddleRetainsNegativeSequenceEvidence(t *testing.T) {
	base := time.Unix(1_721_500_000, 0).UTC()
	candidate := lifecycleTestCandidates(t, base)[0]
	for _, partialInitial := range []bool{false, true} {
		t.Run(fmt.Sprint("partial_initial=", partialInitial), func(t *testing.T) {
			proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
			initial := lifecycleRows(candidate, "active", 10, 10)
			if partialInitial {
				initial = lifecycleRowsWithRoles(candidate, [3]string{"leader", "missing", "missing"}, 10, 10)
			}
			if err := proof.Observe(base, initial); err != nil {
				t.Fatal(err)
			}
			if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
				t.Fatal(err)
			}
			if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
				t.Fatal(err)
			}
			clock := newProductionLifecycleClock(candidate.ReheatAt.Add(-time.Millisecond))
			prober := &lifecycleWindowProber{clock: clock, ends: []time.Time{candidate.ReheatAt.Add(time.Millisecond)},
				phases: []productionLifecyclePhase{productionLifecycleReloaded}, mutate: func(rows []model.ChannelRuntimeProbeResult) {
					for node := range rows {
						v := uint64(20)
						if partialInitial && node == 1 {
							v = 9
						}
						rows[node].Channels[0].LEO, rows[node].Channels[0].HW, rows[node].Channels[0].CheckpointHW = v, v, v
					}
				}}
			_, err := proof.Poll(context.Background(), prober, clock, LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1})
			if partialInitial {
				if !errors.Is(err, ErrLifecycleProductFailure) || proof.Snapshot().ProductFailureReasons.SequenceProof != 1 {
					t.Fatalf("error=%v snapshot=%+v; timing cannot hide a definite sequence reset", err, proof.Snapshot())
				}
				return
			}
			if err != nil || proof.Snapshot().Completed != 0 {
				t.Fatalf("straddle error=%v snapshot=%+v", err, proof.Snapshot())
			}
			err = proof.Observe(clock.Now(), lifecycleRows(candidate, "active", 11, 11))
			if !errors.Is(err, ErrLifecycleProductFailure) || proof.Snapshot().ProductFailureReasons.WatermarkRegression != 1 {
				t.Fatalf("error=%v snapshot=%+v; straddle must retain observed watermarks", err, proof.Snapshot())
			}
		})
	}
}

type lifecycleWindowApprovalWorker struct {
	productionLifecycleRotatingWorker
	clock      *productionLifecycleClock
	responseAt time.Time
}

func (w *lifecycleWindowApprovalWorker) ApproveLifecycleReheat(_ context.Context, request WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error) {
	w.clock.mu.Lock()
	w.clock.now = w.responseAt
	w.clock.mu.Unlock()
	return WorkerLifecycleReheatResponse{WorkerFence: request.WorkerFence, WorkerID: 0, WorkerCount: 3, Approved: uint16(len(request.Items))}, nil
}

type lifecycleWindowProber struct {
	clock  *productionLifecycleClock
	ends   []time.Time
	phases []productionLifecyclePhase
	mutate func([]model.ChannelRuntimeProbeResult)
	call   int
}

func (p *lifecycleWindowProber) ProbeChannelRuntimeAll(_ context.Context, request model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	p.clock.mu.Lock()
	p.clock.now = p.ends[p.call]
	p.clock.mu.Unlock()
	phase := productionLifecycleMissing
	if len(p.phases) != 0 {
		phase = p.phases[p.call]
	}
	p.call++
	rows := make([]model.ChannelRuntimeProbeResult, 3)
	for node := range rows {
		rows[node].NodeID, rows[node].Checked = uint64(node+1), len(request.Channels)
		for _, identity := range request.Channels {
			row := model.ChannelRuntimeProbeChannel{
				ChannelID: identity.ChannelID, ChannelType: identity.ChannelType, Role: "missing", Status: "missing",
			}
			if phase != productionLifecycleMissing {
				row.Role, row.Status, row.LEO, row.HW, row.CheckpointHW = "follower", "active", 10, 10, 10
				if node == 0 {
					row.Role = "leader"
				}
				if phase == productionLifecycleReloaded {
					row.LEO, row.HW, row.CheckpointHW = 11, 11, 11
				}
			}
			rows[node].Channels = append(rows[node].Channels, row)
		}
	}
	if p.mutate != nil {
		p.mutate(rows)
	}
	return rows, nil
}
