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
	waitPollInterval = 5 * time.Millisecond
	maxWaitEvidence  = 64
)

// Registry supervises and observes first-party goroutines without owning
// module-specific cancellation or shutdown ordering.
type Registry struct {
	catalog map[TaskID]TaskSpec
	tasks   map[TaskID]*taskState
	modules map[Module]*moduleState

	poolsMu       sync.RWMutex
	pools         map[uint64]poolRegistration
	retiredReject map[TaskID]int64
	nextPoolID    atomic.Uint64
	panicObserver func(PanicEvent)

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
}

type moduleState struct {
	active atomic.Int64
	peak   atomic.Int64
}

type poolRegistration struct {
	task     TaskID
	snapshot func() PoolStats
}

type metricDescriptors struct {
	active       *prometheus.Desc
	started      *prometheus.Desc
	panics       *prometheus.Desc
	poolBusy     *prometheus.Desc
	poolCapacity *prometheus.Desc
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
	// RejectedTotal is the cumulative number of saturated admissions.
	RejectedTotal int64
}

// PanicEvent is bounded panic evidence for one managed task.
type PanicEvent struct {
	Module    Module
	Task      TaskID
	Recovered any
}

// TaskSnapshot is the current state of one fixed catalog task.
type TaskSnapshot struct {
	Task          TaskID        `json:"task"`
	Name          string        `json:"name"`
	Kind          TaskKind      `json:"kind"`
	Critical      bool          `json:"critical"`
	Expected      int           `json:"expected,omitempty"`
	Active        int64         `json:"active"`
	Peak          int64         `json:"process_peak"`
	TotalStarted  int64         `json:"total_started"`
	TotalStopped  int64         `json:"total_stopped"`
	PanicCount    int64         `json:"panics"`
	BusyTasks     int64         `json:"busy_tasks,omitempty"`
	PoolCapacity  int64         `json:"pool_capacity,omitempty"`
	QueueDepth    int64         `json:"queue_depth,omitempty"`
	RejectedTotal int64         `json:"rejected_total,omitempty"`
	RunningFor    time.Duration `json:"running_for,omitempty"`
	Health        string        `json:"health"`
	HealthReason  string        `json:"health_reason,omitempty"`
}

// ModuleSnapshot aggregates managed goroutine state for one module.
type ModuleSnapshot struct {
	Module        Module         `json:"module"`
	Active        int64          `json:"active"`
	Peak          int64          `json:"process_peak"`
	TotalStarted  int64          `json:"total_started"`
	TotalStopped  int64          `json:"total_stopped"`
	PanicCount    int64          `json:"panics"`
	BusyTasks     int64          `json:"busy_tasks,omitempty"`
	PoolCapacity  int64          `json:"pool_capacity,omitempty"`
	QueueDepth    int64          `json:"queue_depth,omitempty"`
	RejectedTotal int64          `json:"rejected_total,omitempty"`
	Tasks         []TaskSnapshot `json:"tasks"`
	Health        string         `json:"health"`
}

const (
	HealthNormal   = "normal"
	HealthWarning  = "warning"
	HealthCritical = "critical"
)

// Snapshot is a process-scoped managed goroutine snapshot.
type Snapshot struct {
	GeneratedAt      time.Time        `json:"generated_at"`
	ProcessStartedAt time.Time        `json:"process_started_at"`
	BootID           string           `json:"boot_id"`
	ProcessTotal     int64            `json:"process_total"`
	ManagedTotal     int64            `json:"managed_total"`
	UnmanagedTotal   int64            `json:"unmanaged_total"`
	Reconciled       bool             `json:"reconciled"`
	TotalActive      int64            `json:"total_active"`
	TotalStarted     int64            `json:"total_started"`
	TotalPanics      int64            `json:"total_panics"`
	Modules          []ModuleSnapshot `json:"modules"`
}

// WaitTaskEvidence identifies one task still live when a bounded wait expires.
type WaitTaskEvidence struct {
	Task       TaskID
	Active     int64
	RunningFor time.Duration
}

// WaitError reports bounded task evidence after a module wait deadline.
type WaitError struct {
	Module Module
	Tasks  []WaitTaskEvidence
	Cause  error
}

// Baseline is an unlabeled lifecycle fence captured before one app instance
// constructs or starts its owned runtimes.
type Baseline struct {
	active map[TaskID]int64
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
		pools:            make(map[uint64]poolRegistration),
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
	snapshot := r.Snapshot()
	active := make(map[TaskID]int64, len(r.tasks))
	for _, module := range snapshot.Modules {
		for _, task := range module.Tasks {
			active[task.Task] = task.Active
		}
	}
	return Baseline{active: active}
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
	r.start(spec, state)
	go func() {
		labels := pprof.Labels("module", string(spec.Module), "task", spec.Name)
		pprof.Do(context.Background(), labels, func(context.Context) {
			defer r.done(spec, state)
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
	id := r.nextPoolID.Add(1)
	r.poolsMu.Lock()
	r.pools[id] = poolRegistration{task: task, snapshot: snapshot}
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
	poolStats := r.snapshotPools()
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
			RejectedTotal: pool.RejectedTotal,
		}
		task.Health, task.HealthReason = r.taskHealth(spec, task)
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

func (r *Registry) taskHealth(spec TaskSpec, task TaskSnapshot) (string, string) {
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
		if r.ready.Load() && task.TotalStarted > 0 && task.Active < int64(spec.Expected) {
			if task.Critical {
				return HealthCritical, "missing"
			}
			return HealthWarning, "missing"
		}
	}
	if spec.Kind == TaskKindPool && task.PoolCapacity > 0 {
		if task.RejectedTotal > 0 || (task.BusyTasks >= task.PoolCapacity && task.QueueDepth > 0) {
			return HealthCritical, "saturated"
		}
		if task.QueueDepth > 0 && task.BusyTasks*100 >= task.PoolCapacity*80 {
			return HealthWarning, "pressure"
		}
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

func (r *Registry) snapshotPools() map[TaskID]PoolStats {
	r.poolsMu.RLock()
	out := make(map[TaskID]PoolStats)
	for _, registration := range r.pools {
		stats := registration.snapshot()
		current := out[registration.task]
		current.Goroutines += clampNonNegative(stats.Goroutines)
		current.BusyTasks += clampNonNegative(stats.BusyTasks)
		current.Capacity += clampNonNegative(stats.Capacity)
		current.QueueDepth += clampNonNegative(stats.QueueDepth)
		current.RejectedTotal += clampNonNegative(stats.RejectedTotal)
		out[registration.task] = current
	}
	for task, retired := range r.retiredReject {
		current := out[task]
		current.RejectedTotal += retired
		out[task] = current
	}
	r.poolsMu.RUnlock()
	return out
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

func (r *Registry) start(spec TaskSpec, state *taskState) {
	state.started.Add(1)
	active := state.active.Add(1)
	if active == 1 {
		state.activeSince.Store(time.Now().UnixNano())
	}
	updatePeak(&state.peak, active)
	module := r.modules[spec.Module]
	moduleActive := module.active.Add(1)
	updatePeak(&module.peak, moduleActive)
}

func (r *Registry) done(spec TaskSpec, state *taskState) {
	state.stopped.Add(1)
	active := state.active.Add(-1)
	if active == 0 {
		state.activeSince.Store(0)
	}
	if active < 0 {
		panic("goroutine: active count underflow")
	}
	if moduleActive := r.modules[spec.Module].active.Add(-1); moduleActive < 0 {
		panic("goroutine: module active count underflow")
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
	snapshot := r.Snapshot()
	for _, item := range snapshot.Modules {
		if item.Module == module {
			var active int64
			for _, task := range item.Tasks {
				active += activeAboveBaseline(task, baseline)
			}
			return active
		}
	}
	return 0
}

func (r *Registry) waitErrorAbove(module Module, baseline Baseline, cause error) error {
	snapshot := r.Snapshot()
	evidence := make([]WaitTaskEvidence, 0)
	for _, item := range snapshot.Modules {
		if item.Module != module {
			continue
		}
		for _, task := range item.Tasks {
			active := activeAboveBaseline(task, baseline)
			if active <= 0 {
				continue
			}
			evidence = append(evidence, WaitTaskEvidence{
				Task:       task.Task,
				Active:     active,
				RunningFor: task.RunningFor,
			})
			if len(evidence) == maxWaitEvidence {
				break
			}
		}
	}
	return &WaitError{Module: module, Tasks: evidence, Cause: cause}
}

func activeAboveBaseline(task TaskSnapshot, baseline Baseline) int64 {
	active := task.Active - baseline.active[task.Task]
	if active < 0 {
		return 0
	}
	return active
}

// Describe implements prometheus.Collector.
func (r *Registry) Describe(ch chan<- *prometheus.Desc) {
	ch <- r.descriptors.active
	ch <- r.descriptors.started
	ch <- r.descriptors.panics
	ch <- r.descriptors.poolBusy
	ch <- r.descriptors.poolCapacity
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
