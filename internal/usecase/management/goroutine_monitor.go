package management

import "time"

// GoroutineTaskSnapshot is the entry-independent read model for one managed task.
type GoroutineTaskSnapshot struct {
	// Task is the globally unique module/task identity.
	Task string `json:"task"`
	// Name is the stable task name within its module.
	Name string `json:"name"`
	// Kind is singleton, fixed, dynamic, pool, or burst.
	Kind string `json:"kind"`
	// Critical reports whether a panic is fatal to the process.
	Critical bool `json:"critical"`
	// Expected is the declared normal live count when known.
	Expected int `json:"expected,omitempty"`
	// Active is the current live goroutine count.
	Active int64 `json:"active"`
	// ProcessPeak is the highest live count observed during this process boot.
	ProcessPeak int64 `json:"process_peak"`
	// TotalStarted is the cumulative process-boot start count.
	TotalStarted int64 `json:"total_started"`
	// TotalStopped is the cumulative process-boot stop count.
	TotalStopped int64 `json:"total_stopped"`
	// Panics is the cumulative process-boot panic count.
	Panics int64 `json:"panics"`
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

// GoroutineModuleSnapshot aggregates managed goroutine state for one module.
type GoroutineModuleSnapshot struct {
	// Module is the stable low-cardinality owner.
	Module string `json:"module"`
	// Active is the current live goroutine count.
	Active int64 `json:"active"`
	// ProcessPeak is the highest aggregate live count observed during this process boot.
	ProcessPeak int64 `json:"process_peak"`
	// TotalStarted is the cumulative process-boot start count.
	TotalStarted int64 `json:"total_started"`
	// TotalStopped is the cumulative process-boot stop count.
	TotalStopped int64 `json:"total_stopped"`
	// Panics is the cumulative process-boot panic count.
	Panics int64 `json:"panics"`
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
	Tasks []GoroutineTaskSnapshot `json:"tasks"`
	// Health is the worst derived task health in the module.
	Health string `json:"health"`
}

// GoroutineSnapshot is one process-scoped managed goroutine read model.
type GoroutineSnapshot struct {
	// GeneratedAt is the UTC time when the snapshot was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// ProcessStartedAt is the process-local registry construction time.
	ProcessStartedAt time.Time `json:"process_started_at"`
	// BootID distinguishes counters and peaks across process restarts.
	BootID string `json:"boot_id"`
	// ProcessTotal is the current Go process goroutine count.
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
	Modules []GoroutineModuleSnapshot `json:"modules"`
}
