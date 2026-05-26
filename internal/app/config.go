package app

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/messageid"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

// Config contains all application configuration loaded for one WuKongIM node.
type Config struct {
	// TestMode enables e2e-only guest test-data endpoints. Never enable it in production.
	TestMode bool
	// Node configures this process identity and local data root.
	Node NodeConfig
	// Storage configures local durable storage paths.
	Storage StorageConfig
	// Cluster configures controller, slot, and channel replication runtimes.
	Cluster ClusterConfig
	// ChannelPlane configures channel-keyed durable append reactors.
	ChannelPlane ChannelPlaneConfig
	// ChannelMigration configures the cluster-authoritative channel replica migration executor.
	ChannelMigration ChannelMigrationConfig
	// ChannelMessageRetention configures leader-driven channel message expiry.
	ChannelMessageRetention ChannelMessageRetentionConfig
	// Message configures message send business rules.
	Message MessageConfig
	// Delivery configures realtime delivery routing and fanout.
	Delivery DeliveryConfig
	// Plugin configures node-local PDK-compatible plugin execution.
	Plugin PluginConfig
	// API configures the public HTTP API entry point.
	API APIConfig
	// Bench controls unauthenticated benchmark-only APIs used by wkbench.
	Bench BenchConfig
	// Manager configures the administration HTTP API entry point.
	Manager ManagerConfig
	// Gateway configures client connection listeners and session behavior.
	Gateway GatewayConfig
	// Conversation configures conversation projection and sync behavior.
	Conversation ConversationConfig
	// Observability configures metrics and health endpoint detail.
	Observability ObservabilityConfig
	// Log configures application logging output.
	Log LogConfig
}

// BenchConfig controls unauthenticated benchmark-only APIs used by wkbench.
type BenchConfig struct {
	// APIEnabled registers /bench/v1/* routes for controlled benchmark environments.
	APIEnabled bool
	// APIMaxBatchSize limits top-level records accepted by a bench API request.
	APIMaxBatchSize int
	// APIMaxPayloadBytes limits HTTP request body size accepted by bench APIs.
	APIMaxPayloadBytes int64
}

// PluginConfig controls node-local PDK-compatible plugin execution.
type PluginConfig struct {
	// Enable starts the node-local plugin runtime. It defaults to false because plugins execute local binaries.
	Enable bool
	// Dir contains local .wkp plugin binaries for this node.
	Dir string
	// SocketPath is the Unix socket path used by PDK plugins to call the host.
	SocketPath string
	// SandboxDir is the per-plugin writable directory root passed to plugin processes.
	SandboxDir string
	// StateDir stores node-local desired plugin config and enable state.
	StateDir string
	// Timeout bounds plugin RPC calls and graceful stop waits.
	Timeout time.Duration
	// HotReload watches Dir for .wkp changes and restarts affected local plugins.
	HotReload bool
	// FailOpen lets sends continue when Send hooks fail; false rejects sends on plugin errors.
	FailOpen bool

	timeoutSet   bool
	hotReloadSet bool
	failOpenSet  bool
}

// ChannelPlaneConfig controls durable send routing reactors and peer batching.
type ChannelPlaneConfig struct {
	// ReactorCount is the number of channel-keyed append reactor shards. Zero uses a CPU-aware default.
	ReactorCount int
	// PeerLaneCount is the number of remote append batching lanes per target node. Zero uses a CPU-aware default.
	PeerLaneCount int
	// PeerBatchMaxWait bounds how long a remote peer lane waits before flushing a partial batch. Zero uses the default.
	PeerBatchMaxWait time.Duration
}

// SetExplicitFlags records which plugin values were explicitly configured.
func (c *PluginConfig) SetExplicitFlags(timeoutSet, hotReloadSet, failOpenSet bool) {
	if c == nil {
		return
	}
	c.timeoutSet = timeoutSet
	c.hotReloadSet = hotReloadSet
	c.failOpenSet = failOpenSet
}

// ChannelMigrationConfig controls the background executor that advances durable channel migration tasks.
type ChannelMigrationConfig struct {
	// ScanInterval is the delay between channel migration executor scans on this node.
	ScanInterval time.Duration
	// ScanLimit caps how many runnable migration tasks one executor tick reads from local slot leaders.
	ScanLimit int
	// OwnerLeaseTTL controls how long this node may own one migration task before another slot leader may reclaim it.
	OwnerLeaseTTL time.Duration
	// RetryBackoff delays the next attempt after a retryable migration phase error or blocked dependency.
	RetryBackoff time.Duration
	// FenceTTL controls how long a migration-owned channel write fence remains valid before recovery must reset it.
	FenceTTL time.Duration
	// LeaderLeaseTTL is the lease duration written to channel runtime metadata after a migration leader transfer.
	LeaderLeaseTTL time.Duration
	// CatchUpStableWindow controls how long target lag must stay at or below CatchUpLagThreshold before cutover.
	CatchUpStableWindow time.Duration
	// CatchUpLagThreshold is the maximum leader-to-target record lag accepted as warm catch-up stable.
	CatchUpLagThreshold uint64
	// MaxConcurrent limits active channel migration tasks this node advances in one scan; zero disables the global limit.
	MaxConcurrent int
	// MaxConcurrentPerSource limits active replacement tasks draining or removing the same source node; zero disables the per-source limit.
	MaxConcurrentPerSource int
	// MaxConcurrentPerTarget limits active tasks bootstrapping, catching up, or promoting the same target node; zero disables the per-target limit.
	MaxConcurrentPerTarget int
	// CompletedRetentionTTL controls how long terminal migration tasks remain queryable before garbage collection; zero disables cleanup.
	CompletedRetentionTTL time.Duration
	// GCLimit controls how many terminal migration tasks one cleanup tick may delete.
	GCLimit int

	scanIntervalSet           bool
	scanLimitSet              bool
	ownerLeaseTTLSet          bool
	retryBackoffSet           bool
	fenceTTLSet               bool
	leaderLeaseTTLSet         bool
	catchUpStableWindowSet    bool
	catchUpLagThresholdSet    bool
	maxConcurrentSet          bool
	maxConcurrentPerSourceSet bool
	maxConcurrentPerTargetSet bool
	completedRetentionTTLSet  bool
	gcLimitSet                bool
}

// SetExplicitFlags records which channel migration tuning values were explicitly configured.
func (c *ChannelMigrationConfig) SetExplicitFlags(
	scanIntervalSet bool,
	scanLimitSet bool,
	ownerLeaseTTLSet bool,
	retryBackoffSet bool,
	fenceTTLSet bool,
	leaderLeaseTTLSet bool,
	catchUpStableWindowSet bool,
	catchUpLagThresholdSet bool,
	maxConcurrentSet bool,
	maxConcurrentPerSourceSet bool,
	maxConcurrentPerTargetSet bool,
	completedRetentionTTLSet bool,
	gcLimitSet bool,
) {
	if c == nil {
		return
	}
	c.scanIntervalSet = scanIntervalSet
	c.scanLimitSet = scanLimitSet
	c.ownerLeaseTTLSet = ownerLeaseTTLSet
	c.retryBackoffSet = retryBackoffSet
	c.fenceTTLSet = fenceTTLSet
	c.leaderLeaseTTLSet = leaderLeaseTTLSet
	c.catchUpStableWindowSet = catchUpStableWindowSet
	c.catchUpLagThresholdSet = catchUpLagThresholdSet
	c.maxConcurrentSet = maxConcurrentSet
	c.maxConcurrentPerSourceSet = maxConcurrentPerSourceSet
	c.maxConcurrentPerTargetSet = maxConcurrentPerTargetSet
	c.completedRetentionTTLSet = completedRetentionTTLSet
	c.gcLimitSet = gcLimitSet
}

// MessageConfig controls send-path business compatibility rules.
type MessageConfig struct {
	// PersonWhitelistEnabled enables receiver-side personal allowlist enforcement for sends.
	// It is disabled by default to match legacy WhitelistOffOfPerson=true compatibility.
	PersonWhitelistEnabled bool
	// SystemDeviceID identifies trusted gateway sessions that bypass channel-type-specific
	// send permissions after sender SendBan has passed.
	SystemDeviceID string
	// PermissionCacheTTL enables a bounded node-local send-permission read cache, including
	// missing-channel authorization reads. Keep zero for strict authorization freshness.
	PermissionCacheTTL time.Duration
	// UserRateLimitEnabled enables node-local UID send token buckets before expensive send-path work.
	UserRateLimitEnabled bool
	// UserRateLimitRate is the sustained number of sends admitted per UID per second.
	UserRateLimitRate float64
	// UserRateLimitBurst is the maximum immediate send burst admitted for one UID.
	UserRateLimitBurst int
	// UserRateLimitBucketShards controls lock striping for in-memory UID buckets.
	UserRateLimitBucketShards int
	// UserRateLimitIdleTTL controls how long inactive UID buckets remain in memory.
	UserRateLimitIdleTTL time.Duration
	// UserRateLimitMaxBuckets caps allocated UID buckets to bound memory under high UID cardinality.
	UserRateLimitMaxBuckets int
	// UserRateLimitSystemUIDBypass lets trusted system UIDs bypass user send rate limiting.
	UserRateLimitSystemUIDBypass bool
	// UserRateLimitPluginBypass lets plugin-origin sends bypass user send rate limiting.
	UserRateLimitPluginBypass bool

	userRateLimitRateSet            bool
	userRateLimitBurstSet           bool
	userRateLimitBucketShardsSet    bool
	userRateLimitIdleTTLSet         bool
	userRateLimitMaxBucketsSet      bool
	userRateLimitSystemUIDBypassSet bool
}

// SetExplicitFlags records which message tuning values were explicitly configured.
func (c *MessageConfig) SetExplicitFlags(rateSet, burstSet, bucketShardsSet, idleTTLSet, maxBucketsSet, systemUIDBypassSet bool) {
	if c == nil {
		return
	}
	c.userRateLimitRateSet = rateSet
	c.userRateLimitBurstSet = burstSet
	c.userRateLimitBucketShardsSet = bucketShardsSet
	c.userRateLimitIdleTTLSet = idleTTLSet
	c.userRateLimitMaxBucketsSet = maxBucketsSet
	c.userRateLimitSystemUIDBypassSet = systemUIDBypassSet
}

// DeliveryConfig controls realtime delivery routing and fanout behavior.
type DeliveryConfig struct {
	// PresenceCacheTTL enables a bounded node-local cache for positive UID presence routes
	// used by delivery fanout. Keep zero for strict presence freshness.
	PresenceCacheTTL time.Duration
	// AckBatchMaxWait bounds how long remote delivery acknowledgements may wait
	// while they are coalesced for the same owner node. Zero disables batching.
	AckBatchMaxWait time.Duration
	// AckBatchMaxSize flushes remote delivery acknowledgements immediately once
	// the pending owner-node batch reaches this size. Zero uses a safe default.
	AckBatchMaxSize int
}

// ChannelMessageRetentionConfig controls cluster-authoritative channel message retention.
type ChannelMessageRetentionConfig struct {
	// TTL is the age after which channel messages may be hidden by leader-driven retention.
	// A zero value disables automatic retention so existing messages remain readable until
	// another cluster-authoritative retention boundary is advanced by an operator or worker.
	TTL time.Duration
	// ScanInterval is the delay between retention worker scans after TTL-based retention is
	// enabled. It is ignored while TTL is zero, but defaults to a positive value so enabling
	// TTL without an explicit interval starts a bounded periodic worker.
	ScanInterval time.Duration
	// ChannelBatchSize limits how many local channel logs one retention pass scans.
	// It bounds cross-channel scan work and must be positive when TTL-based retention is enabled.
	ChannelBatchSize int
	// MaxTrimMessages limits how many expired messages one channel may include in one retention pass.
	// It bounds per-channel deletion planning and must be positive when TTL-based retention is enabled.
	MaxTrimMessages int

	scanIntervalSet     bool
	channelBatchSizeSet bool
	maxTrimMessagesSet  bool
}

// SetExplicitFlags records whether retention tuning values were explicitly configured.
func (c *ChannelMessageRetentionConfig) SetExplicitFlags(scanIntervalSet, channelBatchSizeSet, maxTrimMessagesSet bool) {
	if c == nil {
		return
	}
	c.scanIntervalSet = scanIntervalSet
	c.channelBatchSizeSet = channelBatchSizeSet
	c.maxTrimMessagesSet = maxTrimMessagesSet
}

// ObservabilityConfig controls runtime metrics and health response detail.
type ObservabilityConfig struct {
	// MetricsEnabled enables the metrics endpoint and metrics collection.
	MetricsEnabled bool
	// NetworkEnabled enables per-packet local network observations for manager network snapshots.
	NetworkEnabled bool
	// HealthDetailEnabled includes dependency details in health responses.
	HealthDetailEnabled bool
	// HealthDebugEnabled includes debug-oriented health diagnostics.
	HealthDebugEnabled bool
	// Diagnostics configures the bounded local diagnostics event store and sampling policy.
	Diagnostics DiagnosticsConfig

	metricsEnabledSet      bool
	networkEnabledSet      bool
	healthDetailEnabledSet bool
	healthDebugEnabledSet  bool
}

// SetExplicitFlags records whether observability booleans were explicitly configured.
func (c *ObservabilityConfig) SetExplicitFlags(metricsSet, detailSet, debugSet bool) {
	if c == nil {
		return
	}
	c.metricsEnabledSet = metricsSet
	c.healthDetailEnabledSet = detailSet
	c.healthDebugEnabledSet = debugSet
}

// SetNetworkExplicitFlag records whether network observation was explicitly configured.
func (c *ObservabilityConfig) SetNetworkExplicitFlag(networkSet bool) {
	if c == nil {
		return
	}
	c.networkEnabledSet = networkSet
}

// SetDiagnosticsExplicitFlags records which diagnostics values were explicitly configured.
func (c *ObservabilityConfig) SetDiagnosticsExplicitFlags(enabledSet, sampleRateSet, errorSampleRateSet, debugAPIEnabledSet bool) {
	if c == nil {
		return
	}
	c.Diagnostics.enabledSet = enabledSet
	c.Diagnostics.sampleRateSet = sampleRateSet
	c.Diagnostics.errorSampleRateSet = errorSampleRateSet
	c.Diagnostics.debugAPIEnabledSet = debugAPIEnabledSet
}

// DiagnosticsConfig controls local diagnostics event retention and debug query exposure.
type DiagnosticsConfig struct {
	// Enabled turns local diagnostics event capture on or off.
	Enabled bool
	// BufferSize is the maximum number of diagnostics events retained in memory.
	BufferSize int
	// SampleRate is the baseline keep probability for successful diagnostics events.
	SampleRate float64
	// SlowThreshold keeps successful events whose duration is at least this threshold.
	SlowThreshold time.Duration
	// ErrorSampleRate is the keep probability for diagnostics events with non-ok results.
	ErrorSampleRate float64
	// DebugAPIEnabled enables debug HTTP endpoints backed by the local diagnostics store.
	DebugAPIEnabled bool
	// DebugMatches configures temporary high-priority sampling rules.
	DebugMatches []DiagnosticsDebugMatchConfig

	enabledSet         bool
	sampleRateSet      bool
	errorSampleRateSet bool
	debugAPIEnabledSet bool
}

// DiagnosticsDebugMatchConfig defines one temporary diagnostics sampling override rule.
type DiagnosticsDebugMatchConfig struct {
	// UID matches the sender UID when it is set.
	UID string `json:"uid,omitempty"`
	// ChannelKey matches the diagnostics-safe channel identifier when it is set.
	ChannelKey string `json:"channel_key,omitempty"`
	// ClientMsgNo matches the client message number when it is set.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// TraceID matches the trace identifier when it is set.
	TraceID string `json:"trace_id,omitempty"`
	// TTLSeconds controls how long the temporary debug sampling rule stays active.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// SampleRate is the keep probability applied when the rule matches.
	SampleRate float64 `json:"sample_rate,omitempty"`
}

// LogConfig defines zap and lumberjack logging settings.
type LogConfig struct {
	// Level is the minimum log level, such as debug, info, warn, or error.
	Level string
	// Dir is the directory used for rolling log files.
	Dir string
	// MaxSize is the maximum size in megabytes before a log file rotates.
	MaxSize int
	// MaxAge is the maximum number of days to retain old log files.
	MaxAge int
	// MaxBackups is the maximum number of rotated log files to retain.
	MaxBackups int
	// Compress enables gzip compression for rotated log files.
	Compress bool
	// Console enables writing logs to stdout or stderr.
	Console bool
	// Format selects the log encoder format, such as console or json.
	Format string

	compressSet bool
	consoleSet  bool
}

// SetExplicitFlags records whether log booleans were explicitly configured.
func (c *LogConfig) SetExplicitFlags(compressSet, consoleSet bool) {
	if c == nil {
		return
	}
	c.compressSet = compressSet
	c.consoleSet = consoleSet
}

// NodeConfig defines this node's cluster identity and local data root.
type NodeConfig struct {
	// ID is the stable cluster node ID and must fit the message ID generator range.
	ID uint64
	// Name is the human-readable node name used in diagnostics.
	Name string
	// DataDir is the base directory used to derive default storage paths.
	DataDir string
}

// StorageConfig defines local paths for metadata, Raft, and channel logs.
type StorageConfig struct {
	// DBPath is the local metadata database path.
	DBPath string
	// RaftPath is the local slot Raft log path.
	RaftPath string
	// ChannelLogPath is the local channel message log path.
	ChannelLogPath string
	// ControllerMetaPath is the local controller metadata path.
	ControllerMetaPath string
	// ControllerRaftPath is the local controller Raft log path.
	ControllerRaftPath string
	// RaftSnapshotPath stores external Slot Raft snapshot chunks outside the Slot Raft log database.
	RaftSnapshotPath string
	// ControllerRaftSnapshotPath stores external Controller Raft snapshot chunks outside the Controller Raft log database.
	ControllerRaftSnapshotPath string
	// RaftSnapshotChunkSize is the maximum external snapshot chunk size in bytes before a snapshot is split into another chunk file.
	RaftSnapshotChunkSize uint64
	// RaftSnapshotGCGrace controls how long orphan external snapshot directories remain on disk before they become eligible for cleanup.
	RaftSnapshotGCGrace time.Duration

	raftSnapshotChunkSizeSet bool
	raftSnapshotGCGraceSet   bool
}

// SetRaftSnapshotExplicitFlags records which scalar snapshot settings were explicitly configured.
func (c *StorageConfig) SetRaftSnapshotExplicitFlags(chunkSizeSet, gcGraceSet bool) {
	if c == nil {
		return
	}
	c.raftSnapshotChunkSizeSet = chunkSizeSet
	c.raftSnapshotGCGraceSet = gcGraceSet
}

// ControllerLogCompactionConfig controls local Controller Raft snapshot compaction.
type ControllerLogCompactionConfig struct {
	// Enabled controls whether this node creates local Controller Raft snapshots.
	Enabled bool
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between compaction checks.
	CheckInterval time.Duration

	enabledSet        bool
	triggerEntriesSet bool
	checkIntervalSet  bool
}

// SlotLogCompactionConfig controls local Slot Raft snapshot compaction.
type SlotLogCompactionConfig struct {
	// Enabled controls whether this node creates local Slot Raft snapshots.
	Enabled bool
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between compaction checks.
	CheckInterval time.Duration

	enabledSet        bool
	triggerEntriesSet bool
	checkIntervalSet  bool
}

// ClusterConfig defines controller, slot, and channel replication settings for this node's cluster runtime.
type ClusterConfig struct {
	// ListenAddr is the node-to-node cluster RPC listen address.
	ListenAddr string
	// SlotCount is the legacy configured physical slot count and mirrors InitialSlotCount when set.
	SlotCount uint32
	// HashSlotCount is the number of hash slots used to map keys to managed physical slots.
	HashSlotCount uint16
	// EnableHashSlotMigration allows experimental hash-slot migration workflows.
	// Keep this disabled unless durable delta forwarding, source fencing, and
	// recoverable cutover semantics are explicitly accepted by the operator.
	EnableHashSlotMigration bool
	// InitialSlotCount is the number of managed physical slots to create at bootstrap.
	InitialSlotCount uint32
	// ChannelBootstrapDefaultMinISR is the default MinISR for newly bootstrapped channel metadata.
	ChannelBootstrapDefaultMinISR int
	// MaxChannels limits active local channel runtimes on this node.
	// A zero value keeps the legacy unlimited behavior; a positive value
	// makes channel activation fail closed when the per-node runtime budget is exhausted.
	MaxChannels int
	// ChannelIdleTimeout is the idle duration after which a local channel runtime may be evicted.
	// A zero value disables idle eviction so activated channel runtimes remain resident until removal or shutdown.
	ChannelIdleTimeout time.Duration
	// ChannelIdleScanInterval is how often this node scans for idle local channel runtimes.
	// A zero value lets the channel runtime derive a bounded scan interval from ChannelIdleTimeout.
	ChannelIdleScanInterval time.Duration
	// ChannelExecutionMode selects local channel replica execution scheduling.
	// Empty values default to "pooled"; "dedicated" remains available as a rollback mode
	// with legacy per-replica workers.
	ChannelExecutionMode string
	// ChannelExecutionWorkers is the number of shared workers used by pooled channel execution.
	// A zero value lets the replica execution runtime derive a default from GOMAXPROCS.
	ChannelExecutionWorkers int
	// ChannelExecutionQueueSize bounds pooled execution queues to avoid unbounded memory growth.
	// A zero value uses the replica execution runtime default.
	ChannelExecutionQueueSize int
	// LongPollLaneCount is the number of channel fetch long-poll lanes.
	LongPollLaneCount int
	// LongPollMaxWait is the maximum wait for one channel fetch long-poll request.
	LongPollMaxWait time.Duration
	// LongPollMaxBytes is the maximum response size for one channel fetch long-poll request.
	LongPollMaxBytes int
	// LongPollMaxChannels is the maximum number of channels served by one long-poll cycle.
	LongPollMaxChannels int
	// LongPollDataNotifyDelay coalesces append wakeups so one lane response can batch multiple channels.
	LongPollDataNotifyDelay time.Duration
	// FollowerReplicationRetryInterval is the retry interval for channel follower replication.
	FollowerReplicationRetryInterval time.Duration
	// AppendGroupCommitMaxWait is the maximum delay for channel append group commit batching.
	AppendGroupCommitMaxWait time.Duration
	// AppendGroupCommitMaxRecords is the maximum record count for one append group commit batch.
	AppendGroupCommitMaxRecords int
	// AppendGroupCommitMaxBytes is the maximum byte size for one append group commit batch.
	AppendGroupCommitMaxBytes int
	// CommitCoordinatorFlushWindow is the maximum delay for cross-channel durable Pebble sync batching.
	CommitCoordinatorFlushWindow time.Duration
	// CommitCoordinatorMaxRequests caps logical requests in one cross-channel durable Pebble sync batch.
	CommitCoordinatorMaxRequests int
	// CommitCoordinatorMaxRecords caps channel log records in one cross-channel durable Pebble sync batch.
	CommitCoordinatorMaxRecords int
	// CommitCoordinatorMaxBytes caps approximate payload bytes in one cross-channel durable Pebble sync batch.
	CommitCoordinatorMaxBytes int
	// Nodes lists every cluster node participating in the cluster runtime.
	Nodes []NodeConfigRef
	// Seeds lists existing cluster RPC addresses used only to bootstrap dynamic node join.
	Seeds []string
	// AdvertiseAddr is this node's cluster RPC address published to controller membership.
	AdvertiseAddr string
	// JoinToken authenticates automatic node join requests.
	JoinToken string
	// Slots is deprecated and must remain empty because slots are managed by the controller.
	Slots []SlotConfig
	// ControllerReplicaN is the number of controller Raft voters.
	ControllerReplicaN int
	// SlotReplicaN is the target replica count for each managed slot.
	SlotReplicaN int
	// ForwardTimeout is the timeout for internal forwarding operations.
	ForwardTimeout time.Duration
	// PoolSize is the general cluster worker pool size hint.
	PoolSize int
	// DataPlanePoolSize is the channel data-plane RPC pool size.
	DataPlanePoolSize int
	// TickInterval is the Raft tick interval.
	TickInterval time.Duration
	// RaftWorkers is the number of workers used by the Raft runtime.
	RaftWorkers int
	// ElectionTick is the Raft election timeout measured in ticks.
	ElectionTick int
	// HeartbeatTick is the Raft heartbeat interval measured in ticks.
	HeartbeatTick int
	// DialTimeout is the timeout for node-to-node connection dialing.
	DialTimeout time.Duration
	// ManagedSlotsReadyTimeout bounds startup waiting for controller-managed physical slots to become ready.
	// Increase this for cold multi-node cluster restarts where controller election and slot leader recovery can exceed the default.
	ManagedSlotsReadyTimeout time.Duration
	// ControllerLogCompaction controls local Controller Raft snapshot compaction.
	ControllerLogCompaction ControllerLogCompactionConfig
	// SlotLogCompaction controls local Slot Raft snapshot compaction.
	SlotLogCompaction SlotLogCompactionConfig
	// Timeouts configures controller observation, managed slot, and retry budgets.
	Timeouts raftcluster.Timeouts
	// DataPlaneRPCTimeout is the timeout for channel data-plane RPCs.
	DataPlaneRPCTimeout time.Duration
	// DataPlaneMaxFetchInflight limits concurrent fetch RPCs per data-plane client.
	DataPlaneMaxFetchInflight int
	// DataPlaneMaxPendingFetch limits queued fetch RPCs per data-plane client.
	DataPlaneMaxPendingFetch int

	channelBootstrapDefaultMinISRSet    bool
	followerReplicationRetryIntervalSet bool
	appendGroupCommitMaxWaitSet         bool
	appendGroupCommitMaxRecordsSet      bool
	appendGroupCommitMaxBytesSet        bool
	commitCoordinatorFlushWindowSet     bool
	longPollLaneCountSet                bool
	longPollMaxWaitSet                  bool
	longPollMaxBytesSet                 bool
	longPollMaxChannelsSet              bool
}

// SetExplicitFlags records whether channel replication settings were explicitly configured.
func (c *ClusterConfig) SetExplicitFlags(
	channelBootstrapDefaultMinISRSet bool,
	followerReplicationRetryIntervalSet bool,
	appendGroupCommitMaxWaitSet bool,
	appendGroupCommitMaxRecordsSet bool,
	appendGroupCommitMaxBytesSet bool,
) {
	if c == nil {
		return
	}
	c.channelBootstrapDefaultMinISRSet = channelBootstrapDefaultMinISRSet
	c.followerReplicationRetryIntervalSet = followerReplicationRetryIntervalSet
	c.appendGroupCommitMaxWaitSet = appendGroupCommitMaxWaitSet
	c.appendGroupCommitMaxRecordsSet = appendGroupCommitMaxRecordsSet
	c.appendGroupCommitMaxBytesSet = appendGroupCommitMaxBytesSet
}

// SetReplicationExplicitFlags records whether long-poll replication settings were explicitly configured.
func (c *ClusterConfig) SetReplicationExplicitFlags(longPollLaneCountSet, longPollMaxWaitSet, longPollMaxBytesSet, longPollMaxChannelsSet bool) {
	if c == nil {
		return
	}
	c.longPollLaneCountSet = longPollLaneCountSet
	c.longPollMaxWaitSet = longPollMaxWaitSet
	c.longPollMaxBytesSet = longPollMaxBytesSet
	c.longPollMaxChannelsSet = longPollMaxChannelsSet
}

// SetCommitCoordinatorExplicitFlags records whether durable commit coordinator settings were explicitly configured.
func (c *ClusterConfig) SetCommitCoordinatorExplicitFlags(flushWindowSet, maxRequestsSet, maxRecordsSet, maxBytesSet bool) {
	if c == nil {
		return
	}
	c.commitCoordinatorFlushWindowSet = flushWindowSet
}

// SetControllerLogCompactionExplicitFlags records which Controller log compaction values were explicitly configured.
func (c *ClusterConfig) SetControllerLogCompactionExplicitFlags(enabledSet, triggerEntriesSet, checkIntervalSet bool) {
	if c == nil {
		return
	}
	c.ControllerLogCompaction.enabledSet = enabledSet
	c.ControllerLogCompaction.triggerEntriesSet = triggerEntriesSet
	c.ControllerLogCompaction.checkIntervalSet = checkIntervalSet
}

// SetSlotLogCompactionExplicitFlags records which Slot log compaction values were explicitly configured.
func (c *ClusterConfig) SetSlotLogCompactionExplicitFlags(enabledSet, triggerEntriesSet, checkIntervalSet bool) {
	if c == nil {
		return
	}
	c.SlotLogCompaction.enabledSet = enabledSet
	c.SlotLogCompaction.triggerEntriesSet = triggerEntriesSet
	c.SlotLogCompaction.checkIntervalSet = checkIntervalSet
}

// JoinModeEnabled reports whether any dynamic node join bootstrap setting is configured.
func (c ClusterConfig) JoinModeEnabled() bool {
	return len(c.Seeds) > 0 || c.AdvertiseAddr != "" || c.JoinToken != ""
}

// NodeConfigRef describes one node entry in the cluster membership list.
type NodeConfigRef struct {
	// ID is the stable numeric cluster node ID.
	ID uint64
	// Addr is the node-to-node cluster RPC address for this node.
	Addr string
}

// SlotConfig describes a deprecated static slot assignment.
type SlotConfig struct {
	// ID is the deprecated static slot ID.
	ID uint32
	// Peers lists deprecated static slot replica node IDs.
	Peers []uint64
}

// DerivedControllerNodes returns the sorted controller voter subset.
func (c ClusterConfig) DerivedControllerNodes() []NodeConfigRef {
	nodes := append([]NodeConfigRef(nil), c.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	if c.ControllerReplicaN > 0 && c.ControllerReplicaN < len(nodes) {
		nodes = nodes[:c.ControllerReplicaN]
	}
	return nodes
}

// GatewayConfig defines client gateway listener and session settings.
type GatewayConfig struct {
	// TokenAuthOn enables token authentication for gateway sessions.
	TokenAuthOn bool
	// SendTimeout bounds how long gateway send handling waits for durable completion.
	SendTimeout time.Duration
	// DefaultSession defines default session buffering and timeout behavior.
	DefaultSession gateway.SessionOptions
	// Transport defines transport-specific runtime tuning for gateway listeners.
	Transport gateway.TransportOptions
	// Listeners lists gateway listener bindings.
	Listeners []gateway.ListenerOptions

	sendTimeoutSet bool
}

// SetExplicitFlags records whether gateway settings were explicitly configured.
func (c *GatewayConfig) SetExplicitFlags(sendTimeoutSet bool) {
	if c == nil {
		return
	}
	c.sendTimeoutSet = sendTimeoutSet
}

// APIConfig configures the public HTTP API service.
type APIConfig struct {
	// ListenAddr is the HTTP API listen address. An empty value disables the API service.
	ListenAddr string
	// ExternalTCPAddr is the published TCP gateway address returned by route APIs.
	ExternalTCPAddr string
	// ExternalWSAddr is the published WebSocket gateway address returned by route APIs.
	ExternalWSAddr string
	// ExternalWSSAddr is the published secure WebSocket gateway address returned by route APIs.
	ExternalWSSAddr string
}

// ManagerConfig configures the manager HTTP service.
type ManagerConfig struct {
	// ListenAddr is the manager server listen address. An empty value disables
	// the manager service entirely.
	ListenAddr string
	// AuthOn enables JWT login and permission checks for manager routes.
	AuthOn bool
	// JWTSecret is the signing secret used for manager JWT tokens.
	JWTSecret string
	// JWTIssuer is the issuer claim embedded in manager JWT tokens.
	JWTIssuer string
	// JWTExpire is the manager JWT lifetime.
	JWTExpire time.Duration
	// Users defines the static manager users allowed to log in.
	Users []ManagerUserConfig
}

// ManagerUserConfig describes one static manager user.
type ManagerUserConfig struct {
	// Username is the static login identity.
	Username string
	// Password is the static login secret.
	Password string
	// Permissions lists the resource actions granted to the user.
	Permissions []ManagerPermissionConfig
}

// ManagerPermissionConfig binds one resource to allowed actions.
type ManagerPermissionConfig struct {
	// Resource is the manager resource name, such as "cluster.node"; use "*" to grant all manager resources.
	Resource string
	// Actions contains the allowed action codes: r, w, or *.
	Actions []string
}

// ConversationConfig controls conversation projection, sync limits, and batching.
type ConversationConfig struct {
	// ColdThreshold is the inactivity duration after which conversations are treated as cold.
	ColdThreshold time.Duration
	// ActiveScanLimit limits how many active conversations are scanned in one pass.
	ActiveScanLimit int
	// ChannelProbeBatchSize limits channel probes per conversation scan batch.
	ChannelProbeBatchSize int
	// SyncDefaultLimit is the default number of conversations returned by sync APIs.
	SyncDefaultLimit int
	// SyncMaxLimit is the maximum number of conversations returned by sync APIs.
	SyncMaxLimit int
	// FlushInterval is the periodic conversation projector flush interval.
	FlushInterval time.Duration
	// FlushDirtyLimit is the dirty conversation count that triggers an eager flush.
	FlushDirtyLimit int
	// SubscriberPageSize is the page size used when scanning channel subscribers for projection side effects.
	SubscriberPageSize int
	// ActiveHintFlushInterval is the background interval for flushing best-effort active hints.
	ActiveHintFlushInterval time.Duration
	// ActiveHintTTL controls how long unflushed hot active hints remain visible in memory.
	ActiveHintTTL time.Duration
	// ActiveHintBarrierTTL controls how long delete barriers block stale delayed hints in memory.
	ActiveHintBarrierTTL time.Duration
	// ActiveHintMaxHints limits total hot active hints held in memory.
	ActiveHintMaxHints int
	// ActiveHintMaxHintsPerUID limits hot active hints per UID.
	ActiveHintMaxHintsPerUID int
	// ActiveHintFlushBatchSize limits active hints written per flush batch.
	ActiveHintFlushBatchSize int
	// GroupActiveFanoutInterval throttles subscriber fanout for group active hints.
	GroupActiveFanoutInterval time.Duration
	// GroupActiveFanoutMaxSubscribers caps subscriber fanout for large groups; zero disables group fanout.
	GroupActiveFanoutMaxSubscribers int
}

// ApplyDefaultsAndValidate fills default values and validates cross-field constraints.
func (c *Config) ApplyDefaultsAndValidate() error {
	if c == nil {
		return fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}

	if c.Node.ID == 0 {
		return fmt.Errorf("%w: node id must be set", ErrInvalidConfig)
	}
	if c.Node.ID > messageid.MaxNodeID {
		return fmt.Errorf("%w: node id %d exceeds snowflake max %d", ErrInvalidConfig, c.Node.ID, messageid.MaxNodeID)
	}
	if c.Node.DataDir == "" {
		return fmt.Errorf("%w: node data dir must be set", ErrInvalidConfig)
	}
	if c.Cluster.ListenAddr == "" {
		return fmt.Errorf("%w: cluster listen addr must be set", ErrInvalidConfig)
	}
	if len(c.Gateway.Listeners) == 0 {
		return fmt.Errorf("%w: gateway listeners must be set", ErrInvalidConfig)
	}
	if c.Gateway.TokenAuthOn {
		return fmt.Errorf("%w: gateway token auth requires verifier hooks", ErrInvalidConfig)
	}
	if c.Gateway.SendTimeout <= 0 && c.Gateway.sendTimeoutSet {
		return fmt.Errorf("%w: gateway send timeout must be > 0", ErrInvalidConfig)
	}
	if c.Manager.ListenAddr != "" && c.Manager.AuthOn {
		if c.Manager.JWTSecret == "" {
			return fmt.Errorf("%w: manager jwt secret must be set when auth is enabled", ErrInvalidConfig)
		}
		if c.Manager.JWTExpire <= 0 {
			return fmt.Errorf("%w: manager jwt expire must be positive when auth is enabled", ErrInvalidConfig)
		}
		if len(c.Manager.Users) == 0 {
			return fmt.Errorf("%w: manager users must be set when auth is enabled", ErrInvalidConfig)
		}
		for _, user := range c.Manager.Users {
			if user.Username == "" {
				return fmt.Errorf("%w: manager username must be set", ErrInvalidConfig)
			}
			if user.Password == "" {
				return fmt.Errorf("%w: manager password must be set", ErrInvalidConfig)
			}
			for _, permission := range user.Permissions {
				if permission.Resource == "" {
					return fmt.Errorf("%w: manager permission resource must be set", ErrInvalidConfig)
				}
				if len(permission.Actions) == 0 {
					return fmt.Errorf("%w: manager permission action must be set", ErrInvalidConfig)
				}
				for _, action := range permission.Actions {
					if !validManagerPermissionAction(action) {
						return fmt.Errorf("%w: manager permission action %q must be one of r, w or *", ErrInvalidConfig, action)
					}
				}
			}
		}
	}
	if c.ChannelPlane.ReactorCount < 0 {
		return fmt.Errorf("%w: channel plane reactor count must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelPlane.PeerLaneCount < 0 {
		return fmt.Errorf("%w: channel plane peer lane count must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelPlane.PeerBatchMaxWait < 0 {
		return fmt.Errorf("%w: channel plane peer batch max wait must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelPlane.ReactorCount == 0 {
		c.ChannelPlane.ReactorCount = max(4, runtime.GOMAXPROCS(0))
	}
	if c.ChannelPlane.PeerLaneCount == 0 {
		c.ChannelPlane.PeerLaneCount = max(1, min(8, runtime.GOMAXPROCS(0)))
	}
	if c.ChannelPlane.PeerBatchMaxWait == 0 {
		c.ChannelPlane.PeerBatchMaxWait = 500 * time.Microsecond
	}
	if c.ChannelMigration.ScanInterval < 0 {
		return fmt.Errorf("%w: channel migration scan interval must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.ScanInterval == 0 && c.ChannelMigration.scanIntervalSet {
		return fmt.Errorf("%w: channel migration scan interval must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.ScanLimit < 0 {
		return fmt.Errorf("%w: channel migration scan limit must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.ScanLimit == 0 && c.ChannelMigration.scanLimitSet {
		return fmt.Errorf("%w: channel migration scan limit must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.OwnerLeaseTTL < 0 {
		return fmt.Errorf("%w: channel migration owner lease ttl must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.OwnerLeaseTTL == 0 && c.ChannelMigration.ownerLeaseTTLSet {
		return fmt.Errorf("%w: channel migration owner lease ttl must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.RetryBackoff < 0 {
		return fmt.Errorf("%w: channel migration retry backoff must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.RetryBackoff == 0 && c.ChannelMigration.retryBackoffSet {
		return fmt.Errorf("%w: channel migration retry backoff must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.FenceTTL < 0 {
		return fmt.Errorf("%w: channel migration fence ttl must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.FenceTTL == 0 && c.ChannelMigration.fenceTTLSet {
		return fmt.Errorf("%w: channel migration fence ttl must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.LeaderLeaseTTL < 0 {
		return fmt.Errorf("%w: channel migration leader lease ttl must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.LeaderLeaseTTL == 0 && c.ChannelMigration.leaderLeaseTTLSet {
		return fmt.Errorf("%w: channel migration leader lease ttl must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.CatchUpStableWindow < 0 {
		return fmt.Errorf("%w: channel migration catch-up stable window must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.CatchUpStableWindow == 0 && c.ChannelMigration.catchUpStableWindowSet {
		return fmt.Errorf("%w: channel migration catch-up stable window must be positive", ErrInvalidConfig)
	}
	if c.ChannelMigration.MaxConcurrent < 0 {
		return fmt.Errorf("%w: channel migration max concurrent must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.MaxConcurrentPerSource < 0 {
		return fmt.Errorf("%w: channel migration max concurrent per source must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.MaxConcurrentPerTarget < 0 {
		return fmt.Errorf("%w: channel migration max concurrent per target must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.CompletedRetentionTTL < 0 {
		return fmt.Errorf("%w: channel migration completed retention ttl must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.GCLimit < 0 {
		return fmt.Errorf("%w: channel migration gc limit must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMigration.GCLimit == 0 && c.ChannelMigration.gcLimitSet {
		return fmt.Errorf("%w: channel migration gc limit must be positive", ErrInvalidConfig)
	}
	if c.ChannelMessageRetention.TTL < 0 {
		return fmt.Errorf("%w: channel message retention ttl must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMessageRetention.ScanInterval < 0 {
		return fmt.Errorf("%w: channel message retention scan interval must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMessageRetention.ChannelBatchSize < 0 {
		return fmt.Errorf("%w: channel message retention channel batch size must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMessageRetention.MaxTrimMessages < 0 {
		return fmt.Errorf("%w: channel message retention max trim messages must be >= 0", ErrInvalidConfig)
	}
	if c.ChannelMessageRetention.TTL > 0 {
		if c.ChannelMessageRetention.ScanInterval == 0 && c.ChannelMessageRetention.scanIntervalSet {
			return fmt.Errorf("%w: channel message retention scan interval must be positive when ttl is enabled", ErrInvalidConfig)
		}
		if c.ChannelMessageRetention.ChannelBatchSize == 0 && c.ChannelMessageRetention.channelBatchSizeSet {
			return fmt.Errorf("%w: channel message retention channel batch size must be positive when ttl is enabled", ErrInvalidConfig)
		}
		if c.ChannelMessageRetention.MaxTrimMessages == 0 && c.ChannelMessageRetention.maxTrimMessagesSet {
			return fmt.Errorf("%w: channel message retention max trim messages must be positive when ttl is enabled", ErrInvalidConfig)
		}
	}
	if c.Message.PermissionCacheTTL < 0 {
		return fmt.Errorf("%w: message permission cache ttl must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitRate < 0 {
		return fmt.Errorf("%w: message user rate limit rate must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitBurst < 0 {
		return fmt.Errorf("%w: message user rate limit burst must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitBucketShards < 0 {
		return fmt.Errorf("%w: message user rate limit bucket shards must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitIdleTTL < 0 {
		return fmt.Errorf("%w: message user rate limit idle ttl must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitMaxBuckets < 0 {
		return fmt.Errorf("%w: message user rate limit max buckets must be >= 0", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitEnabled && c.Message.UserRateLimitRate == 0 && c.Message.userRateLimitRateSet {
		return fmt.Errorf("%w: message user rate limit rate must be positive when enabled", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitEnabled && c.Message.UserRateLimitBurst == 0 && c.Message.userRateLimitBurstSet {
		return fmt.Errorf("%w: message user rate limit burst must be positive when enabled", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitBucketShards == 0 && c.Message.userRateLimitBucketShardsSet {
		return fmt.Errorf("%w: message user rate limit bucket shards must be positive", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitIdleTTL == 0 && c.Message.userRateLimitIdleTTLSet {
		return fmt.Errorf("%w: message user rate limit idle ttl must be positive", ErrInvalidConfig)
	}
	if c.Message.UserRateLimitMaxBuckets == 0 && c.Message.userRateLimitMaxBucketsSet {
		return fmt.Errorf("%w: message user rate limit max buckets must be positive", ErrInvalidConfig)
	}
	if c.Delivery.PresenceCacheTTL < 0 {
		return fmt.Errorf("%w: delivery presence cache ttl must be >= 0", ErrInvalidConfig)
	}
	if c.Delivery.AckBatchMaxWait < 0 {
		return fmt.Errorf("%w: delivery ack batch max wait must be >= 0", ErrInvalidConfig)
	}
	if c.Delivery.AckBatchMaxSize < 0 {
		return fmt.Errorf("%w: delivery ack batch max size must be >= 0", ErrInvalidConfig)
	}
	if c.Delivery.AckBatchMaxWait > 0 && c.Delivery.AckBatchMaxSize == 0 {
		c.Delivery.AckBatchMaxSize = defaultDeliveryAckBatchMaxSize
	}
	if c.Plugin.Timeout <= 0 {
		if c.Plugin.Timeout < 0 || c.Plugin.timeoutSet {
			return fmt.Errorf("%w: plugin timeout must be positive", ErrInvalidConfig)
		}
		c.Plugin.Timeout = 5 * time.Second
	}
	if !c.Plugin.hotReloadSet {
		c.Plugin.HotReload = true
	}
	if len(c.Cluster.Slots) > 0 {
		return fmt.Errorf("%w: Cluster.Slots is no longer supported; remove static slot peers and use Cluster.InitialSlotCount for managed slots", ErrInvalidConfig)
	}
	if c.Cluster.SlotCount > 0 && c.Cluster.InitialSlotCount > 0 && c.Cluster.SlotCount != c.Cluster.InitialSlotCount {
		return fmt.Errorf("%w: cluster SlotCount=%d must match InitialSlotCount=%d when both are set", ErrInvalidConfig, c.Cluster.SlotCount, c.Cluster.InitialSlotCount)
	}

	if c.Cluster.InitialSlotCount == 0 && c.Cluster.SlotCount > 0 {
		c.Cluster.InitialSlotCount = c.Cluster.SlotCount
	}
	if c.Cluster.SlotCount == 0 && c.Cluster.InitialSlotCount > 0 {
		c.Cluster.SlotCount = c.Cluster.InitialSlotCount
	}
	initialSlotCount := c.Cluster.effectiveInitialSlotCount()
	if initialSlotCount == 0 {
		return fmt.Errorf("%w: cluster initial slot count must be set", ErrInvalidConfig)
	}
	if c.Cluster.HashSlotCount == 0 {
		if initialSlotCount > math.MaxUint16 {
			return fmt.Errorf("%w: cluster initial slot count %d exceeds max hash slot count", ErrInvalidConfig, initialSlotCount)
		}
		c.Cluster.HashSlotCount = uint16(initialSlotCount)
	}
	if uint32(c.Cluster.HashSlotCount) < initialSlotCount {
		return fmt.Errorf("%w: cluster hash slot count %d must be >= initial slot count %d", ErrInvalidConfig, c.Cluster.HashSlotCount, initialSlotCount)
	}
	staticCluster := len(c.Cluster.Nodes) > 0
	dynamicJoin := !staticCluster && len(c.Cluster.Seeds) > 0
	if !staticCluster && !dynamicJoin {
		if c.Cluster.JoinModeEnabled() {
			return fmt.Errorf("%w: cluster seeds must be set for dynamic join mode", ErrInvalidConfig)
		}
		return fmt.Errorf("%w: cluster nodes must be set", ErrInvalidConfig)
	}
	if len(c.Cluster.Seeds) > 0 {
		if err := validateClusterSeeds(c.Cluster.Seeds); err != nil {
			return err
		}
	}
	if dynamicJoin {
		if c.Cluster.AdvertiseAddr == "" {
			return fmt.Errorf("%w: cluster advertise addr must be set for dynamic join mode", ErrInvalidConfig)
		}
		if c.Cluster.JoinToken == "" {
			return fmt.Errorf("%w: cluster join token must be set for dynamic join mode", ErrInvalidConfig)
		}
	}
	if staticCluster && c.Cluster.ControllerReplicaN == 0 {
		c.Cluster.ControllerReplicaN = len(c.Cluster.Nodes)
	}
	if c.Cluster.ControllerReplicaN <= 0 {
		return fmt.Errorf("%w: controller replica count must be positive", ErrInvalidConfig)
	}
	if staticCluster && c.Cluster.ControllerReplicaN > len(c.Cluster.Nodes) {
		return fmt.Errorf("%w: controller replica count %d exceeds cluster nodes %d", ErrInvalidConfig, c.Cluster.ControllerReplicaN, len(c.Cluster.Nodes))
	}
	if staticCluster && c.Cluster.SlotReplicaN == 0 {
		c.Cluster.SlotReplicaN = len(c.Cluster.Nodes)
	}
	if c.Cluster.SlotReplicaN <= 0 {
		return fmt.Errorf("%w: slot replica count must be positive", ErrInvalidConfig)
	}
	if staticCluster && c.Cluster.SlotReplicaN > len(c.Cluster.Nodes) {
		return fmt.Errorf("%w: slot replica count %d exceeds cluster nodes %d", ErrInvalidConfig, c.Cluster.SlotReplicaN, len(c.Cluster.Nodes))
	}
	if !c.Cluster.ControllerLogCompaction.enabledSet {
		c.Cluster.ControllerLogCompaction.Enabled = true
	}
	if c.Cluster.ControllerLogCompaction.Enabled {
		if c.Cluster.ControllerLogCompaction.TriggerEntries == 0 && c.Cluster.ControllerLogCompaction.triggerEntriesSet {
			return fmt.Errorf("%w: controller log compaction trigger entries must be > 0", ErrInvalidConfig)
		}
		if c.Cluster.ControllerLogCompaction.CheckInterval == 0 && c.Cluster.ControllerLogCompaction.checkIntervalSet {
			return fmt.Errorf("%w: controller log compaction check interval must be > 0", ErrInvalidConfig)
		}
		if c.Cluster.ControllerLogCompaction.CheckInterval < 0 {
			return fmt.Errorf("%w: controller log compaction check interval must be > 0", ErrInvalidConfig)
		}
	}
	if c.Cluster.ControllerLogCompaction.TriggerEntries == 0 {
		c.Cluster.ControllerLogCompaction.TriggerEntries = 10000
	}
	if c.Cluster.ControllerLogCompaction.CheckInterval == 0 {
		c.Cluster.ControllerLogCompaction.CheckInterval = 30 * time.Second
	}
	if !c.Cluster.SlotLogCompaction.enabledSet {
		c.Cluster.SlotLogCompaction.Enabled = true
	}
	if c.Cluster.SlotLogCompaction.Enabled {
		if c.Cluster.SlotLogCompaction.TriggerEntries == 0 && c.Cluster.SlotLogCompaction.triggerEntriesSet {
			return fmt.Errorf("%w: slot log compaction trigger entries must be > 0", ErrInvalidConfig)
		}
		if c.Cluster.SlotLogCompaction.CheckInterval == 0 && c.Cluster.SlotLogCompaction.checkIntervalSet {
			return fmt.Errorf("%w: slot log compaction check interval must be > 0", ErrInvalidConfig)
		}
		if c.Cluster.SlotLogCompaction.CheckInterval < 0 {
			return fmt.Errorf("%w: slot log compaction check interval must be > 0", ErrInvalidConfig)
		}
	}
	if c.Cluster.SlotLogCompaction.TriggerEntries == 0 {
		c.Cluster.SlotLogCompaction.TriggerEntries = 10000
	}
	if c.Cluster.SlotLogCompaction.CheckInterval == 0 {
		c.Cluster.SlotLogCompaction.CheckInterval = 30 * time.Second
	}
	if c.Cluster.ChannelBootstrapDefaultMinISR <= 0 {
		if c.Cluster.channelBootstrapDefaultMinISRSet {
			return fmt.Errorf("%w: channel bootstrap default min isr must be positive", ErrInvalidConfig)
		}
		c.Cluster.ChannelBootstrapDefaultMinISR = 2
	}
	if c.Cluster.MaxChannels < 0 {
		return fmt.Errorf("%w: cluster max channels must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.ChannelIdleTimeout < 0 {
		return fmt.Errorf("%w: channel idle timeout must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.ChannelIdleScanInterval < 0 {
		return fmt.Errorf("%w: channel idle scan interval must be >= 0", ErrInvalidConfig)
	}
	switch c.Cluster.ChannelExecutionMode {
	case "", "dedicated", "pooled":
	default:
		return fmt.Errorf("%w: channel execution mode must be dedicated or pooled", ErrInvalidConfig)
	}
	if c.Cluster.ChannelExecutionMode == "" {
		c.Cluster.ChannelExecutionMode = "pooled"
	}
	if c.Cluster.ChannelExecutionWorkers < 0 {
		return fmt.Errorf("%w: channel execution worker count must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.ChannelExecutionQueueSize < 0 {
		return fmt.Errorf("%w: channel execution queue size must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.FollowerReplicationRetryInterval <= 0 && c.Cluster.followerReplicationRetryIntervalSet {
		return fmt.Errorf("%w: follower replication retry interval must be positive", ErrInvalidConfig)
	}
	if c.Cluster.AppendGroupCommitMaxWait <= 0 && c.Cluster.appendGroupCommitMaxWaitSet {
		return fmt.Errorf("%w: append group commit max wait must be positive", ErrInvalidConfig)
	}
	if c.Cluster.AppendGroupCommitMaxRecords <= 0 && c.Cluster.appendGroupCommitMaxRecordsSet {
		return fmt.Errorf("%w: append group commit max records must be positive", ErrInvalidConfig)
	}
	if c.Cluster.AppendGroupCommitMaxBytes <= 0 && c.Cluster.appendGroupCommitMaxBytesSet {
		return fmt.Errorf("%w: append group commit max bytes must be positive", ErrInvalidConfig)
	}
	if c.Cluster.CommitCoordinatorFlushWindow <= 0 && c.Cluster.commitCoordinatorFlushWindowSet {
		return fmt.Errorf("%w: commit coordinator flush window must be positive", ErrInvalidConfig)
	}
	if c.Cluster.CommitCoordinatorMaxRequests < 0 {
		return fmt.Errorf("%w: commit coordinator max requests must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.CommitCoordinatorMaxRecords < 0 {
		return fmt.Errorf("%w: commit coordinator max records must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.CommitCoordinatorMaxBytes < 0 {
		return fmt.Errorf("%w: commit coordinator max bytes must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.LongPollLaneCount <= 0 {
		if c.Cluster.longPollLaneCountSet {
			return fmt.Errorf("%w: long poll lane count must be positive", ErrInvalidConfig)
		}
		c.Cluster.LongPollLaneCount = 8
	}
	if c.Cluster.LongPollMaxWait <= 0 {
		if c.Cluster.longPollMaxWaitSet {
			return fmt.Errorf("%w: long poll max wait must be positive", ErrInvalidConfig)
		}
		c.Cluster.LongPollMaxWait = 200 * time.Millisecond
	}
	if c.Cluster.LongPollMaxBytes <= 0 {
		if c.Cluster.longPollMaxBytesSet {
			return fmt.Errorf("%w: long poll max bytes must be positive", ErrInvalidConfig)
		}
		c.Cluster.LongPollMaxBytes = 64 * 1024
	}
	if c.Cluster.LongPollMaxChannels <= 0 {
		if c.Cluster.longPollMaxChannelsSet {
			return fmt.Errorf("%w: long poll max channels must be positive", ErrInvalidConfig)
		}
		c.Cluster.LongPollMaxChannels = 64
	}
	if c.Cluster.LongPollDataNotifyDelay < 0 {
		return fmt.Errorf("%w: long poll data notify delay must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.ManagedSlotsReadyTimeout < 0 {
		return fmt.Errorf("%w: managed slots ready timeout must be >= 0", ErrInvalidConfig)
	}
	if c.Cluster.ManagedSlotsReadyTimeout == 0 {
		c.Cluster.ManagedSlotsReadyTimeout = defaultManagedSlotsReadyTimeout
	}

	if c.Storage.DBPath == "" {
		c.Storage.DBPath = filepath.Join(c.Node.DataDir, "data")
	}
	if c.Storage.RaftPath == "" {
		c.Storage.RaftPath = filepath.Join(c.Node.DataDir, "raft")
	}
	if c.Storage.ChannelLogPath == "" {
		c.Storage.ChannelLogPath = filepath.Join(c.Node.DataDir, "channellog")
	}
	if c.Storage.ControllerMetaPath == "" {
		c.Storage.ControllerMetaPath = filepath.Join(c.Node.DataDir, "controller-meta")
	}
	if c.Storage.ControllerRaftPath == "" {
		c.Storage.ControllerRaftPath = filepath.Join(c.Node.DataDir, "controller-raft")
	}
	if c.Storage.RaftSnapshotPath == "" {
		c.Storage.RaftSnapshotPath = filepath.Join(c.Node.DataDir, "raft-snapshots")
	}
	if c.Storage.ControllerRaftSnapshotPath == "" {
		c.Storage.ControllerRaftSnapshotPath = filepath.Join(c.Node.DataDir, "controller-raft-snapshots")
	}
	if c.Plugin.Dir == "" {
		c.Plugin.Dir = filepath.Join(c.Node.DataDir, "plugins")
	}
	if c.Plugin.SocketPath == "" {
		c.Plugin.SocketPath = filepath.Join(c.Node.DataDir, "run", "plugin.sock")
	}
	if c.Plugin.SandboxDir == "" {
		c.Plugin.SandboxDir = filepath.Join(c.Node.DataDir, "plugin-sandbox")
	}
	if c.Plugin.StateDir == "" {
		c.Plugin.StateDir = filepath.Join(c.Node.DataDir, "plugin-state")
	}
	if c.Storage.RaftSnapshotChunkSize == 0 {
		if c.Storage.raftSnapshotChunkSizeSet {
			return fmt.Errorf("%w: raft snapshot chunk size must be > 0", ErrInvalidConfig)
		}
		c.Storage.RaftSnapshotChunkSize = 8 << 20
	}
	if c.Storage.RaftSnapshotGCGrace < 0 {
		return fmt.Errorf("%w: raft snapshot gc grace must be non-negative", ErrInvalidConfig)
	}
	if c.Storage.RaftSnapshotGCGrace == 0 && !c.Storage.raftSnapshotGCGraceSet {
		c.Storage.RaftSnapshotGCGrace = 30 * time.Minute
	}

	dbPath, err := normalizeStoragePath(c.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("%w: normalize db path: %v", ErrInvalidConfig, err)
	}
	raftPath, err := normalizeStoragePath(c.Storage.RaftPath)
	if err != nil {
		return fmt.Errorf("%w: normalize raft path: %v", ErrInvalidConfig, err)
	}
	channelLogPath, err := normalizeStoragePath(c.Storage.ChannelLogPath)
	if err != nil {
		return fmt.Errorf("%w: normalize channel log path: %v", ErrInvalidConfig, err)
	}
	controllerMetaPath, err := normalizeStoragePath(c.Storage.ControllerMetaPath)
	if err != nil {
		return fmt.Errorf("%w: normalize controller meta path: %v", ErrInvalidConfig, err)
	}
	controllerRaftPath, err := normalizeStoragePath(c.Storage.ControllerRaftPath)
	if err != nil {
		return fmt.Errorf("%w: normalize controller raft path: %v", ErrInvalidConfig, err)
	}
	raftSnapshotPath, err := normalizeStoragePath(c.Storage.RaftSnapshotPath)
	if err != nil {
		return fmt.Errorf("%w: normalize raft snapshot path: %v", ErrInvalidConfig, err)
	}
	controllerRaftSnapshotPath, err := normalizeStoragePath(c.Storage.ControllerRaftSnapshotPath)
	if err != nil {
		return fmt.Errorf("%w: normalize controller raft snapshot path: %v", ErrInvalidConfig, err)
	}
	pluginDir, err := normalizeStoragePath(c.Plugin.Dir)
	if err != nil {
		return fmt.Errorf("%w: normalize plugin dir: %v", ErrInvalidConfig, err)
	}
	pluginSocketPath, err := normalizeStoragePath(c.Plugin.SocketPath)
	if err != nil {
		return fmt.Errorf("%w: normalize plugin socket path: %v", ErrInvalidConfig, err)
	}
	pluginSandboxDir, err := normalizeStoragePath(c.Plugin.SandboxDir)
	if err != nil {
		return fmt.Errorf("%w: normalize plugin sandbox dir: %v", ErrInvalidConfig, err)
	}
	pluginStateDir, err := normalizeStoragePath(c.Plugin.StateDir)
	if err != nil {
		return fmt.Errorf("%w: normalize plugin state dir: %v", ErrInvalidConfig, err)
	}
	c.Storage.DBPath = dbPath
	c.Storage.RaftPath = raftPath
	c.Storage.ChannelLogPath = channelLogPath
	c.Storage.ControllerMetaPath = controllerMetaPath
	c.Storage.ControllerRaftPath = controllerRaftPath
	c.Storage.RaftSnapshotPath = raftSnapshotPath
	c.Storage.ControllerRaftSnapshotPath = controllerRaftSnapshotPath
	c.Plugin.Dir = pluginDir
	c.Plugin.SocketPath = pluginSocketPath
	c.Plugin.SandboxDir = pluginSandboxDir
	c.Plugin.StateDir = pluginStateDir
	if err := validateStoragePathIsolation([]namedStoragePath{
		{name: "storage db path", path: dbPath},
		{name: "slot raft path", path: raftPath},
		{name: "channel log path", path: channelLogPath},
		{name: "controller meta path", path: controllerMetaPath},
		{name: "controller raft path", path: controllerRaftPath},
		{name: "slot raft snapshot path", path: raftSnapshotPath},
		{name: "controller raft snapshot path", path: controllerRaftSnapshotPath},
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	if c.Gateway.DefaultSession.AsyncSendBatchMaxRecords < 0 {
		return fmt.Errorf("%w: gateway send batch max records must be non-negative", ErrInvalidConfig)
	}
	if c.Gateway.DefaultSession.AsyncSendBatchMaxBytes < 0 {
		return fmt.Errorf("%w: gateway send batch max bytes must be non-negative", ErrInvalidConfig)
	}
	c.Gateway.DefaultSession = gateway.NormalizeSessionOptions(c.Gateway.DefaultSession)
	if c.Gateway.Transport.Gnet.NumEventLoop < 0 {
		return fmt.Errorf("%w: gateway gnet num event loop must be non-negative", ErrInvalidConfig)
	}
	if c.Gateway.Transport.Gnet.ReadBufferCap < 0 {
		return fmt.Errorf("%w: gateway gnet read buffer cap must be non-negative", ErrInvalidConfig)
	}
	if c.Gateway.Transport.Gnet.WriteBufferCap < 0 {
		return fmt.Errorf("%w: gateway gnet write buffer cap must be non-negative", ErrInvalidConfig)
	}
	if c.Gateway.SendTimeout <= 0 {
		c.Gateway.SendTimeout = defaultGatewaySendTimeout
	}
	if c.Bench.APIMaxBatchSize < 0 {
		return fmt.Errorf("%w: bench api max batch size must be positive", ErrInvalidConfig)
	}
	if c.Bench.APIMaxPayloadBytes < 0 {
		return fmt.Errorf("%w: bench api max payload bytes must be positive", ErrInvalidConfig)
	}
	if c.Bench.APIMaxBatchSize == 0 {
		c.Bench.APIMaxBatchSize = defaultBenchAPIMaxBatchSize
	}
	if c.Bench.APIMaxPayloadBytes == 0 {
		c.Bench.APIMaxPayloadBytes = defaultBenchAPIMaxPayloadBytes
	}
	if c.Conversation.ColdThreshold <= 0 {
		c.Conversation.ColdThreshold = 30 * 24 * time.Hour
	}
	if c.Conversation.ActiveScanLimit <= 0 {
		c.Conversation.ActiveScanLimit = 2000
	}
	if c.Conversation.ChannelProbeBatchSize <= 0 {
		c.Conversation.ChannelProbeBatchSize = 512
	}
	if !c.Observability.metricsEnabledSet {
		c.Observability.MetricsEnabled = true
	}
	if !c.Observability.networkEnabledSet {
		c.Observability.NetworkEnabled = true
	}
	if !c.Observability.healthDetailEnabledSet {
		c.Observability.HealthDetailEnabled = true
	}
	if !c.Observability.healthDebugEnabledSet {
		c.Observability.HealthDebugEnabled = false
	}
	if !c.Observability.Diagnostics.enabledSet {
		c.Observability.Diagnostics.Enabled = true
	}
	if c.Observability.Diagnostics.BufferSize <= 0 {
		c.Observability.Diagnostics.BufferSize = 50000
	}
	if c.Observability.Diagnostics.SampleRate < 0 || c.Observability.Diagnostics.SampleRate > 1 {
		return fmt.Errorf("%w: diagnostics sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if c.Observability.Diagnostics.SampleRate == 0 && !c.Observability.Diagnostics.sampleRateSet {
		c.Observability.Diagnostics.SampleRate = 0.01
	}
	if c.Observability.Diagnostics.SlowThreshold <= 0 {
		c.Observability.Diagnostics.SlowThreshold = 500 * time.Millisecond
	}
	if c.Observability.Diagnostics.ErrorSampleRate < 0 || c.Observability.Diagnostics.ErrorSampleRate > 1 {
		return fmt.Errorf("%w: diagnostics error sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if c.Observability.Diagnostics.ErrorSampleRate == 0 && !c.Observability.Diagnostics.errorSampleRateSet {
		c.Observability.Diagnostics.ErrorSampleRate = 1.0
	}
	if !c.Observability.Diagnostics.debugAPIEnabledSet {
		c.Observability.Diagnostics.DebugAPIEnabled = false
	}
	for _, match := range c.Observability.Diagnostics.DebugMatches {
		if match.SampleRate < 0 || match.SampleRate > 1 {
			return fmt.Errorf("%w: diagnostics debug match sample rate must be between 0 and 1", ErrInvalidConfig)
		}
		if match.TTLSeconds < 0 {
			return fmt.Errorf("%w: diagnostics debug match ttl seconds must be >= 0", ErrInvalidConfig)
		}
	}
	if c.Conversation.SyncDefaultLimit <= 0 {
		c.Conversation.SyncDefaultLimit = 200
	}
	if c.Conversation.SyncMaxLimit <= 0 {
		c.Conversation.SyncMaxLimit = 500
	}
	if c.Conversation.SyncDefaultLimit > c.Conversation.SyncMaxLimit {
		c.Conversation.SyncDefaultLimit = c.Conversation.SyncMaxLimit
	}
	if c.Conversation.FlushInterval <= 0 {
		c.Conversation.FlushInterval = 200 * time.Millisecond
	}
	if c.Conversation.FlushDirtyLimit <= 0 {
		c.Conversation.FlushDirtyLimit = 1024
	}
	if c.Conversation.SubscriberPageSize <= 0 {
		c.Conversation.SubscriberPageSize = 512
	}
	if c.Conversation.ActiveHintFlushInterval <= 0 {
		c.Conversation.ActiveHintFlushInterval = 10 * time.Second
	}
	if c.Conversation.ActiveHintTTL <= 0 {
		c.Conversation.ActiveHintTTL = 30 * time.Minute
	}
	if c.Conversation.ActiveHintBarrierTTL <= 0 {
		c.Conversation.ActiveHintBarrierTTL = 30 * time.Minute
	}
	if c.Conversation.ActiveHintMaxHints <= 0 {
		c.Conversation.ActiveHintMaxHints = 100000
	}
	if c.Conversation.ActiveHintMaxHintsPerUID <= 0 {
		c.Conversation.ActiveHintMaxHintsPerUID = 1000
	}
	if c.Conversation.ActiveHintFlushBatchSize <= 0 {
		c.Conversation.ActiveHintFlushBatchSize = 32
	}
	if c.Conversation.GroupActiveFanoutInterval <= 0 {
		c.Conversation.GroupActiveFanoutInterval = 5 * time.Minute
	}
	if c.ChannelMigration.ScanInterval <= 0 {
		c.ChannelMigration.ScanInterval = defaultChannelMigrationScanInterval
	}
	if c.ChannelMigration.ScanLimit <= 0 {
		c.ChannelMigration.ScanLimit = defaultChannelMigrationScanLimit
	}
	if c.ChannelMigration.OwnerLeaseTTL <= 0 {
		c.ChannelMigration.OwnerLeaseTTL = defaultChannelMigrationOwnerLeaseTTL
	}
	if c.ChannelMigration.RetryBackoff <= 0 {
		c.ChannelMigration.RetryBackoff = defaultChannelMigrationRetryBackoff
	}
	if c.ChannelMigration.FenceTTL <= 0 {
		c.ChannelMigration.FenceTTL = defaultChannelMigrationFenceTTL
	}
	if c.ChannelMigration.LeaderLeaseTTL <= 0 {
		c.ChannelMigration.LeaderLeaseTTL = defaultChannelMigrationLeaderLeaseTTL
	}
	if c.ChannelMigration.CatchUpStableWindow <= 0 {
		c.ChannelMigration.CatchUpStableWindow = defaultChannelMigrationCatchUpStableWindow
	}
	if c.ChannelMigration.MaxConcurrent == 0 && !c.ChannelMigration.maxConcurrentSet {
		c.ChannelMigration.MaxConcurrent = defaultChannelMigrationMaxConcurrent
	}
	if c.ChannelMigration.MaxConcurrentPerSource == 0 && !c.ChannelMigration.maxConcurrentPerSourceSet {
		c.ChannelMigration.MaxConcurrentPerSource = defaultChannelMigrationMaxConcurrentPerSource
	}
	if c.ChannelMigration.MaxConcurrentPerTarget == 0 && !c.ChannelMigration.maxConcurrentPerTargetSet {
		c.ChannelMigration.MaxConcurrentPerTarget = defaultChannelMigrationMaxConcurrentPerTarget
	}
	if c.ChannelMigration.CompletedRetentionTTL == 0 && !c.ChannelMigration.completedRetentionTTLSet {
		c.ChannelMigration.CompletedRetentionTTL = defaultChannelMigrationCompletedRetentionTTL
	}
	if c.ChannelMigration.GCLimit <= 0 && !c.ChannelMigration.gcLimitSet {
		c.ChannelMigration.GCLimit = defaultChannelMigrationGCLimit
	}
	if c.ChannelMessageRetention.ScanInterval <= 0 {
		c.ChannelMessageRetention.ScanInterval = defaultChannelMessageRetentionScanInterval
	}
	if c.ChannelMessageRetention.ChannelBatchSize <= 0 {
		c.ChannelMessageRetention.ChannelBatchSize = defaultChannelMessageRetentionChannelBatchSize
	}
	if c.ChannelMessageRetention.MaxTrimMessages <= 0 {
		c.ChannelMessageRetention.MaxTrimMessages = defaultChannelMessageRetentionMaxTrimMessages
	}
	if c.Message.SystemDeviceID == "" {
		c.Message.SystemDeviceID = defaultMessageSystemDeviceID
	}
	if c.Message.UserRateLimitRate == 0 {
		c.Message.UserRateLimitRate = defaultMessageUserRateLimitRate
	}
	if c.Message.UserRateLimitBurst == 0 {
		c.Message.UserRateLimitBurst = defaultMessageUserRateLimitBurst
	}
	if c.Message.UserRateLimitBucketShards == 0 {
		c.Message.UserRateLimitBucketShards = defaultMessageUserRateLimitBucketShards
	}
	if c.Message.UserRateLimitIdleTTL == 0 {
		c.Message.UserRateLimitIdleTTL = defaultMessageUserRateLimitIdleTTL
	}
	if c.Message.UserRateLimitMaxBuckets == 0 {
		c.Message.UserRateLimitMaxBuckets = defaultMessageUserRateLimitMaxBuckets
	}
	if !c.Message.UserRateLimitEnabled && !c.Message.userRateLimitSystemUIDBypassSet {
		c.Message.UserRateLimitSystemUIDBypass = true
	} else if c.Message.UserRateLimitEnabled && !c.Message.userRateLimitSystemUIDBypassSet {
		c.Message.UserRateLimitSystemUIDBypass = true
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Dir == "" {
		c.Log.Dir = "./logs"
	}
	if c.Log.MaxSize <= 0 {
		c.Log.MaxSize = 100
	}
	if c.Log.MaxAge <= 0 {
		c.Log.MaxAge = 30
	}
	if c.Log.MaxBackups <= 0 {
		c.Log.MaxBackups = 10
	}
	if c.Log.Format == "" {
		c.Log.Format = "console"
	}
	if !c.Log.Compress && !c.Log.compressSet {
		c.Log.Compress = true
	}
	if !c.Log.Console && !c.Log.consoleSet {
		c.Log.Console = true
	}
	c.Cluster.DataPlanePoolSize = effectiveDataPlanePoolSize(c.Cluster.PoolSize, c.Cluster.DataPlanePoolSize)
	c.Cluster.DataPlaneRPCTimeout = effectiveDataPlaneRPCTimeout(c.Cluster.DataPlaneRPCTimeout)
	c.Cluster.DataPlaneMaxFetchInflight = effectiveDataPlaneMaxFetchInflight(c.Cluster.PoolSize, c.Cluster.DataPlaneMaxFetchInflight)
	c.Cluster.DataPlaneMaxPendingFetch = effectiveDataPlaneMaxPendingFetch(c.Cluster.PoolSize, c.Cluster.DataPlaneMaxPendingFetch)
	c.Cluster.FollowerReplicationRetryInterval = effectiveFollowerReplicationRetryInterval(c.Cluster.FollowerReplicationRetryInterval)
	c.Cluster.AppendGroupCommitMaxWait = effectiveAppendGroupCommitMaxWait(c.Cluster.AppendGroupCommitMaxWait)
	c.Cluster.AppendGroupCommitMaxRecords = effectiveAppendGroupCommitMaxRecords(c.Cluster.AppendGroupCommitMaxRecords)
	c.Cluster.AppendGroupCommitMaxBytes = effectiveAppendGroupCommitMaxBytes(c.Cluster.AppendGroupCommitMaxBytes)
	c.Cluster.CommitCoordinatorFlushWindow = effectiveCommitCoordinatorFlushWindow(c.Cluster.CommitCoordinatorFlushWindow)

	if staticCluster {
		nodeSet := make(map[uint64]struct{}, len(c.Cluster.Nodes))
		selfNodeFound := false
		for _, node := range c.Cluster.Nodes {
			if node.ID == 0 {
				return fmt.Errorf("%w: cluster node id must be set", ErrInvalidConfig)
			}
			if node.Addr == "" {
				return fmt.Errorf("%w: cluster node addr must be set", ErrInvalidConfig)
			}
			if _, ok := nodeSet[node.ID]; ok {
				return fmt.Errorf("%w: duplicate cluster node id %d", ErrInvalidConfig, node.ID)
			}
			nodeSet[node.ID] = struct{}{}
			if node.ID == c.Node.ID {
				selfNodeFound = true
			}
		}

		if !selfNodeFound {
			return fmt.Errorf("%w: node id %d not found in cluster nodes", ErrInvalidConfig, c.Node.ID)
		}
	}

	return nil
}

func validateClusterSeeds(seeds []string) error {
	seen := make(map[string]struct{}, len(seeds))
	for _, seed := range seeds {
		seed = strings.TrimSpace(seed)
		if seed == "" {
			return fmt.Errorf("%w: cluster seed addr must be set", ErrInvalidConfig)
		}
		if _, ok := seen[seed]; ok {
			return fmt.Errorf("%w: duplicate cluster seed %q", ErrInvalidConfig, seed)
		}
		seen[seed] = struct{}{}
	}
	return nil
}

func normalizeStoragePath(path string) (string, error) {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleanPath); err == nil {
		cleanPath = filepath.Clean(resolved)
	}
	return cleanPath, nil
}

func normalizeStoragePathForOverlap(path string) string {
	if resolved, ok := resolveExistingPathPrefix(path); ok {
		return resolved
	}
	return path
}

func resolveExistingPathPrefix(path string) (string, bool) {
	var suffix []string
	for current := path; ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{filepath.Clean(resolved)}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
	}
}

type namedStoragePath struct {
	name string
	path string
}

func validateStoragePathIsolation(paths []namedStoragePath) error {
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			if storagePathsOverlap(paths[i].path, paths[j].path) ||
				storagePathsOverlap(normalizeStoragePathForOverlap(paths[i].path), normalizeStoragePathForOverlap(paths[j].path)) {
				return fmt.Errorf("%s and %s must not overlap", paths[i].name, paths[j].name)
			}
		}
	}
	return nil
}

func storagePathsOverlap(left, right string) bool {
	leftVolume := filepath.VolumeName(left)
	rightVolume := filepath.VolumeName(right)
	if leftVolume != rightVolume {
		return false
	}
	leftParts := storagePathSegments(strings.TrimPrefix(left, leftVolume))
	rightParts := storagePathSegments(strings.TrimPrefix(right, rightVolume))
	minLen := len(leftParts)
	if len(rightParts) < minLen {
		minLen = len(rightParts)
	}
	for i := 0; i < minLen; i++ {
		if leftParts[i] != rightParts[i] {
			return false
		}
	}
	return true
}

func storagePathSegments(path string) []string {
	cleaned := filepath.Clean(path)
	trimmed := strings.Trim(cleaned, string(filepath.Separator))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}

// ParseRaftSnapshotChunkSize parses a strict byte size with optional B, KiB, MiB, or GiB suffix.
func ParseRaftSnapshotChunkSize(raw string) (uint64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("raft snapshot chunk size is empty")
	}

	multiplier := uint64(1)
	number := value
	for _, unit := range []struct {
		suffix     string
		multiplier uint64
	}{
		{suffix: "KiB", multiplier: 1 << 10},
		{suffix: "MiB", multiplier: 1 << 20},
		{suffix: "GiB", multiplier: 1 << 30},
		{suffix: "B", multiplier: 1},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier = unit.multiplier
			number = strings.TrimSuffix(value, unit.suffix)
			break
		}
	}

	if number == "" || strings.TrimSpace(number) != number {
		return 0, fmt.Errorf("raft snapshot chunk size %q is invalid", raw)
	}
	bytes, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("raft snapshot chunk size %q is invalid: %w", raw, err)
	}
	if bytes == 0 {
		return 0, fmt.Errorf("raft snapshot chunk size must be > 0")
	}
	if bytes > math.MaxUint64/multiplier {
		return 0, fmt.Errorf("raft snapshot chunk size %q overflows uint64", raw)
	}
	return bytes * multiplier, nil
}

func validManagerPermissionAction(action string) bool {
	switch action {
	case "r", "w", "*":
		return true
	default:
		return false
	}
}

func (c ClusterConfig) effectiveInitialSlotCount() uint32 {
	if c.InitialSlotCount > 0 {
		return c.InitialSlotCount
	}
	return c.SlotCount
}

func effectiveFollowerReplicationRetryInterval(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return time.Second
}

func effectiveAppendGroupCommitMaxWait(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return time.Millisecond
}

func effectiveAppendGroupCommitMaxRecords(configured int) int {
	if configured > 0 {
		return configured
	}
	return 64
}

func effectiveAppendGroupCommitMaxBytes(configured int) int {
	if configured > 0 {
		return configured
	}
	return 64 * 1024
}

func effectiveCommitCoordinatorFlushWindow(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return 200 * time.Microsecond
}

func effectiveDataPlaneRPCTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return time.Second
}

const defaultGatewaySendTimeout = 20 * time.Second

const defaultBenchAPIMaxBatchSize = 10000

const defaultBenchAPIMaxPayloadBytes = int64(10 << 20)

const defaultChannelMigrationScanInterval = time.Second

const defaultChannelMigrationScanLimit = 64

const defaultChannelMigrationOwnerLeaseTTL = 30 * time.Second

const defaultChannelMigrationRetryBackoff = time.Minute

const defaultChannelMigrationFenceTTL = time.Minute

const defaultChannelMigrationLeaderLeaseTTL = time.Minute

const defaultChannelMigrationCatchUpStableWindow = time.Second

const defaultChannelMigrationMaxConcurrent = 64

const defaultChannelMigrationMaxConcurrentPerSource = 1

const defaultChannelMigrationMaxConcurrentPerTarget = 1

const defaultChannelMigrationCompletedRetentionTTL = 24 * time.Hour

const defaultChannelMigrationGCLimit = 128

const defaultChannelMessageRetentionScanInterval = time.Hour

const defaultChannelMessageRetentionChannelBatchSize = 128

const defaultChannelMessageRetentionMaxTrimMessages = 10000

const defaultMessageSystemDeviceID = "____device"
const defaultMessageUserRateLimitRate = 100.0
const defaultMessageUserRateLimitBurst = 200
const defaultMessageUserRateLimitBucketShards = 256
const defaultMessageUserRateLimitIdleTTL = 10 * time.Minute
const defaultMessageUserRateLimitMaxBuckets = 100000
