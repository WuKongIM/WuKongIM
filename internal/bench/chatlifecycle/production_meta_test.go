package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func TestProductionMetaExpectationUsesWorkerVectorsAndDeterministicGroupCatalog(t *testing.T) {
	cfg := FormalConfig()
	workers := productionMetaWorkerSnapshots(cfg)
	workers[0].MetaCreate.PersonByHashSlot[0] = 7
	workers[1].MetaCreate.PersonByHashSlot[127] = 11
	workers[2].MetaCreate.PersonByHashSlot[255] = 13

	person, groups, err := productionMetaExpectation(cfg, workers)
	if err != nil {
		t.Fatalf("productionMetaExpectation: %v", err)
	}
	if person[0] != 7 || person[127] != 11 || person[255] != 13 {
		t.Fatalf("person expectation lost physical hash slots: [0]=%d [127]=%d [255]=%d", person[0], person[127], person[255])
	}
	var groupTotal uint64
	for hashSlot, count := range groups {
		groupTotal += count
		if count == 0 {
			t.Fatalf("formal deterministic group catalog did not cover hash slot %d", hashSlot)
		}
	}
	if groupTotal != uint64(cfg.Workload.Groups.Small+cfg.Workload.Groups.Medium+cfg.Workload.Groups.Large+cfg.Workload.Groups.VeryLarge) {
		t.Fatalf("group total = %d, want fixed catalog total", groupTotal)
	}
}

func TestProductionMetaControllerCheckpointsThreeNodeMetricsAcrossTwelveLogicalSlots(t *testing.T) {
	cfg := FormalConfig()
	workers := productionMetaWorkerSnapshots(cfg)
	workers[0].MetaCreate.PersonByHashSlot[0] = 5
	workers[1].MetaCreate.PersonByHashSlot[127] = 7
	workers[2].MetaCreate.PersonByHashSlot[255] = 9
	assignment := productionMetaInitialAssignment(t)
	person, groups, err := productionMetaExpectation(cfg, workers)
	if err != nil {
		t.Fatal(err)
	}
	expectedBySlot, expectedTotal, ok := foldMetaCreateExpectation(person, groups, assignment)
	if !ok {
		t.Fatal("foldMetaCreateExpectation rejected production expectation")
	}
	metrics := productionMetaMetricsForExpected(expectedBySlot)
	sources := productionMetaSources(metrics)
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionMetaController(ProductionMetaControllerOptions{
		Config: cfg, Metrics: sources, Accounting: accounting,
	})
	if err != nil {
		t.Fatalf("NewProductionMetaController: %v", err)
	}
	if err := controller.Checkpoint(context.Background(), workers, assignment, false); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	for index, source := range sources {
		if source.(*recordingProductionMetaMetricsSource).calls != 1 {
			t.Fatalf("metrics source %d calls = %d, want 1", index, source.(*recordingProductionMetaMetricsSource).calls)
		}
	}
	snapshot := accounting.Snapshot()
	if snapshot.ExpectedUnique != expectedTotal || snapshot.Created != expectedTotal || snapshot.Checkpoints != 1 {
		t.Fatalf("accounting snapshot = %+v, expected total %d", snapshot, expectedTotal)
	}
	for slot := range formalLogicalSlotGroups {
		if snapshot.ExpectedBySlot[slot] != expectedBySlot[slot] || snapshot.CreatedBySlot[slot] != expectedBySlot[slot] {
			t.Fatalf("logical slot %d accounting = expected %d created %d, want %d", slot+1, snapshot.ExpectedBySlot[slot], snapshot.CreatedBySlot[slot], expectedBySlot[slot])
		}
	}

	for index := range workers {
		workers[index].SnapshotSequence++
	}
	if err := controller.Checkpoint(context.Background(), workers, assignment, true); err != nil {
		t.Fatalf("reheat zero-delta Checkpoint: %v", err)
	}
	if snapshot = accounting.Snapshot(); snapshot.ReheatCreated != 0 || snapshot.Checkpoints != 2 {
		t.Fatalf("reheat zero-delta snapshot = %+v", snapshot)
	}
}

func TestProductionMetaControllerRejectsWorkerRegressionAndOverflowBeforeMetrics(t *testing.T) {
	cfg := LocalConfig()
	assignment := productionMetaInitialAssignment(t)
	workers := productionMetaWorkerSnapshots(cfg)
	workers[0].MetaCreate.PersonByHashSlot[17] = 2
	person, groups, err := productionMetaExpectation(cfg, workers)
	if err != nil {
		t.Fatal(err)
	}
	expected, _, ok := foldMetaCreateExpectation(person, groups, assignment)
	if !ok {
		t.Fatal("fold initial expectation")
	}
	sources := productionMetaSources(productionMetaMetricsForExpected(expected))
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionMetaController(ProductionMetaControllerOptions{Config: cfg, Metrics: sources, Accounting: accounting})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Checkpoint(context.Background(), workers, assignment, false); err != nil {
		t.Fatal(err)
	}

	regressed := append([]WorkerSnapshot(nil), workers...)
	regressed[0].MetaCreate.PersonByHashSlot[17]--
	for index := range regressed {
		regressed[index].SnapshotSequence++
	}
	if err := controller.Checkpoint(context.Background(), regressed, assignment, false); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("regression error = %v, want harness invalid", err)
	}
	for index, source := range sources {
		if source.(*recordingProductionMetaMetricsSource).calls != 1 {
			t.Fatalf("regression called metrics source %d %d times", index, source.(*recordingProductionMetaMetricsSource).calls)
		}
	}
	if snapshot := accounting.Snapshot(); snapshot.Checkpoints != 1 {
		t.Fatalf("regression mutated accounting: %+v", snapshot)
	}

	overflow := productionMetaWorkerSnapshots(cfg)
	overflow[0].MetaCreate.PersonByHashSlot[255] = math.MaxUint64
	overflow[1].MetaCreate.PersonByHashSlot[255] = 1
	if _, _, err := productionMetaExpectation(cfg, overflow); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("overflow error = %v, want harness invalid", err)
	}
}

func TestProductionMetaControllerCancellationDoesNotCheckpointPartialRound(t *testing.T) {
	cfg := LocalConfig()
	started := make(chan struct{}, coordinatorWorkerCount)
	returned := make(chan struct{}, coordinatorWorkerCount)
	sources := [coordinatorWorkerCount]ProductionMetaMetricsSource{}
	for index := range sources {
		sources[index] = blockingProductionMetaMetricsSource{started: started, returned: returned}
	}
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionMetaController(ProductionMetaControllerOptions{Config: cfg, Metrics: sources, Accounting: accounting})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	assignment := productionMetaInitialAssignment(t)
	go func() {
		result <- controller.Checkpoint(ctx, productionMetaWorkerSnapshots(cfg), assignment, false)
	}()
	for index := 0; index < coordinatorWorkerCount; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("metrics source %d did not start", index)
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Checkpoint error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Checkpoint did not return")
	}
	for index := 0; index < coordinatorWorkerCount; index++ {
		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatalf("metrics source %d had not returned before Checkpoint", index)
		}
	}
	if snapshot := accounting.Snapshot(); snapshot.Checkpoints != 0 {
		t.Fatalf("canceled round mutated accounting: %+v", snapshot)
	}
}

func productionMetaWorkerSnapshots(cfg Config) []WorkerSnapshot {
	workers := make([]WorkerSnapshot, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = WorkerSnapshot{
			RunID: cfg.RunID, AssignmentID: "production-meta", Phase: WorkerPhaseRunning,
			SnapshotSequence: 1, Generation: 1, WorkerID: uint64(workerID), WorkerCount: coordinatorWorkerCount,
		}
	}
	return workers
}

func productionMetaInitialAssignment(t *testing.T) LifecycleSlotAssignment {
	t.Helper()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatalf("newInitialLifecycleSlotAssignment: %v", err)
	}
	return assignment
}

type recordingProductionMetaMetricsSource struct {
	mu       sync.Mutex
	snapshot target.MetricsSnapshot
	calls    int
}

func (s *recordingProductionMetaMetricsSource) Metrics(context.Context) (target.MetricsSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.snapshot, nil
}

func productionMetaSources(metrics [coordinatorWorkerCount]target.MetricsSnapshot) [coordinatorWorkerCount]ProductionMetaMetricsSource {
	var sources [coordinatorWorkerCount]ProductionMetaMetricsSource
	for index := range sources {
		sources[index] = &recordingProductionMetaMetricsSource{snapshot: metrics[index]}
	}
	return sources
}

type blockingProductionMetaMetricsSource struct {
	started  chan<- struct{}
	returned chan<- struct{}
}

func (s blockingProductionMetaMetricsSource) Metrics(ctx context.Context) (target.MetricsSnapshot, error) {
	s.started <- struct{}{}
	<-ctx.Done()
	s.returned <- struct{}{}
	return target.MetricsSnapshot{}, ctx.Err()
}

func productionMetaMetricsForExpected(expected [formalLogicalSlotGroups]uint64) [coordinatorWorkerCount]target.MetricsSnapshot {
	var metrics [coordinatorWorkerCount]target.MetricsSnapshot
	for node := range metrics {
		metrics[node].MetaCreatedTotal = map[string]float64{"created": 0, "already_existing": 0, "error": 0}
	}
	for slot, count := range expected {
		node := slot % coordinatorWorkerCount
		metrics[node].MetaCreatedBySlot[slot].Created = count
		metrics[node].MetaCreatedTotal["created"] += float64(count)
	}
	return metrics
}
