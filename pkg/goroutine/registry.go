package goroutine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	waitPollInterval              = 5 * time.Millisecond
	poolPressureGrace             = 10 * time.Second
	poolPressureMaxObservationGap = 2 * poolPressureGrace
	maxWaitEvidence               = 64
)

// Registry supervises and observes first-party goroutines without owning
// module-specific cancellation or shutdown ordering.
type Registry struct {
	catalog map[TaskID]TaskSpec
	tasks   map[TaskID]*taskState
	modules map[Module]*moduleState

	poolsMu       sync.RWMutex
	pools         map[uint64]*poolRegistration
	retiredReject map[TaskID]int64
	nextPoolID    atomic.Uint64
	panicObserver func(PanicEvent)

	lifecycleFencesMu sync.Mutex
	lifecycleFences   int
	hasLifecycleFence atomic.Bool

	processStartedAt time.Time
	bootID           string
	processTotal     func() int
	ready            atomic.Bool

	registerers []prometheus.Registerer
	descriptors metricDescriptors
}

type taskState struct {
	active      atomic.Int64
	started     atomic.Int64
	stopped     atomic.Int64
	panics      atomic.Int64
	peak        atomic.Int64
	activeSince atomic.Int64

	fencesMu     sync.Mutex
	fenceVersion atomic.Uint64
	fences       atomic.Pointer[taskFenceSet]
}

type taskFence struct {
	started     int64
	active      atomic.Int64
	activeSince atomic.Int64
}

type taskFenceSet struct {
	items []*taskFence
}

type managedRun struct {
	id     int64
	fences *taskFenceSet
}

type moduleState struct {
	peak atomic.Int64
}

type poolRegistration struct {
	task         TaskID
	registeredAt time.Time
	snapshot     func() PoolStats

	pressureMu       sync.Mutex
	pressureSince    time.Time
	pressureObserved time.Time
}

type aggregatedPoolSnapshot struct {
	PoolStats
	health       string
	healthReason string
}

type metricDescriptors struct {
	active       *prometheus.Desc
	started      *prometheus.Desc
	panics       *prometheus.Desc
	poolBusy     *prometheus.Desc
	poolCapacity *prometheus.Desc
	poolQueueCap *prometheus.Desc
	poolQueue    *prometheus.Desc
	poolRejected *prometheus.Desc
}

// PoolStats is the current low-cardinality state of one owned worker pool.
type PoolStats struct {
	// Goroutines is the number of live pool worker and known maintenance goroutines.
	Goroutines int64
	// BusyTasks is the number of logical tasks currently executing.
	BusyTasks int64
	// Capacity is the configured maximum concurrent task count.
	Capacity int64
	// QueueDepth is accepted work that has not started execution.
	QueueDepth int64
	// QueueCapacity is the maximum accepted work awaiting execution when bounded.
	QueueCapacity int64
	// RejectedTotal is the cumulative number of saturated admissions.
	RejectedTotal int64
}

// PanicEvent is bounded panic evidence for one managed task.
type PanicEvent struct {
	// Module owns the task that panicked.
	Module Module
	// Task is the stable task identity that panicked.
	Task TaskID
	// Recovered is the original bounded panic value.
	Recovered any
}

// TaskSnapshot is the current state of one fixed catalog task.
type TaskSnapshot struct {
	// Task is the globally unique module/task identity.
	Task TaskID `json:"task"`
	// Name is the stable task name within its module.
	Name string `json:"name"`
	// Kind determines task lifecycle and capacity semantics.
	Kind TaskKind `json:"kind"`
	// Critical reports whether a panic is fatal to the process.
	Critical bool `json:"critical"`
	// Expected is the declared normal live count when known.
	Expected int `json:"expected,omitempty"`
	// Active is the current live goroutine count.
	Active int64 `json:"active"`
	// Peak is the highest live count observed during this process boot.
	Peak int64 `json:"process_peak"`
	// TotalStarted is the cumulative process-boot start count.
	TotalStarted int64 `json:"total_started"`
	// TotalStopped is the cumulative process-boot stop count.
	TotalStopped int64 `json:"total_stopped"`
	// PanicCount is the cumulative process-boot panic count.
	PanicCount int64 `json:"panics"`
	// BusyTasks is the current executing task count for owned pools.
	BusyTasks int64 `json:"busy_tasks,omitempty"`
	// PoolCapacity is the configured concurrent task capacity.
	PoolCapacity int64 `json:"pool_capacity,omitempty"`
	// QueueDepth is accepted work that has not started execution.
	QueueDepth int64 `json:"queue_depth,omitempty"`
	// QueueCapacity is the configured bounded queue capacity when known.
	QueueCapacity int64 `json:"queue_capacity,omitempty"`
	// RejectedTotal is the cumulative saturated admission count.
	RejectedTotal int64 `json:"rejected_total,omitempty"`
	// RunningFor is the observed age of the continuously active direct-task cohort.
	RunningFor time.Duration `json:"running_for,omitempty"`
	// Health is the derived normal, warning, or critical state.
	Health string `json:"health"`
	// HealthReason is the bounded machine-readable health cause.
	HealthReason string `json:"health_reason,omitempty"`
}

// ModuleSnapshot aggregates managed goroutine state for one module.
type ModuleSnapshot struct {
	// Module is the stable low-cardinality owner.
	Module Module `json:"module"`
	// Active is the current live goroutine count.
	Active int64 `json:"active"`
	// Peak is the highest aggregate live count observed during this process boot.
	Peak int64 `json:"process_peak"`
	// TotalStarted is the cumulative process-boot start count.
	TotalStarted int64 `json:"total_started"`
	// TotalStopped is the cumulative process-boot stop count.
	TotalStopped int64 `json:"total_stopped"`
	// PanicCount is the cumulative process-boot panic count.
	PanicCount int64 `json:"panics"`
	// BusyTasks is the aggregate executing task count for owned pools.
	BusyTasks int64 `json:"busy_tasks,omitempty"`
	// PoolCapacity is the aggregate concurrent task capacity.
	PoolCapacity int64 `json:"pool_capacity,omitempty"`
	// QueueDepth is aggregate accepted work awaiting execution.
	QueueDepth int64 `json:"queue_depth,omitempty"`
	// QueueCapacity is the aggregate bounded queue capacity when known.
	QueueCapacity int64 `json:"queue_capacity,omitempty"`
	// RejectedTotal is the aggregate saturated admission count.
	RejectedTotal int64 `json:"rejected_total,omitempty"`
	// Tasks contains the module's fixed catalog task snapshots.
	Tasks []TaskSnapshot `json:"tasks"`
	// Health is the worst derived task health in the module.
	Health string `json:"health"`
}

const (
	HealthNormal   = "normal"
	HealthWarning  = "warning"
	HealthCritical = "critical"
)

// Snapshot is a process-scoped managed goroutine snapshot.
type Snapshot struct {
	// GeneratedAt is the UTC time when the snapshot was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// ProcessStartedAt is the process-local registry construction time.
	ProcessStartedAt time.Time `json:"process_started_at"`
	// BootID distinguishes counters and peaks across process restarts.
	BootID string `json:"boot_id"`
	// ProcessTotal is runtime.NumGoroutine at snapshot time.
	ProcessTotal int64 `json:"process_total"`
	// ManagedTotal is the current first-party managed goroutine count.
	ManagedTotal int64 `json:"managed_total"`
	// UnmanagedTotal is the non-negative process-minus-managed remainder.
	UnmanagedTotal int64 `json:"unmanaged_total"`
	// Reconciled reports whether process and managed totals were consistent.
	Reconciled bool `json:"reconciled"`
	// TotalActive aliases ManagedTotal for metric consumers.
	TotalActive int64 `json:"total_active"`
	// TotalStarted is the cumulative managed start count for this process boot.
	TotalStarted int64 `json:"total_started"`
	// TotalPanics is the cumulative managed panic count for this process boot.
	TotalPanics int64 `json:"total_panics"`
	// Modules contains every fixed catalog module and task.
	Modules []ModuleSnapshot `json:"modules"`
}

// WaitTaskEvidence identifies one task still live when a bounded wait expires.
type WaitTaskEvidence struct {
	// Task is the fixed catalog identity that remained live.
	Task TaskID
	// Active is the live direct and pool goroutine count above the wait fence.
	Active int64
	// RunningFor is the observed age of the continuously active post-fence cohort.
	RunningFor time.Duration
}

// WaitError reports bounded task evidence after a module wait deadline.
type WaitError struct {
	// Module is the module whose managed activity did not drain.
	Module Module
	// Tasks contains at most maxWaitEvidence live task groups.
	Tasks []WaitTaskEvidence
	// Cause is the context cancellation or deadline that ended the wait.
	Cause error
}

// Baseline is an unlabeled lifecycle fence captured before one app instance
// constructs or starts its owned runtimes.
type Baseline struct {
	active map[TaskID]int64
	fences map[TaskID]*taskFence
	poolID uint64
}

// Active returns the task activity recorded at the lifecycle fence.
func (b Baseline) Active(task TaskID) int64 {
	return b.active[task]
}

// Error implements error.
func (e *WaitError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("goroutine: module %s did not stop: %d live task groups: %v", e.Module, len(e.Tasks), e.Cause)
}

// Unwrap returns the wait deadline or cancellation cause.
func (e *WaitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Group is a module-scoped lifecycle wait facade.
type Group struct {
	registry *Registry
	module   Module
}

// Option configures a Registry.
type Option func(*Registry)

// WithPanicObserver receives bounded panic evidence before recovery or repanic.
func WithPanicObserver(observer func(PanicEvent)) Option {
	return func(registry *Registry) {
		registry.panicObserver = observer
	}
}

// WithPrometheusRegisterer registers scrape-time goroutine metrics.
func WithPrometheusRegisterer(registerer prometheus.Registerer) Option {
	return func(registry *Registry) {
		if registerer != nil {
			registry.registerers = append(registry.registerers, registerer)
		}
	}
}

// New creates an always-on goroutine registry.
func New(opts ...Option) *Registry {
	catalog, err := buildCatalog(defaultTaskCatalog)
	if err != nil {
		panic(err)
	}
	startedAt := time.Now()
	registry := &Registry{
		catalog:          catalog,
		tasks:            make(map[TaskID]*taskState, len(catalog)),
		modules:          make(map[Module]*moduleState),
		pools:            make(map[uint64]*poolRegistration),
		retiredReject:    make(map[TaskID]int64),
		processStartedAt: startedAt,
		bootID:           strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(startedAt.UnixNano(), 36),
		processTotal:     runtime.NumGoroutine,
		descriptors:      newMetricDescriptors(),
	}
	for id, spec := range catalog {
		registry.tasks[id] = &taskState{}
		if registry.modules[spec.Module] == nil {
			registry.modules[spec.Module] = &moduleState{}
		}
	}
	for _, opt := range opts {
		opt(registry)
	}
	for _, registerer := range registry.registerers {
		registerer.MustRegister(registry)
	}
	return registry
}

func newMetricDescriptors() metricDescriptors {
	labels := []string{"module", "task", "kind"}
	return metricDescriptors{
		active: prometheus.NewDesc(
			"wukongim_goroutines_active",
			"Current live first-party goroutines by fixed module and task.",
			labels,
			nil,
		),
		started: prometheus.NewDesc(
			"wukongim_goroutines_started_total",
			"Total first-party goroutines started by fixed module and task.",
			labels,
			nil,
		),
		panics: prometheus.NewDesc(
			"wukongim_goroutines_panics_total",
			"Total recovered first-party goroutine panics by fixed module and task.",
			labels,
			nil,
		),
		poolBusy: prometheus.NewDesc(
			"wukongim_goroutine_pool_busy_tasks",
			"Current logical busy tasks in owned goroutine pools.",
			labels,
			nil,
		),
		poolCapacity: prometheus.NewDesc(
			"wukongim_goroutine_pool_capacity",
			"Configured concurrent task capacity of owned goroutine pools.",
			labels,
			nil,
		),
		poolQueueCap: prometheus.NewDesc(
			"wukongim_goroutine_pool_queue_capacity",
			"Configured accepted queue capacity of owned goroutine pools when bounded.",
			labels,
			nil,
		),
		poolQueue: prometheus.NewDesc(
			"wukongim_goroutine_pool_queue_depth",
			"Current accepted queue depth of owned goroutine pools.",
			labels,
			nil,
		),
		poolRejected: prometheus.NewDesc(
			"wukongim_goroutine_pool_rejected_total",
			"Total saturated admissions rejected by owned goroutine pools.",
			labels,
			nil,
		),
	}
}

// Group returns a module-scoped lifecycle facade.
func (r *Registry) Group(module Module) *Group {
	return &Group{registry: r, module: module}
}

// Baseline captures current task activity without adding owner labels.
func (r *Registry) Baseline() Baseline {
	if r == nil {
		return fallbackRegistry.Baseline()
	}
	r.lifecycleFencesMu.Lock()
	r.hasLifecycleFence.Store(true)
	defer func() {
		if r.lifecycleFences == 0 {
			r.hasLifecycleFence.Store(false)
		}
		r.lifecycleFencesMu.Unlock()
	}()
	active := make(map[TaskID]int64, len(r.tasks))
	fences := make(map[TaskID]*taskFence)
	for task, state := range r.tasks {
		state.fencesMu.Lock()
		state.fenceVersion.Add(1)
		taskActive := state.active.Load()
		active[task] = taskActive
		if taskActive > 0 {
			fence := &taskFence{started: state.started.Load()}
			current := state.fences.Load()
			items := make([]*taskFence, 0, 1)
			if current != nil {
				items = make([]*taskFence, 0, len(current.items)+1)
				items = append(items, current.items...)
			}
			items = append(items, fence)
			state.fences.Store(&taskFenceSet{items: items})
			fences[task] = fence
			r.lifecycleFences++
		}
		state.fenceVersion.Add(1)
		state.fencesMu.Unlock()
	}
	r.poolsMu.RLock()
	poolID := r.nextPoolID.Load()
	for _, registration := range r.pools {
		active[registration.task] += clampNonNegative(registration.snapshot().Goroutines)
	}
	r.poolsMu.RUnlock()
	return Baseline{active: active, fences: fences, poolID: poolID}
}

// SetReady enables or disables post-readiness fixed-task health checks.
func (r *Registry) SetReady(ready bool) {
	if r == nil {
		fallbackRegistry.SetReady(ready)
		return
	}
	r.ready.Store(ready)
}

// Go starts one managed goroutine from the fixed task catalog.
func (r *Registry) Go(task TaskID, fn func()) {
	if r == nil {
		fallbackRegistry.Go(task, fn)
		return
	}
	spec, state := r.mustTask(task)
	run := r.start(state)
	go func() {
		labels := pprof.Labels("module", string(spec.Module), "task", spec.Name)
		pprof.Do(context.Background(), labels, func(context.Context) {
			defer r.done(state, run)
			defer r.handlePanic(spec, state)
			fn()
		})
	}()
}

// GoN starts n identical managed goroutines.
func (r *Registry) GoN(task TaskID, n int, fn func(workerID int)) {
	for workerID := 0; workerID < n; workerID++ {
		id := workerID
		r.Go(task, func() { fn(id) })
	}
}

// RegisterPool adds one owned pool to scrape-time and snapshot accounting.
func (r *Registry) RegisterPool(task TaskID, snapshot func() PoolStats) (func(), error) {
	if r == nil {
		return fallbackRegistry.RegisterPool(task, snapshot)
	}
	spec, _ := r.mustTask(task)
	if spec.Kind != TaskKindPool {
		return nil, fmt.Errorf("goroutine: task %q is %s, want pool", task, spec.Kind)
	}
	if snapshot == nil {
		return nil, fmt.Errorf("goroutine: pool %q snapshot is nil", task)
	}
	r.poolsMu.Lock()
	id := r.nextPoolID.Add(1)
	r.pools[id] = &poolRegistration{task: task, registeredAt: time.Now(), snapshot: snapshot}
	r.poolsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			stats := snapshot()
			r.poolsMu.Lock()
			delete(r.pools, id)
			r.retiredReject[task] += clampNonNegative(stats.RejectedTotal)
			r.poolsMu.Unlock()
		})
	}, nil
}

// Snapshot returns a non-blocking process and ownership snapshot.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return fallbackRegistry.Snapshot()
	}
	now := time.Now()
	poolStats := r.snapshotPools(now)
	moduleMap := make(map[Module]*ModuleSnapshot)
	var managedTotal, totalStarted, totalPanics int64

	taskIDs := make([]TaskID, 0, len(r.catalog))
	for id := range r.catalog {
		taskIDs = append(taskIDs, id)
	}
	sort.Slice(taskIDs, func(i, j int) bool { return taskIDs[i] < taskIDs[j] })
	for _, id := range taskIDs {
		spec := r.catalog[id]
		state := r.tasks[id]
		pool := poolStats[id]
		active := state.active.Load() + pool.Goroutines
		updatePeak(&state.peak, active)
		task := TaskSnapshot{
			Task:          id,
			Name:          spec.Name,
			Kind:          spec.Kind,
			Critical:      spec.PanicPolicy == PanicPolicyRepanic,
			Expected:      spec.Expected,
			Active:        active,
			Peak:          state.peak.Load(),
			TotalStarted:  state.started.Load(),
			TotalStopped:  state.stopped.Load(),
			PanicCount:    state.panics.Load(),
			BusyTasks:     pool.BusyTasks,
			PoolCapacity:  pool.Capacity,
			QueueDepth:    pool.QueueDepth,
			QueueCapacity: pool.QueueCapacity,
			RejectedTotal: pool.RejectedTotal,
		}
		task.Health, task.HealthReason = r.taskHealth(spec, task, pool)
		if since := state.activeSince.Load(); since > 0 && state.active.Load() > 0 {
			task.RunningFor = now.Sub(time.Unix(0, since))
		}
		module := moduleMap[spec.Module]
		if module == nil {
			module = &ModuleSnapshot{Module: spec.Module, Health: HealthNormal}
			moduleMap[spec.Module] = module
		}
		if healthSeverity(task.Health) > healthSeverity(module.Health) {
			module.Health = task.Health
		}
		module.Active += task.Active
		module.TotalStarted += task.TotalStarted
		module.TotalStopped += task.TotalStopped
		module.PanicCount += task.PanicCount
		module.BusyTasks += task.BusyTasks
		module.PoolCapacity += task.PoolCapacity
		module.QueueDepth += task.QueueDepth
		module.QueueCapacity += task.QueueCapacity
		module.RejectedTotal += task.RejectedTotal
		module.Tasks = append(module.Tasks, task)
		managedTotal += task.Active
		totalStarted += task.TotalStarted
		totalPanics += task.PanicCount
	}

	modules := make([]ModuleSnapshot, 0, len(moduleMap))
	for _, module := range moduleMap {
		state := r.modules[module.Module]
		updatePeak(&state.peak, module.Active)
		module.Peak = state.peak.Load()
		modules = append(modules, *module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Module < modules[j].Module })

	processTotal := int64(r.processTotal())
	unmanaged := processTotal - managedTotal
	reconciled := unmanaged >= 0
	if unmanaged < 0 {
		unmanaged = 0
	}
	return Snapshot{
		GeneratedAt:      now.UTC(),
		ProcessStartedAt: r.processStartedAt.UTC(),
		BootID:           r.bootID,
		ProcessTotal:     processTotal,
		ManagedTotal:     managedTotal,
		UnmanagedTotal:   unmanaged,
		Reconciled:       reconciled,
		TotalActive:      managedTotal,
		TotalStarted:     totalStarted,
		TotalPanics:      totalPanics,
		Modules:          modules,
	}
}

func (r *Registry) taskHealth(spec TaskSpec, task TaskSnapshot, pool aggregatedPoolSnapshot) (string, string) {
	if task.PanicCount > 0 {
		if task.Critical {
			return HealthCritical, "panic"
		}
		return HealthWarning, "panic"
	}
	if spec.Expected > 0 {
		if task.Active > int64(spec.Expected) {
			return HealthCritical, "over_declared"
		}
		// Optional runtimes are not expected until they have started once.
		if r.ready.Load() && (spec.Required || task.TotalStarted > 0) && task.Active < int64(spec.Expected) {
			if task.Critical {
				return HealthCritical, "missing"
			}
			return HealthWarning, "missing"
		}
	}
	if spec.Kind == TaskKindPool && pool.health != "" {
		return pool.health, pool.healthReason
	}
	return HealthNormal, ""
}

func healthSeverity(health string) int {
	switch health {
	case HealthCritical:
		return 2
	case HealthWarning:
		return 1
	default:
		return 0
	}
}

func (r *Registry) snapshotPools(now time.Time) map[TaskID]aggregatedPoolSnapshot {
	r.poolsMu.RLock()
	out := make(map[TaskID]aggregatedPoolSnapshot)
	for _, registration := range r.pools {
		stats := registration.snapshot()
		current := out[registration.task]
		current.Goroutines += clampNonNegative(stats.Goroutines)
		current.BusyTasks += clampNonNegative(stats.BusyTasks)
		current.Capacity += clampNonNegative(stats.Capacity)
		current.QueueDepth += clampNonNegative(stats.QueueDepth)
		current.QueueCapacity += clampNonNegative(stats.QueueCapacity)
		current.RejectedTotal += clampNonNegative(stats.RejectedTotal)
		health, reason := registration.health(stats, now)
		if healthSeverity(health) > healthSeverity(current.health) {
			current.health = health
			current.healthReason = reason
		}
		out[registration.task] = current
	}
	for task, retired := range r.retiredReject {
		current := out[task]
		current.RejectedTotal += retired
		if retired > 0 {
			current.health = HealthCritical
			current.healthReason = "saturated"
		}
		out[task] = current
	}
	r.poolsMu.RUnlock()
	return out
}

func (p *poolRegistration) health(stats PoolStats, now time.Time) (string, string) {
	p.pressureMu.Lock()
	defer p.pressureMu.Unlock()

	rejected := clampNonNegative(stats.RejectedTotal)
	queueDepth := clampNonNegative(stats.QueueDepth)
	queueCapacity := clampNonNegative(stats.QueueCapacity)
	if rejected > 0 || (queueCapacity > 0 && queueDepth >= queueCapacity) {
		p.pressureSince = time.Time{}
		p.pressureObserved = time.Time{}
		return HealthCritical, "saturated"
	}

	capacity := clampNonNegative(stats.Capacity)
	busy := clampNonNegative(stats.BusyTasks)
	pressured := capacity > 0 && queueDepth > 0 && busy*100 >= capacity*80
	if !pressured {
		p.pressureSince = time.Time{}
		p.pressureObserved = time.Time{}
		return HealthNormal, ""
	}

	if !p.pressureObserved.IsZero() && now.Before(p.pressureObserved) {
		now = p.pressureObserved
	}
	if p.pressureObserved.IsZero() || now.Sub(p.pressureObserved) > poolPressureMaxObservationGap {
		p.pressureSince = now
	}
	p.pressureObserved = now
	if now.Sub(p.pressureSince) >= poolPressureGrace {
		return HealthWarning, "pressure"
	}
	return HealthNormal, ""
}

func clampNonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func (r *Registry) mustTask(task TaskID) (TaskSpec, *taskState) {
	spec, ok := r.catalog[task]
	if !ok {
		panic(fmt.Sprintf("goroutine: task %q is not in the fixed catalog", task))
	}
	return spec, r.tasks[task]
}

func (r *Registry) start(state *taskState) managedRun {
	runID := state.started.Add(1)
	active := state.active.Add(1)
	var startedAt int64
	if active == 1 {
		startedAt = time.Now().UnixNano()
		state.activeSince.Store(startedAt)
	}
	var fences *taskFenceSet
	if r.hasLifecycleFence.Load() {
		fences = stableTaskFences(state)
	}
	if fences != nil {
		for _, fence := range fences.items {
			if runID <= fence.started {
				continue
			}
			if startedAt == 0 {
				startedAt = time.Now().UnixNano()
			}
			if fence.active.Add(1) == 1 {
				fence.activeSince.Store(startedAt)
			}
		}
	}
	updatePeak(&state.peak, active)
	return managedRun{id: runID, fences: fences}
}

func stableTaskFences(state *taskState) *taskFenceSet {
	for {
		before := state.fenceVersion.Load()
		if before%2 != 0 {
			runtime.Gosched()
			continue
		}
		fences := state.fences.Load()
		if state.fenceVersion.Load() == before {
			return fences
		}
	}
}

func (r *Registry) done(state *taskState, run managedRun) {
	state.stopped.Add(1)
	active := state.active.Add(-1)
	if active == 0 {
		state.activeSince.Store(0)
	}
	if active < 0 {
		panic("goroutine: active count underflow")
	}
	if run.fences == nil {
		return
	}
	for _, fence := range run.fences.items {
		if run.id <= fence.started {
			continue
		}
		fenceActive := fence.active.Add(-1)
		if fenceActive == 0 {
			fence.activeSince.Store(0)
		}
		if fenceActive < 0 {
			panic("goroutine: fence active count underflow")
		}
	}
}

func (r *Registry) handlePanic(spec TaskSpec, state *taskState) {
	recovered := recover()
	if recovered == nil {
		return
	}
	state.panics.Add(1)
	if r.panicObserver != nil {
		r.panicObserver(PanicEvent{
			Module:    spec.Module,
			Task:      spec.ID,
			Recovered: recovered,
		})
	}
	if spec.PanicPolicy == PanicPolicyRepanic {
		panic(recovered)
	}
}

// ReportPoolPanic applies the catalog panic policy to an ants worker panic.
func (r *Registry) ReportPoolPanic(task TaskID, recovered any) {
	if r == nil {
		fallbackRegistry.ReportPoolPanic(task, recovered)
		return
	}
	if recovered == nil {
		return
	}
	spec, state := r.mustTask(task)
	state.panics.Add(1)
	if r.panicObserver != nil {
		r.panicObserver(PanicEvent{Module: spec.Module, Task: spec.ID, Recovered: recovered})
	}
	if spec.PanicPolicy == PanicPolicyRepanic {
		panic(recovered)
	}
}

func updatePeak(peak *atomic.Int64, current int64) {
	for {
		old := peak.Load()
		if current <= old || peak.CompareAndSwap(old, current) {
			return
		}
	}
}

// Wait blocks until all tracked goroutines and registered pool workers owned by
// the group have exited or ctx expires.
func (g *Group) Wait(ctx context.Context) error {
	return g.waitFrom(ctx, Baseline{})
}

// WaitFrom waits until module activity has returned to the captured baseline.
// It lets one app instance share a process registry without waiting for tasks
// that were already alive before that app was constructed.
func (g *Group) WaitFrom(ctx context.Context, baseline Baseline) error {
	return g.waitFrom(ctx, baseline)
}

func (g *Group) waitFrom(ctx context.Context, baseline Baseline) error {
	if g == nil || g.registry == nil {
		return nil
	}
	defer g.registry.releaseBaseline(g.module, baseline)
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()
	for {
		if g.registry.moduleActiveAbove(g.module, baseline) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return g.registry.waitErrorAbove(g.module, baseline, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *Registry) moduleActiveAbove(module Module, baseline Baseline) int64 {
	var active int64
	for task, spec := range r.catalog {
		if spec.Module != module {
			continue
		}
		count, _ := r.directActivityAbove(task, baseline)
		active += count
	}
	poolCount, _ := r.poolActivityAbove(module, baseline)
	return active + poolCount
}

func (r *Registry) waitErrorAbove(module Module, baseline Baseline, cause error) error {
	evidence := make([]WaitTaskEvidence, 0)
	taskIDs := make([]TaskID, 0, len(r.catalog))
	for task, spec := range r.catalog {
		if spec.Module == module {
			taskIDs = append(taskIDs, task)
		}
	}
	sort.Slice(taskIDs, func(i, j int) bool { return taskIDs[i] < taskIDs[j] })
	for _, task := range taskIDs {
		active, oldest := r.directActivityAbove(task, baseline)
		poolActive, poolOldest := r.poolTaskActivityAbove(task, baseline)
		active += poolActive
		if oldest.IsZero() || (!poolOldest.IsZero() && poolOldest.Before(oldest)) {
			oldest = poolOldest
		}
		if active <= 0 {
			continue
		}
		var runningFor time.Duration
		if !oldest.IsZero() {
			runningFor = time.Since(oldest)
		}
		evidence = append(evidence, WaitTaskEvidence{
			Task:       task,
			Active:     active,
			RunningFor: runningFor,
		})
		if len(evidence) == maxWaitEvidence {
			break
		}
	}
	return &WaitError{Module: module, Tasks: evidence, Cause: cause}
}

func (r *Registry) directActivityAbove(task TaskID, baseline Baseline) (int64, time.Time) {
	state := r.tasks[task]
	if state == nil {
		return 0, time.Time{}
	}
	fence := baseline.fences[task]
	if fence == nil {
		active := state.active.Load()
		if active <= 0 {
			return 0, time.Time{}
		}
		if startedAt := state.activeSince.Load(); startedAt > 0 {
			return active, time.Unix(0, startedAt)
		}
		return active, time.Time{}
	}
	active := fence.active.Load()
	if active <= 0 {
		return 0, time.Time{}
	}
	if startedAt := fence.activeSince.Load(); startedAt > 0 {
		return active, time.Unix(0, startedAt)
	}
	return active, time.Time{}
}

func (r *Registry) releaseBaseline(module Module, baseline Baseline) {
	r.lifecycleFencesMu.Lock()
	defer func() {
		if r.lifecycleFences == 0 {
			r.hasLifecycleFence.Store(false)
		}
		r.lifecycleFencesMu.Unlock()
	}()
	for task, fence := range baseline.fences {
		if r.catalog[task].Module != module {
			continue
		}
		state := r.tasks[task]
		state.fencesMu.Lock()
		state.fenceVersion.Add(1)
		current := state.fences.Load()
		removed := false
		if current != nil {
			items := make([]*taskFence, 0, len(current.items))
			for _, candidate := range current.items {
				if candidate != fence {
					items = append(items, candidate)
				} else {
					removed = true
				}
			}
			if len(items) == 0 {
				state.fences.Store(nil)
			} else {
				state.fences.Store(&taskFenceSet{items: items})
			}
		}
		if removed {
			r.lifecycleFences--
			if r.lifecycleFences < 0 {
				panic("goroutine: lifecycle fence count underflow")
			}
		}
		state.fenceVersion.Add(1)
		state.fencesMu.Unlock()
	}
}

func (r *Registry) poolActivityAbove(module Module, baseline Baseline) (int64, time.Time) {
	var active int64
	var oldest time.Time
	r.poolsMu.RLock()
	for id, registration := range r.pools {
		if id <= baseline.poolID || r.catalog[registration.task].Module != module {
			continue
		}
		stats := registration.snapshot()
		active += clampNonNegative(stats.Goroutines)
		if stats.Goroutines > 0 && (oldest.IsZero() || registration.registeredAt.Before(oldest)) {
			oldest = registration.registeredAt
		}
	}
	r.poolsMu.RUnlock()
	return active, oldest
}

func (r *Registry) poolTaskActivityAbove(task TaskID, baseline Baseline) (int64, time.Time) {
	var active int64
	var oldest time.Time
	r.poolsMu.RLock()
	for id, registration := range r.pools {
		if id <= baseline.poolID || registration.task != task {
			continue
		}
		stats := registration.snapshot()
		active += clampNonNegative(stats.Goroutines)
		if stats.Goroutines > 0 && (oldest.IsZero() || registration.registeredAt.Before(oldest)) {
			oldest = registration.registeredAt
		}
	}
	r.poolsMu.RUnlock()
	return active, oldest
}

// Describe implements prometheus.Collector.
func (r *Registry) Describe(ch chan<- *prometheus.Desc) {
	ch <- r.descriptors.active
	ch <- r.descriptors.started
	ch <- r.descriptors.panics
	ch <- r.descriptors.poolBusy
	ch <- r.descriptors.poolCapacity
	ch <- r.descriptors.poolQueueCap
	ch <- r.descriptors.poolQueue
	ch <- r.descriptors.poolRejected
}

// Collect implements prometheus.Collector using one scrape-time snapshot.
func (r *Registry) Collect(ch chan<- prometheus.Metric) {
	snapshot := r.Snapshot()
	for _, module := range snapshot.Modules {
		for _, task := range module.Tasks {
			labels := []string{string(module.Module), task.Name, string(task.Kind)}
			ch <- prometheus.MustNewConstMetric(r.descriptors.active, prometheus.GaugeValue, float64(task.Active), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.started, prometheus.CounterValue, float64(task.TotalStarted), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.panics, prometheus.CounterValue, float64(task.PanicCount), labels...)
			if task.Kind != TaskKindPool {
				continue
			}
			ch <- prometheus.MustNewConstMetric(r.descriptors.poolBusy, prometheus.GaugeValue, float64(task.BusyTasks), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.poolCapacity, prometheus.GaugeValue, float64(task.PoolCapacity), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.poolQueueCap, prometheus.GaugeValue, float64(task.QueueCapacity), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.poolQueue, prometheus.GaugeValue, float64(task.QueueDepth), labels...)
			ch <- prometheus.MustNewConstMetric(r.descriptors.poolRejected, prometheus.CounterValue, float64(task.RejectedTotal), labels...)
		}
	}
}

var fallbackRegistry = New()

// Default returns the process-wide always-on goroutine supervisor.
func Default() *Registry {
	return fallbackRegistry
}

// SafeGo starts a managed goroutine on r or the always-on fallback registry.
func SafeGo(r *Registry, task TaskID, fn func()) {
	if r == nil {
		fallbackRegistry.Go(task, fn)
		return
	}
	r.Go(task, fn)
}

// SafeGoN starts managed goroutines on r or the always-on fallback registry.
func SafeGoN(r *Registry, task TaskID, n int, fn func(workerID int)) {
	if r == nil {
		fallbackRegistry.GoN(task, n, fn)
		return
	}
	r.GoN(task, n, fn)
}
