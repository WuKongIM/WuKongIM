package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

const (
	productionMetaSettleAttempts = 5
	productionMetaSettleDelay    = 25 * time.Millisecond
)

// ProductionMetaMetricsSource returns one authoritative service-node metrics
// snapshot for the exact checkpoint round represented by its context.
type ProductionMetaMetricsSource interface {
	Metrics(context.Context) (target.MetricsSnapshot, error)
}

// ProductionMetaControllerOptions binds one validated run to exactly three
// service-node metrics sources and one cumulative accounting reducer.
type ProductionMetaControllerOptions struct {
	Config     Config
	Metrics    [coordinatorWorkerCount]ProductionMetaMetricsSource
	Accounting *MetaCreateAccounting
}

// ProductionMetaController reconciles successful first-SEND worker create
// expectations with one same-round scrape from each service node.
type ProductionMetaController struct {
	mu sync.Mutex

	runID       string
	workerCount uint64
	groupLimit  uint64
	metrics     [coordinatorWorkerCount]ProductionMetaMetricsSource
	accounting  *MetaCreateAccounting
	settleWait  func(context.Context) error

	initialized  bool
	assignmentID string
	generation   uint64
	sequences    [coordinatorWorkerCount]uint64
	person       [coordinatorWorkerCount]MetaCreateHashSlotCounts
	groups       [coordinatorWorkerCount]MetaCreateHashSlotCounts
}

// NewProductionMetaController validates immutable expectation inputs without
// issuing a metrics request or mutating accounting state.
func NewProductionMetaController(options ProductionMetaControllerOptions) (*ProductionMetaController, error) {
	if options.Config.Validate() != nil || options.Config.Workload.Workers != coordinatorWorkerCount || options.Accounting == nil {
		return nil, ErrLifecycleHarnessInvalid
	}
	for _, source := range options.Metrics {
		if source == nil {
			return nil, ErrLifecycleHarnessInvalid
		}
	}
	return &ProductionMetaController{
		runID: options.Config.RunID, workerCount: uint64(options.Config.Workload.Workers),
		groupLimit: productionMetaGroupLimit(options.Config),
		metrics:    options.Metrics, accounting: options.Accounting,
		settleWait: waitProductionMetaSettle,
	}, nil
}

// Checkpoint aggregates exactly three generation-fenced worker vectors, then
// reconciles them against three same-round metrics snapshots. Failed or
// canceled rounds never advance the controller baseline.
func (c *ProductionMetaController) Checkpoint(
	ctx context.Context,
	workers []WorkerSnapshot,
	assignment LifecycleSlotAssignment,
	reheat bool,
) error {
	if c == nil || ctx == nil {
		return ErrLifecycleHarnessInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	expectation, err := collectProductionMetaExpectation(c.runID, c.workerCount, c.groupLimit, workers)
	if err != nil {
		return err
	}
	if c.initialized {
		if expectation.assignmentID != c.assignmentID || expectation.generation != c.generation {
			return ErrLifecycleHarnessInvalid
		}
		for workerID := range expectation.personByWorker {
			if expectation.sequences[workerID] <= c.sequences[workerID] {
				return ErrLifecycleHarnessInvalid
			}
			for hashSlot := range expectation.personByWorker[workerID] {
				if expectation.personByWorker[workerID][hashSlot] < c.person[workerID][hashSlot] ||
					expectation.groupByWorker[workerID][hashSlot] < c.groups[workerID][hashSlot] {
					return ErrLifecycleHarnessInvalid
				}
			}
		}
	}
	metrics, err := c.collectSettledMetrics(ctx, expectation.person, expectation.groups, assignment)
	if err != nil {
		return err
	}
	if err := c.accounting.Checkpoint(expectation.person, expectation.groups, assignment, metrics, reheat); err != nil {
		return err
	}
	c.initialized = true
	c.assignmentID = expectation.assignmentID
	c.generation = expectation.generation
	c.sequences = expectation.sequences
	c.person = expectation.personByWorker
	c.groups = expectation.groupByWorker
	return nil
}

// collectSettledMetrics gives authoritative counters one short bounded window
// to become visible after a final worker SENDACK. It never commits a preview,
// and a stable deficit is still handed to accounting as product evidence.
func (c *ProductionMetaController) collectSettledMetrics(
	ctx context.Context,
	person MetaCreateHashSlotCounts,
	groups MetaCreateHashSlotCounts,
	assignment LifecycleSlotAssignment,
) ([coordinatorWorkerCount]target.MetricsSnapshot, error) {
	for attempt := 0; attempt < productionMetaSettleAttempts; attempt++ {
		metrics, err := collectProductionMetaMetrics(ctx, c.metrics)
		if err != nil {
			return [coordinatorWorkerCount]target.MetricsSnapshot{}, err
		}
		preview := NewMetaCreateAccounting()
		previewErr := preview.Checkpoint(person, groups, assignment, metrics, false)
		switch {
		case previewErr == nil:
			return metrics, nil
		case !errors.Is(previewErr, ErrLifecycleProductFailure):
			return [coordinatorWorkerCount]target.MetricsSnapshot{}, previewErr
		case attempt == productionMetaSettleAttempts-1:
			return metrics, nil
		}
		if c.settleWait == nil {
			return [coordinatorWorkerCount]target.MetricsSnapshot{}, ErrLifecycleHarnessInvalid
		}
		if err := c.settleWait(ctx); err != nil {
			return [coordinatorWorkerCount]target.MetricsSnapshot{}, err
		}
	}
	return [coordinatorWorkerCount]target.MetricsSnapshot{}, ErrLifecycleHarnessInvalid
}

func waitProductionMetaSettle(ctx context.Context) error {
	timer := time.NewTimer(productionMetaSettleDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func productionMetaExpectation(cfg Config, workers []WorkerSnapshot) (MetaCreateHashSlotCounts, MetaCreateHashSlotCounts, error) {
	if cfg.Validate() != nil || cfg.Workload.Workers != coordinatorWorkerCount {
		return MetaCreateHashSlotCounts{}, MetaCreateHashSlotCounts{}, ErrLifecycleHarnessInvalid
	}
	expectation, err := collectProductionMetaExpectation(
		cfg.RunID, uint64(cfg.Workload.Workers), productionMetaGroupLimit(cfg), workers,
	)
	if err != nil {
		return MetaCreateHashSlotCounts{}, MetaCreateHashSlotCounts{}, err
	}
	return expectation.person, expectation.groups, nil
}

type productionMetaExpectationSnapshot struct {
	person         MetaCreateHashSlotCounts
	groups         MetaCreateHashSlotCounts
	personByWorker [coordinatorWorkerCount]MetaCreateHashSlotCounts
	groupByWorker  [coordinatorWorkerCount]MetaCreateHashSlotCounts
	assignmentID   string
	generation     uint64
	sequences      [coordinatorWorkerCount]uint64
}

func collectProductionMetaExpectation(
	runID string,
	workerCount uint64,
	groupLimit uint64,
	workers []WorkerSnapshot,
) (productionMetaExpectationSnapshot, error) {
	var expectation productionMetaExpectationSnapshot
	if runID == "" || workerCount != coordinatorWorkerCount || groupLimit == 0 || len(workers) != coordinatorWorkerCount {
		return expectation, ErrLifecycleHarnessInvalid
	}
	var seen [coordinatorWorkerCount]bool
	var groupTotal uint64
	for _, snapshot := range workers {
		if snapshot.RunID != runID || snapshot.AssignmentID == "" || snapshot.Generation == 0 ||
			snapshot.WorkerCount != workerCount || snapshot.WorkerID >= workerCount || snapshot.SnapshotSequence == 0 ||
			(snapshot.Phase != WorkerPhaseRunning && snapshot.Phase != WorkerPhaseFinal) || seen[snapshot.WorkerID] {
			return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
		}
		if expectation.assignmentID == "" {
			expectation.assignmentID, expectation.generation = snapshot.AssignmentID, snapshot.Generation
		} else if snapshot.AssignmentID != expectation.assignmentID || snapshot.Generation != expectation.generation {
			return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
		}
		workerID := int(snapshot.WorkerID)
		seen[workerID] = true
		expectation.sequences[workerID] = snapshot.SnapshotSequence
		expectation.personByWorker[workerID] = snapshot.MetaCreate.PersonByHashSlot
		expectation.groupByWorker[workerID] = snapshot.MetaCreate.GroupByHashSlot
		for hashSlot, count := range snapshot.MetaCreate.PersonByHashSlot {
			var ok bool
			if expectation.person[hashSlot], ok = checkedUint64Add(expectation.person[hashSlot], count); !ok {
				return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
			}
			if expectation.groups[hashSlot], ok = checkedUint64Add(
				expectation.groups[hashSlot], snapshot.MetaCreate.GroupByHashSlot[hashSlot],
			); !ok {
				return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
			}
		}
		workerGroups := uint64(0)
		for _, count := range snapshot.MetaCreate.GroupByHashSlot {
			var ok bool
			if workerGroups, ok = checkedUint64Add(workerGroups, count); !ok {
				return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
			}
		}
		var ok bool
		if groupTotal, ok = checkedUint64Add(groupTotal, workerGroups); !ok || groupTotal > groupLimit {
			return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
		}
	}
	for _, present := range seen {
		if !present {
			return productionMetaExpectationSnapshot{}, ErrLifecycleHarnessInvalid
		}
	}
	return expectation, nil
}

func productionMetaGroupLimit(cfg Config) uint64 {
	return uint64(cfg.Workload.Groups.Small + cfg.Workload.Groups.Medium + cfg.Workload.Groups.Large + cfg.Workload.Groups.VeryLarge)
}

func collectProductionMetaMetrics(
	ctx context.Context,
	sources [coordinatorWorkerCount]ProductionMetaMetricsSource,
) ([coordinatorWorkerCount]target.MetricsSnapshot, error) {
	var snapshots [coordinatorWorkerCount]target.MetricsSnapshot
	if err := ctx.Err(); err != nil {
		return snapshots, err
	}
	type result struct {
		index    int
		snapshot target.MetricsSnapshot
		err      error
	}
	results := make(chan result, coordinatorWorkerCount)
	for index, source := range sources {
		go func() {
			snapshot, err := source.Metrics(ctx)
			results <- result{index: index, snapshot: snapshot, err: err}
		}()
	}
	failed := false
	for range coordinatorWorkerCount {
		result := <-results
		if result.err != nil {
			failed = true
		} else {
			snapshots[result.index] = result.snapshot
		}
	}
	if err := ctx.Err(); err != nil {
		return [coordinatorWorkerCount]target.MetricsSnapshot{}, err
	}
	if failed {
		return [coordinatorWorkerCount]target.MetricsSnapshot{}, ErrLifecycleHarnessInvalid
	}
	return snapshots, nil
}
