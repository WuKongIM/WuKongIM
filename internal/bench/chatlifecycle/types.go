package chatlifecycle

import "time"

// Profile selects the formal or local workload scale.
type Profile string

const (
	// ProfileFormal is the fixed formal lifecycle workload.
	ProfileFormal Profile = "formal"
	// ProfileLocal permits shorter local scale while retaining cluster topology and sync semantics.
	ProfileLocal Profile = "local"
)

// Mode selects the coordinator behavior for a validated lifecycle workload.
type Mode string

const (
	// ModeSoak runs the configured lifecycle soak plan.
	ModeSoak Mode = "soak"
	// ModeCapacity admits capacity staircase execution after aged-soak evidence.
	ModeCapacity Mode = "capacity"
)

// Stage selects the evidence claim made by one workload process lifetime.
type Stage string

const (
	// StageFormal runs the complete formal observation window.
	StageFormal Stage = "formal"
	// StageRehearsal runs the full-scale workload for the bounded rehearsal window.
	StageRehearsal Stage = "rehearsal"
	// StageShakeout runs the reduced local validation window.
	StageShakeout Stage = "shakeout"
)

// Config is the complete pure configuration for deterministic chat lifecycle planning.
type Config struct {
	// RunID is the non-secret identifier attached to plans and bounded snapshots.
	RunID string `json:"run_id" yaml:"run_id"`
	// Seed must be nonzero so deterministic random streams do not use an implicit seed.
	Seed uint64 `json:"seed" yaml:"seed"`
	// Profile selects formal fixed defaults or a smaller local shakeout workload.
	Profile Profile `json:"profile" yaml:"profile"`
	// Mode selects soak or capacity orchestration without changing workload semantics.
	Mode Mode `json:"mode" yaml:"mode"`
	// Stage fixes the evidence claim and measured duration for this process lifetime.
	Stage Stage `json:"stage" yaml:"stage"`
	// Workload contains only deterministic traffic and lifecycle quantities.
	Workload WorkloadConfig `json:"workload" yaml:"workload"`
	// Observation declares non-secret observation sources and sampling cadence.
	Observation ObservationConfig `json:"observation" yaml:"observation"`
	// Thresholds bounds correctness, latency, resource, cluster, and disk evidence.
	Thresholds ThresholdsConfig `json:"thresholds" yaml:"thresholds"`
	// Capacity contains aged-checkpoint and staircase requirements.
	Capacity CapacityConfig `json:"capacity" yaml:"capacity"`
}

// WorkloadConfig contains deterministic quantities and distributions only.
type WorkloadConfig struct {
	// Workers is the number of deterministic workload partitions.
	Workers int `json:"workers" yaml:"workers"`
	// OnlineUsers is the target concurrent connected user count.
	OnlineUsers int `json:"online_users" yaml:"online_users"`
	// NewUsersPerDay is the number of new identities introduced each day.
	NewUsersPerDay int `json:"new_users_per_day" yaml:"new_users_per_day"`
	// SendRatePerSecond is the global logical SEND rate before retries.
	SendRatePerSecond int `json:"send_rate_per_second" yaml:"send_rate_per_second"`
	// Traffic splits logical SEND traffic by channel kind.
	Traffic TrafficShareConfig `json:"traffic" yaml:"traffic"`
	// HotSet identifies the active person and group channel targets.
	HotSet HotSetConfig `json:"hot_set" yaml:"hot_set"`
	// Topology fixes the cluster-oriented slot and replication model.
	Topology TopologyConfig `json:"topology" yaml:"topology"`
	// RuntimeSampling bounds deterministic runtime observation samples.
	RuntimeSampling RuntimeSamplingConfig `json:"runtime_sampling" yaml:"runtime_sampling"`
	// Sync defines real client history synchronization semantics.
	Sync SyncConfig `json:"sync" yaml:"sync"`
	// BurstCredit is the permitted logical ingress credit window.
	BurstCredit time.Duration `json:"burst_credit" yaml:"burst_credit"`
	// MaxGlobalBurst is the maximum SEND count permitted by BurstCredit.
	MaxGlobalBurst int `json:"max_global_burst" yaml:"max_global_burst"`
	// MaxChannelsPerNode bounds the generated active channel allocation on a node.
	MaxChannelsPerNode int `json:"max_channels_per_node" yaml:"max_channels_per_node"`
	// Login selects new versus returning identity arrivals.
	Login LoginDistribution `json:"login" yaml:"login"`
	// Sessions selects online session lifetime buckets.
	Sessions []DurationShare `json:"sessions" yaml:"sessions"`
	// Lifecycle gives each semantically distinct activity class a named distribution bucket.
	Lifecycle LifecycleDistribution `json:"lifecycle" yaml:"lifecycle"`
	// Payloads selects deterministic message payload sizes.
	Payloads []PayloadShare `json:"payloads" yaml:"payloads"`
	// PersonDirection selects alternating versus one-way person traffic.
	PersonDirection PersonDirectionConfig `json:"person_direction" yaml:"person_direction"`
	// Relationship controls initial and returning relationship activity.
	Relationship RelationshipConfig `json:"relationship" yaml:"relationship"`
	// Retry fixes bounded retry timing; later runners choose the jitter policy.
	Retry RetryConfig `json:"retry" yaml:"retry"`
	// Groups declares the fixed group catalog and very-large-group send cadence.
	Groups GroupCatalogConfig `json:"groups" yaml:"groups"`
}

// TrafficShareConfig splits person and group logical SEND traffic in percentage points.
type TrafficShareConfig struct {
	PersonPercent int `json:"person_percent" yaml:"person_percent"`
	GroupPercent  int `json:"group_percent" yaml:"group_percent"`
}

// HotSetConfig contains active person and group channel targets.
type HotSetConfig struct {
	PersonChannels int `json:"person_channels" yaml:"person_channels"`
	GroupChannels  int `json:"group_channels" yaml:"group_channels"`
}

// TopologyConfig is the cluster topology assumed by deterministic planning.
type TopologyConfig struct {
	LogicalSlotGroups int `json:"logical_slot_groups" yaml:"logical_slot_groups"`
	HashSlots         int `json:"hash_slots" yaml:"hash_slots"`
	SlotReplicas      int `json:"slot_replicas" yaml:"slot_replicas"`
	ChannelReplicas   int `json:"channel_replicas" yaml:"channel_replicas"`
}

// RuntimeSamplingConfig bounds periodic runtime samples without storing runtime state.
type RuntimeSamplingConfig struct {
	Every time.Duration `json:"every" yaml:"every"`
	Size  int           `json:"size" yaml:"size"`
}

// SyncConfig defines the real history synchronization request shape.
type SyncConfig struct {
	Version      uint64 `json:"version" yaml:"version"`
	Limit        int    `json:"limit" yaml:"limit"`
	MessageCount int    `json:"message_count" yaml:"message_count"`
}

// LoginDistribution specifies new and returning login shares in percentage points.
type LoginDistribution struct {
	NewPercent       int `json:"new_percent" yaml:"new_percent"`
	ReturningPercent int `json:"returning_percent" yaml:"returning_percent"`
}

// DurationShare assigns a positive integer percentage to an inclusive positive
// duration range. Positive shares bound a 100-percent distribution to at most
// 100 buckets; zero durations are allowed only when a caller permits no range.
type DurationShare struct {
	// Percent must be in 1..100 and all buckets must total exactly 100.
	Percent int           `json:"percent" yaml:"percent"`
	Min     time.Duration `json:"min" yaml:"min"`
	Max     time.Duration `json:"max" yaml:"max"`
}

// LifecycleDistribution distinguishes one-shot, revisit, rotating, and long activity behavior.
type LifecycleDistribution struct {
	// OneShot is activity completed in its first visit and has no active-duration range.
	OneShot LifecycleBucket `json:"one_shot" yaml:"one_shot"`
	// Revisit is activity that returns after its first visit and has no active-duration range.
	Revisit LifecycleBucket `json:"revisit" yaml:"revisit"`
	// Rotating is activity that rotates on a bounded active-duration range.
	Rotating LifecycleBucket `json:"rotating" yaml:"rotating"`
	// Long is long-lived activity on a bounded active-duration range.
	Long LifecycleBucket `json:"long" yaml:"long"`
}

// LifecycleBucket assigns a class percentage and, where applicable, its active-duration range.
type LifecycleBucket struct {
	Percent        int           `json:"percent" yaml:"percent"`
	ActiveDuration DurationRange `json:"active_duration" yaml:"active_duration"`
}

// PayloadShare assigns a percentage share to one deterministic payload size.
type PayloadShare struct {
	Percent int `json:"percent" yaml:"percent"`
	Bytes   int `json:"bytes" yaml:"bytes"`
}

// PersonDirectionConfig selects the direction pattern for person channels.
type PersonDirectionConfig struct {
	AlternatingPercent int `json:"alternating_percent" yaml:"alternating_percent"`
	OneWayPercent      int `json:"one_way_percent" yaml:"one_way_percent"`
}

// IntRange is an inclusive integer range.
type IntRange struct {
	Min int `json:"min" yaml:"min"`
	Max int `json:"max" yaml:"max"`
}

// DurationRange is an inclusive positive duration range.
type DurationRange struct {
	Min time.Duration `json:"min" yaml:"min"`
	Max time.Duration `json:"max" yaml:"max"`
}

// RelationshipConfig controls bounded messages for new and returning relationships.
type RelationshipConfig struct {
	InitialMessages         IntRange      `json:"initial_messages" yaml:"initial_messages"`
	InitialMessageWindow    DurationRange `json:"initial_message_window" yaml:"initial_message_window"`
	ReturningMessages       IntRange      `json:"returning_messages" yaml:"returning_messages"`
	ReturningLast24hPercent int           `json:"returning_last_24h_percent" yaml:"returning_last_24h_percent"`
	ReturningOlderPercent   int           `json:"returning_older_percent" yaml:"returning_older_percent"`
}

// RetryConfig limits retry attempts and supplies the three deterministic base delays.
type RetryConfig struct {
	MaxCount int             `json:"max_count" yaml:"max_count"`
	Delays   []time.Duration `json:"delays" yaml:"delays"`
}

// GroupCatalogConfig defines the fixed group-cardinality catalog.
type GroupCatalogConfig struct {
	Small            int `json:"small" yaml:"small"`
	Medium           int `json:"medium" yaml:"medium"`
	Large            int `json:"large" yaml:"large"`
	VeryLarge        int `json:"very_large" yaml:"very_large"`
	VeryLargeMembers int `json:"very_large_members" yaml:"very_large_members"`
	// FixedMembership prevents lifecycle execution from changing generated group membership.
	FixedMembership    bool          `json:"fixed_membership" yaml:"fixed_membership"`
	VeryLargeSendEvery time.Duration `json:"very_large_send_every" yaml:"very_large_send_every"`
}

// ObservationConfig declares observation endpoints but never bearer values or secrets.
type ObservationConfig struct {
	// ServiceNodes declares product service-node observation endpoints.
	ServiceNodes []EndpointDeclaration `json:"service_nodes" yaml:"service_nodes"`
	// Workers declares workload-worker observation endpoints.
	Workers []EndpointDeclaration `json:"workers" yaml:"workers"`
	// HostMetrics declares node-local host-metrics observation endpoints.
	HostMetrics []EndpointDeclaration `json:"host_metrics" yaml:"host_metrics"`
	// LoadHostMetrics declares the separate workload-generator host endpoint.
	LoadHostMetrics EndpointDeclaration `json:"load_host_metrics" yaml:"load_host_metrics"`
	// APIAddrs is the non-secret HTTP API observation pool.
	APIAddrs []string `json:"api_addrs" yaml:"api_addrs"`
	// GatewayTCPAddrs is the separate non-secret TCP gateway observation pool.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs" yaml:"gateway_tcp_addrs"`
	// Cadence is the interval between bounded observation snapshots.
	Cadence time.Duration `json:"cadence" yaml:"cadence"`
}

// EndpointDeclaration names one non-secret observation source and its structurally usable address.
type EndpointDeclaration struct {
	// Name is the unique stable source name, such as api or host_metrics.
	Name string `json:"name" yaml:"name"`
	// Address is a non-secret endpoint declaration; credentials are supplied elsewhere.
	Address string `json:"address" yaml:"address"`
	// Mountpoint is the exact node_exporter data-filesystem mountpoint label; only host-metrics declarations use it.
	Mountpoint string `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`
	// Device is the exact node_exporter data-filesystem device label; only host-metrics declarations use it.
	Device string `json:"device,omitempty" yaml:"device,omitempty"`
}

// ThresholdsConfig contains all pass/fail bounds outside workload generation.
type ThresholdsConfig struct {
	MinimumDataFilesystemBytes int64                 `json:"minimum_data_filesystem_bytes" yaml:"minimum_data_filesystem_bytes"`
	DiskSafeStopFreePercent    int                   `json:"disk_safe_stop_free_percent" yaml:"disk_safe_stop_free_percent"`
	Correctness                CorrectnessThresholds `json:"correctness" yaml:"correctness"`
	Latency                    LatencyThresholds     `json:"latency" yaml:"latency"`
	Resource                   ResourceThresholds    `json:"resource" yaml:"resource"`
	Cluster                    ClusterThresholds     `json:"cluster" yaml:"cluster"`
	Timeline                   TimelineThresholds    `json:"timeline" yaml:"timeline"`
}

// CorrectnessThresholds bounds terminal operation and sequence correctness failures.
type CorrectnessThresholds struct {
	TerminalSends                int              `json:"terminal_sends" yaml:"terminal_sends"`
	ActivationRejections         int              `json:"activation_rejections" yaml:"activation_rejections"`
	Losses                       int              `json:"losses" yaml:"losses"`
	Duplicates                   int              `json:"duplicates" yaml:"duplicates"`
	Corruptions                  int              `json:"corruptions" yaml:"corruptions"`
	SequenceRegressions          int              `json:"sequence_regressions" yaml:"sequence_regressions"`
	OverallFirstAttemptFailure   FailureRateLimit `json:"overall_first_attempt_failure" yaml:"overall_first_attempt_failure"`
	AnyMinuteFirstAttemptFailure FailureRateLimit `json:"any_minute_first_attempt_failure" yaml:"any_minute_first_attempt_failure"`
}

// Comparison defines the exact operator used by a rational failure-rate limit.
type Comparison string

const (
	// ComparisonLessThan rejects a rate equal to the configured rational bound.
	ComparisonLessThan Comparison = "<"
	// ComparisonLessOrEqual permits a rate equal to the configured rational bound.
	ComparisonLessOrEqual Comparison = "<="
)

// FailureRateLimit compares failures as an exact rational ratio without float rounding.
type FailureRateLimit struct {
	MaxFailures uint32     `json:"max_failures" yaml:"max_failures"`
	PerAttempts uint32     `json:"per_attempts" yaml:"per_attempts"`
	Operator    Comparison `json:"operator" yaml:"operator"`
}

// LatencyThresholds bounds hot, cold, and sync operations plus anomaly behavior.
type LatencyThresholds struct {
	HotSendACK            LatencyLimit  `json:"hot_sendack" yaml:"hot_sendack"`
	Cold                  LatencyLimit  `json:"cold" yaml:"cold"`
	Sync                  LatencyLimit  `json:"sync" yaml:"sync"`
	SingleAnomaly         time.Duration `json:"single_anomaly" yaml:"single_anomaly"`
	SustainedBreachWindow time.Duration `json:"sustained_breach_window" yaml:"sustained_breach_window"`
}

// LatencyLimit bounds p99 and p99.9 latency for one operation class.
type LatencyLimit struct {
	P99  time.Duration `json:"p99" yaml:"p99"`
	P999 time.Duration `json:"p999" yaml:"p999"`
}

// ResourceThresholds bounds long-window forced-GC heap and goroutine growth.
type ResourceThresholds struct {
	ForcedGCLiveHeapGrowthPercent int           `json:"forced_gc_live_heap_growth_percent" yaml:"forced_gc_live_heap_growth_percent"`
	ForcedGCLiveHeapWindow        time.Duration `json:"forced_gc_live_heap_window" yaml:"forced_gc_live_heap_window"`
	GoroutineGrowthPercent        int           `json:"goroutine_growth_percent" yaml:"goroutine_growth_percent"`
	GoroutineGrowthWindow         time.Duration `json:"goroutine_growth_window" yaml:"goroutine_growth_window"`
	// HostCPUPercent is the exclusive host-wide busy-CPU saturation boundary in percent.
	HostCPUPercent int `json:"host_cpu_percent" yaml:"host_cpu_percent"`
	// HostMemoryPercent is the exclusive host-wide used-memory saturation boundary in percent.
	HostMemoryPercent int `json:"host_memory_percent" yaml:"host_memory_percent"`
	// BoundedQueuePercent is the exclusive service runtime queue saturation boundary in percent.
	BoundedQueuePercent int `json:"bounded_queue_percent" yaml:"bounded_queue_percent"`
	// SustainedSaturationWindow is the uninterrupted breach duration required for infrastructure attribution.
	SustainedSaturationWindow time.Duration `json:"sustained_saturation_window" yaml:"sustained_saturation_window"`
	// MinimumLoadFilesystemBytes is the minimum usable data-filesystem size required on the load host.
	MinimumLoadFilesystemBytes int64 `json:"minimum_load_filesystem_bytes" yaml:"minimum_load_filesystem_bytes"`
	// PrometheusSafeStopBytes is the inclusive watched-directory size that triggers a fatal safe stop.
	PrometheusSafeStopBytes int64 `json:"prometheus_safe_stop_bytes" yaml:"prometheus_safe_stop_bytes"`
}

// ClusterThresholds bounds health and leader-distribution evidence.
type ClusterThresholds struct {
	HealthPollEvery    time.Duration `json:"health_poll_every" yaml:"health_poll_every"`
	UnhealthyFailAfter time.Duration `json:"unhealthy_fail_after" yaml:"unhealthy_fail_after"`
	// MaxHotReplicaLagEntries is the largest leader-reported Raft entry lag still considered in sync for a hot Slot group.
	MaxHotReplicaLagEntries uint64        `json:"max_hot_replica_lag_entries" yaml:"max_hot_replica_lag_entries"`
	LeaderImbalancePercent  int           `json:"leader_imbalance_percent" yaml:"leader_imbalance_percent"`
	LeaderImbalanceFor      time.Duration `json:"leader_imbalance_for" yaml:"leader_imbalance_for"`
}

// TimelineThresholds defines warmup, aged checkpoint, and final evidence times.
type TimelineThresholds struct {
	Warmup     time.Duration `json:"warmup" yaml:"warmup"`
	Checkpoint time.Duration `json:"checkpoint" yaml:"checkpoint"`
	Final      time.Duration `json:"final" yaml:"final"`
}

// CapacityConfig controls capacity-mode admission and the staircase search schedule.
type CapacityConfig struct {
	AgedCheckpoint AgedCheckpoint `json:"aged_checkpoint" yaml:"aged_checkpoint"`
	// StartRatePerSecond is the initial offered ingress rate for the staircase.
	StartRatePerSecond int `json:"start_rate_per_second" yaml:"start_rate_per_second"`
	// RecoveryRatePerSecond is the fixed reconnect/recovery rate between steps.
	RecoveryRatePerSecond int `json:"recovery_rate_per_second" yaml:"recovery_rate_per_second"`
	StepPercent           int `json:"step_percent" yaml:"step_percent"`
	RefinePercent         int `json:"refine_percent" yaml:"refine_percent"`
	// MaximumDuration bounds only the staircase search; recovery follows it.
	MaximumDuration  time.Duration `json:"maximum_duration" yaml:"maximum_duration"`
	Step             CapacityStep  `json:"step" yaml:"step"`
	RecoveryDuration time.Duration `json:"recovery_duration" yaml:"recovery_duration"`
}

// AgedCheckpoint is a typed reference to a completed passing prior lifecycle run.
type AgedCheckpoint struct {
	Reference string        `json:"reference" yaml:"reference"`
	Completed bool          `json:"completed" yaml:"completed"`
	Passed    bool          `json:"passed" yaml:"passed"`
	Duration  time.Duration `json:"duration" yaml:"duration"`
}

// CapacityStep divides one capacity staircase step into stabilization and measurement.
type CapacityStep struct {
	Stabilize time.Duration `json:"stabilize" yaml:"stabilize"`
	Measure   time.Duration `json:"measure" yaml:"measure"`
}
