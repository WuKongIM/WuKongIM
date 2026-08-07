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

// ProductionMetaController reconciles deterministic worker/catalog create
// expectations with one same-round scrape from each service node.
type ProductionMetaController struct {
	mu sync.Mutex

	runID       string
	workerCount uint64
	metrics     [coordinatorWorkerCount]ProductionMetaMetricsSource
	accounting  *MetaCreateAccounting
	groups      MetaCreateHashSlotCounts
	settleWait  func(context.Context) error

	initialized  bool
	assignmentID string
	generation   uint64
	sequences    [coordinatorWorkerCount]uint64
	person       [coordinatorWorkerCount]MetaCreateHashSlotCounts
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
	groups, err := productionMetaGroupExpectation(options.Config)
	if err != nil {
		return nil, err
	}
	return &ProductionMetaController{
		runID: options.Config.RunID, workerCount: uint64(options.Config.Workload.Workers),
		metrics: options.Metrics, accounting: options.Accounting, groups: groups,
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

	person, ordered, assignmentID, generation, sequences, err := productionMetaPersonExpectation(c.runID, c.workerCount, workers)
	if err != nil {
		return err
	}
	if c.initialized {
		if assignmentID != c.assignmentID || generation != c.generation {
			return ErrLifecycleHarnessInvalid
		}
		for workerID := range ordered {
			if sequences[workerID] <= c.sequences[workerID] {
				return ErrLifecycleHarnessInvalid
			}
			for hashSlot := range ordered[workerID] {
				if ordered[workerID][hashSlot] < c.person[workerID][hashSlot] {
					return ErrLifecycleHarnessInvalid
				}
			}
		}
	}
	metrics, err := c.collectSettledMetrics(ctx, person, assignment)
	if err != nil {
		return err
	}
	if err := c.accounting.Checkpoint(person, c.groups, assignment, metrics, reheat); err != nil {
		return err
	}
	c.initialized = true
	c.assignmentID = assignmentID
	c.generation = generation
	c.sequences = sequences
	c.person = ordered
	return nil
}

// collectSettledMetrics gives authoritative counters one short bounded window
// to become visible after a final worker SENDACK. It never commits a preview,
// and a stable deficit is still handed to accounting as product evidence.
func (c *ProductionMetaController) collectSettledMetrics(
	ctx context.Context,
	person MetaCreateHashSlotCounts,
	assignment LifecycleSlotAssignment,
) ([coordinatorWorkerCount]target.MetricsSnapshot, error) {
	for attempt := 0; attempt < productionMetaSettleAttempts; attempt++ {
		metrics, err := collectProductionMetaMetrics(ctx, c.metrics)
		if err != nil {
			return [coordinatorWorkerCount]target.MetricsSnapshot{}, err
		}
		preview := NewMetaCreateAccounting()
		previewErr := preview.Checkpoint(person, c.groups, assignment, metrics, false)
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
	person, _, _, _, _, err := productionMetaPersonExpectation(cfg.RunID, uint64(cfg.Workload.Workers), workers)
	if err != nil {
		return MetaCreateHashSlotCounts{}, MetaCreateHashSlotCounts{}, err
	}
	groups, err := productionMetaGroupExpectation(cfg)
	if err != nil {
		return MetaCreateHashSlotCounts{}, MetaCreateHashSlotCounts{}, err
	}
	return person, groups, nil
}

func productionMetaPersonExpectation(
	runID string,
	workerCount uint64,
	workers []WorkerSnapshot,
) (
	MetaCreateHashSlotCounts,
	[coordinatorWorkerCount]MetaCreateHashSlotCounts,
	string,
	uint64,
	[coordinatorWorkerCount]uint64,
	error,
) {
	var total MetaCreateHashSlotCounts
	var ordered [coordinatorWorkerCount]MetaCreateHashSlotCounts
	var sequences [coordinatorWorkerCount]uint64
	if runID == "" || workerCount != coordinatorWorkerCount || len(workers) != coordinatorWorkerCount {
		return total, ordered, "", 0, sequences, ErrLifecycleHarnessInvalid
	}
	var seen [coordinatorWorkerCount]bool
	assignmentID := ""
	var generation uint64
	for _, snapshot := range workers {
		if snapshot.RunID != runID || snapshot.AssignmentID == "" || snapshot.Generation == 0 ||
			snapshot.WorkerCount != workerCount || snapshot.WorkerID >= workerCount || snapshot.SnapshotSequence == 0 ||
			(snapshot.Phase != WorkerPhaseRunning && snapshot.Phase != WorkerPhaseFinal) || seen[snapshot.WorkerID] {
			return MetaCreateHashSlotCounts{}, [coordinatorWorkerCount]MetaCreateHashSlotCounts{}, "", 0, sequences, ErrLifecycleHarnessInvalid
		}
		if assignmentID == "" {
			assignmentID, generation = snapshot.AssignmentID, snapshot.Generation
		} else if snapshot.AssignmentID != assignmentID || snapshot.Generation != generation {
			return MetaCreateHashSlotCounts{}, [coordinatorWorkerCount]MetaCreateHashSlotCounts{}, "", 0, sequences, ErrLifecycleHarnessInvalid
		}
		workerID := int(snapshot.WorkerID)
		seen[workerID] = true
		sequences[workerID] = snapshot.SnapshotSequence
		ordered[workerID] = snapshot.MetaCreate.PersonByHashSlot
		for hashSlot, count := range snapshot.MetaCreate.PersonByHashSlot {
			var ok bool
			if total[hashSlot], ok = checkedUint64Add(total[hashSlot], count); !ok {
				return MetaCreateHashSlotCounts{}, [coordinatorWorkerCount]MetaCreateHashSlotCounts{}, "", 0, sequences, ErrLifecycleHarnessInvalid
			}
		}
	}
	for _, present := range seen {
		if !present {
			return MetaCreateHashSlotCounts{}, [coordinatorWorkerCount]MetaCreateHashSlotCounts{}, "", 0, sequences, ErrLifecycleHarnessInvalid
		}
	}
	return total, ordered, assignmentID, generation, sequences, nil
}

func productionMetaGroupExpectation(cfg Config) (MetaCreateHashSlotCounts, error) {
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		return MetaCreateHashSlotCounts{}, ErrLifecycleHarnessInvalid
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		return MetaCreateHashSlotCounts{}, ErrLifecycleHarnessInvalid
	}
	var groups MetaCreateHashSlotCounts
	for index := 0; index < catalog.Count(); index++ {
		group, err := catalog.Group(uint64(index))
		if err != nil {
			return MetaCreateHashSlotCounts{}, ErrLifecycleHarnessInvalid
		}
		hashSlot := lifecycleHashSlotForKey(group.ID, formalHashSlots)
		if groups[hashSlot] == ^uint64(0) {
			return MetaCreateHashSlotCounts{}, ErrLifecycleHarnessInvalid
		}
		groups[hashSlot]++
	}
	return groups, nil
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
