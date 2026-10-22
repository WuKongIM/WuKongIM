package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

// metaDiagnosticSource exposes only the existing metrics port to each fixture.
type metaDiagnosticSource struct {
	snapshot target.MetricsSnapshot
	err      error
	calls    int
}

type metaDiagnosticPrivateError struct{}

func (*metaDiagnosticPrivateError) Error() string {
	return "private uid=example credential=hidden /private/source"
}

func (s *metaDiagnosticSource) Metrics(context.Context) (target.MetricsSnapshot, error) {
	s.calls++
	return s.snapshot, s.err
}

type metaDiagnosticFixture struct {
	controller *ProductionMetaController
	accounting *MetaCreateAccounting
	workers    []WorkerSnapshot
	assignment LifecycleSlotAssignment
	sources    [coordinatorWorkerCount]*metaDiagnosticSource
	ctx        context.Context
	reheat     bool
}

func newMetaDiagnosticFixture(t *testing.T, initialized bool) *metaDiagnosticFixture {
	t.Helper()
	cfg := LocalConfig()
	f := &metaDiagnosticFixture{
		accounting: NewMetaCreateAccounting(), workers: productionMetaWorkerSnapshots(cfg),
		assignment: productionMetaInitialAssignment(t), ctx: context.Background(),
	}
	var expected [formalLogicalSlotGroups]uint64
	if initialized {
		f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
		f.workers[0].MetaCreate.GroupByHashSlot[0] = 1
		expected[0] = 2
	}
	metrics := productionMetaMetricsForExpected(expected)
	if initialized {
		metrics[0].MetaCreatedTotal["already_existing"] = 2
		metrics[0].MetaCreatedBySlot[0].AlreadyExisting = 2
	}
	var sources [coordinatorWorkerCount]ProductionMetaMetricsSource
	for node := range sources {
		f.sources[node] = &metaDiagnosticSource{snapshot: metrics[node]}
		sources[node] = f.sources[node]
	}
	var err error
	f.controller, err = NewProductionMetaController(ProductionMetaControllerOptions{Config: cfg, Metrics: sources, Accounting: f.accounting})
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		if err := f.controller.Checkpoint(f.ctx, f.workers, f.assignment, false); err != nil {
			t.Fatal(err)
		}
		for worker := range f.workers {
			f.workers[worker].SnapshotSequence++
		}
	}
	return f
}

// TestMetaCheckpointFailureLeaves exercises the public checkpoint boundary;
// malformed rounds keep the same sentinel, source-call bound and baseline.
func TestMetaCheckpointFailureLeaves(t *testing.T) {
	private := &metaDiagnosticPrivateError{}
	tests := []struct {
		name, reason string
		initialized  bool
		mutate       func(*metaDiagnosticFixture)
		want         error
		metrics      bool
	}{
		{"nil_receiver", "guard", false, func(f *metaDiagnosticFixture) { f.controller = nil }, ErrLifecycleHarnessInvalid, false},
		{"nil_context", "guard", false, func(f *metaDiagnosticFixture) { f.ctx = nil }, ErrLifecycleHarnessInvalid, false},
		{"canceled", "context_canceled", false, func(f *metaDiagnosticFixture) { ctx, cancel := context.WithCancel(f.ctx); cancel(); f.ctx = ctx }, context.Canceled, false},
		{"deadline", "context_deadline", false, func(f *metaDiagnosticFixture) {
			ctx, cancel := context.WithDeadline(f.ctx, time.Unix(1, 0))
			cancel()
			f.ctx = ctx
		}, context.DeadlineExceeded, false},
		{"run_config", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.controller.runID = "" }, ErrLifecycleHarnessInvalid, false},
		{"worker_config", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.controller.workerCount = 2 }, ErrLifecycleHarnessInvalid, false},
		{"group_config", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.controller.groupLimit = 0 }, ErrLifecycleHarnessInvalid, false},
		{"worker_count", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers = f.workers[:2] }, ErrLifecycleHarnessInvalid, false},
		{"worker_count_field", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers[0].WorkerCount = 2 }, ErrLifecycleHarnessInvalid, false},
		{"worker_id_range", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers[0].WorkerID = math.MaxUint64 }, ErrLifecycleHarnessInvalid, false},
		{"worker_duplicate", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers[2].WorkerID = 1 }, ErrLifecycleHarnessInvalid, false},
		{"worker_sequence_zero", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers[0].SnapshotSequence = 0 }, ErrLifecycleHarnessInvalid, false},
		{"worker_phase", "expectation_worker_shape", false, func(f *metaDiagnosticFixture) { f.workers[0].Phase = WorkerPhase("private-phase") }, ErrLifecycleHarnessInvalid, false},
		{"run_fence", "expectation_fence", false, func(f *metaDiagnosticFixture) { f.workers[0].RunID = "private-other-run" }, ErrLifecycleHarnessInvalid, false},
		{"assignment_empty", "expectation_fence", false, func(f *metaDiagnosticFixture) { f.workers[0].AssignmentID = "" }, ErrLifecycleHarnessInvalid, false},
		{"generation_zero", "expectation_fence", false, func(f *metaDiagnosticFixture) { f.workers[0].Generation = 0 }, ErrLifecycleHarnessInvalid, false},
		{"assignment_mismatch", "expectation_fence", false, func(f *metaDiagnosticFixture) { f.workers[1].AssignmentID = "private-other-assignment" }, ErrLifecycleHarnessInvalid, false},
		{"generation_mismatch", "expectation_fence", false, func(f *metaDiagnosticFixture) { f.workers[1].Generation++ }, ErrLifecycleHarnessInvalid, false},
		{"person_sum", "expectation_vector_bound", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.PersonByHashSlot[0] = math.MaxUint64
			f.workers[1].MetaCreate.PersonByHashSlot[0] = 1
		}, ErrLifecycleHarnessInvalid, false},
		{"group_sum", "expectation_vector_bound", false, func(f *metaDiagnosticFixture) {
			f.controller.groupLimit = math.MaxUint64
			f.workers[0].MetaCreate.GroupByHashSlot[0] = math.MaxUint64
			f.workers[1].MetaCreate.GroupByHashSlot[0] = 1
		}, ErrLifecycleHarnessInvalid, false},
		{"worker_group_sum", "expectation_vector_bound", false, func(f *metaDiagnosticFixture) {
			f.controller.groupLimit = math.MaxUint64
			f.workers[0].MetaCreate.GroupByHashSlot[0] = math.MaxUint64
			f.workers[0].MetaCreate.GroupByHashSlot[1] = 1
		}, ErrLifecycleHarnessInvalid, false},
		{"global_group_sum", "expectation_vector_bound", false, func(f *metaDiagnosticFixture) {
			f.controller.groupLimit = math.MaxUint64
			f.workers[0].MetaCreate.GroupByHashSlot[0] = math.MaxUint64
			f.workers[1].MetaCreate.GroupByHashSlot[1] = 1
		}, ErrLifecycleHarnessInvalid, false},
		{"group_limit", "expectation_vector_bound", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.GroupByHashSlot[0] = f.controller.groupLimit + 1
		}, ErrLifecycleHarnessInvalid, false},
		{"checkpoint_assignment", "checkpoint_fence", true, func(f *metaDiagnosticFixture) {
			for i := range f.workers {
				f.workers[i].AssignmentID = "private-new-assignment"
			}
		}, ErrLifecycleHarnessInvalid, false},
		{"checkpoint_generation", "checkpoint_fence", true, func(f *metaDiagnosticFixture) {
			for i := range f.workers {
				f.workers[i].Generation++
			}
		}, ErrLifecycleHarnessInvalid, false},
		{"checkpoint_sequence", "checkpoint_sequence", true, func(f *metaDiagnosticFixture) { f.workers[0].SnapshotSequence-- }, ErrLifecycleHarnessInvalid, false},
		{"checkpoint_person", "checkpoint_vector_regression", true, func(f *metaDiagnosticFixture) { f.workers[0].MetaCreate.PersonByHashSlot[0]-- }, ErrLifecycleHarnessInvalid, false},
		{"checkpoint_group", "checkpoint_vector_regression", true, func(f *metaDiagnosticFixture) { f.workers[0].MetaCreate.GroupByHashSlot[0]-- }, ErrLifecycleHarnessInvalid, false},
		{"metrics_fetch", "metrics_fetch", false, func(f *metaDiagnosticFixture) { f.sources[0].err = private }, ErrLifecycleHarnessInvalid, true},
		{"preview_assignment", "metrics_preview", false, func(f *metaDiagnosticFixture) { f.assignment = LifecycleSlotAssignment{} }, ErrLifecycleHarnessInvalid, true},
		{"preview_missing_result", "metrics_preview", false, func(f *metaDiagnosticFixture) { delete(f.sources[0].snapshot.MetaCreatedTotal, "created") }, ErrLifecycleHarnessInvalid, true},
		{"preview_fractional_result", "metrics_preview", false, func(f *metaDiagnosticFixture) { f.sources[0].snapshot.MetaCreatedTotal["created"] = 0.5 }, ErrLifecycleHarnessInvalid, true},
		{"preview_slot_total", "metrics_preview", false, func(f *metaDiagnosticFixture) { f.sources[0].snapshot.MetaCreatedTotal["created"] = 1 }, ErrLifecycleHarnessInvalid, true},
		{"settle_missing", "settle_wait", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
			f.controller.settleWait = nil
		}, ErrLifecycleHarnessInvalid, true},
		{"settle_private_error", "settle_wait", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
			f.controller.settleWait = func(context.Context) error { return private }
		}, private, true},
		{"settle_canceled", "context_canceled", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
			f.controller.settleWait = func(context.Context) error { return context.Canceled }
		}, context.Canceled, true},
		{"settle_deadline", "context_deadline", false, func(f *metaDiagnosticFixture) {
			f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
			f.controller.settleWait = func(context.Context) error { return context.DeadlineExceeded }
		}, context.DeadlineExceeded, true},
		{"accounting_initial_reheat", "accounting", false, func(f *metaDiagnosticFixture) { f.reheat = true }, ErrLifecycleHarnessInvalid, true},
		{"accounting_counter_regression", "accounting", true, func(f *metaDiagnosticFixture) {
			f.sources[0].snapshot.MetaCreatedTotal["already_existing"] = 1
			f.sources[0].snapshot.MetaCreatedBySlot[0].AlreadyExisting = 1
		}, ErrLifecycleHarnessInvalid, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newMetaDiagnosticFixture(t, test.initialized)
			test.mutate(f)
			before := f.accounting.Snapshot()
			var sequences [coordinatorWorkerCount]uint64
			var person, groups [coordinatorWorkerCount]MetaCreateHashSlotCounts
			if f.controller != nil {
				sequences, person, groups = f.controller.sequences, f.controller.person, f.controller.groups
			}
			var calls [coordinatorWorkerCount]int
			for node, source := range f.sources {
				calls[node] = source.calls
			}
			err := f.controller.Checkpoint(f.ctx, f.workers, f.assignment, f.reheat)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want original sentinel", err)
			}
			if test.want == private {
				var cause *metaDiagnosticPrivateError
				if !errors.As(err, &cause) || cause != private {
					t.Error("typed private cause chain lost")
				}
			}
			failure := finalizationDiagnostic(err, "meta_checkpoint", "invalid")
			if failure.Stage != "meta_checkpoint" || failure.Reason != test.reason {
				t.Errorf("diagnostic = %+v, want meta_checkpoint/%s", failure, test.reason)
			}
			body, _ := json.Marshal(failure.Bounded())
			if strings.Contains(err.Error(), "private") || strings.Contains(string(body), "private") {
				t.Error("private cause leaked into formatting")
			}
			if !reflect.DeepEqual(before, f.accounting.Snapshot()) {
				t.Error("invalid checkpoint mutated accounting")
			}
			if f.controller != nil && (f.controller.sequences != sequences || f.controller.person != person || f.controller.groups != groups) {
				t.Error("invalid checkpoint advanced controller baseline")
			}
			for node, source := range f.sources {
				want := calls[node]
				if test.metrics {
					want++
				}
				if source.calls != want {
					t.Errorf("source %d calls = %d, want %d", node, source.calls, want)
				}
			}
		})
	}
}

func TestMetaCheckpointCancellationJoinsEverySource(t *testing.T) {
	f := newMetaDiagnosticFixture(t, false)
	started, returned := make(chan struct{}, coordinatorWorkerCount), make(chan struct{}, coordinatorWorkerCount)
	for node := range f.controller.metrics {
		f.controller.metrics[node] = blockingProductionMetaMetricsSource{started: started, returned: returned}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.controller.Checkpoint(ctx, f.workers, f.assignment, false) }()
	for range coordinatorWorkerCount {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("source did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || finalizationDiagnostic(err, "meta_checkpoint", "invalid").Reason != "context_canceled" {
			t.Errorf("cancellation lost its leaf or sentinel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled round did not join")
	}
	for range coordinatorWorkerCount {
		select {
		case <-returned:
		default:
			t.Fatal("checkpoint returned before every source joined")
		}
	}
	if f.accounting.Snapshot().Checkpoints != 0 || f.controller.initialized {
		t.Error("partial canceled round committed")
	}
}

func TestMetaCheckpointProductDeficitKeepsExistingBound(t *testing.T) {
	f := newMetaDiagnosticFixture(t, false)
	f.workers[0].MetaCreate.PersonByHashSlot[0] = 1
	waits := 0
	f.controller.settleWait = func(context.Context) error { waits++; return nil }
	err := f.controller.Checkpoint(f.ctx, f.workers, f.assignment, false)
	if !errors.Is(err, ErrLifecycleProductFailure) || errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("deficit reclassified: %v", err)
	}
	var diagnostic *finalizationError
	if errors.As(err, &diagnostic) {
		t.Error("product deficit became a finalization error")
	}
	if waits != 4 || productionMetaSettleDelay != 25*time.Millisecond {
		t.Errorf("settle bound changed: waits=%d delay=%v", waits, productionMetaSettleDelay)
	}
	for node, source := range f.sources {
		if source.calls != 5 {
			t.Errorf("source %d calls=%d, want5", node, source.calls)
		}
	}
	if f.accounting.Snapshot().Checkpoints != 1 || f.controller.initialized {
		t.Error("product checkpoint commit semantics changed")
	}
}

func TestMetaCheckpointLeafProjectionAndFinalizeCompleteness(t *testing.T) {
	reasons := []string{"guard", "context_canceled", "context_deadline", "expectation_worker_shape", "expectation_fence", "expectation_vector_bound", "checkpoint_fence", "checkpoint_sequence", "checkpoint_vector_regression", "metrics_fetch", "metrics_preview", "settle_wait", "accounting", "invalid"}
	private := &metaDiagnosticPrivateError{}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			controller, cut, diagnostics, _ := newFinalizeDiagnosticFixture(t)
			controller.meta = finalizeMetaProbe{err: newFinalizationError("meta_checkpoint", reason, private)}
			before := controller.frozen
			err := controller.Finalize(context.Background(), cut)
			if !errors.Is(err, private) || !errors.Is(err, errProductionController) {
				t.Fatal("private cause chain lost")
			}
			var cause *metaDiagnosticPrivateError
			if !errors.As(err, &cause) || cause != private {
				t.Error("typed private cause chain lost")
			}
			failure := finalizationDiagnostic(err, "controller_unknown", "invalid")
			if failure.Stage != "meta_checkpoint" || failure.Reason != reason || failure.ObservedOutcome != before.Outcome || failure.ObservedCause != before.Cause || !failure.ObservedTerminal {
				t.Errorf("leaf/frozen observation lost: %+v", failure)
			}
			if strings.Contains(err.Error(), "private") || bytes.Contains(diagnostics.Bytes(), []byte("private")) {
				t.Error("private chain formatted")
			}
			if _, err := os.Stat(filepath.Join(controller.outputDir, "final.json")); !os.IsNotExist(err) {
				t.Error("incomplete finalization invented a report")
			}
			if !reflect.DeepEqual(controller.frozen, before) {
				t.Error("diagnostic changed the frozen verdict")
			}
		})
	}
	for _, failure := range []FinalizationFailure{{Stage: "meta_checkpoint", Reason: "private-reason"}, {Stage: "private-stage", Reason: "metrics_fetch"}} {
		got := failure.Bounded()
		if got.Stage != "controller_unknown" || got.Reason != "invalid" {
			t.Errorf("forged leaf accepted: %+v", got)
		}
	}
}
