package clusterv2

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	gorutine "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

const minDefaultChannelReactorCount = 4

// Config contains v2-only cluster runtime configuration.
type Config struct {
	// NodeID is the non-zero stable node identity.
	NodeID uint64
	// ListenAddr is the cluster RPC listen address for this node.
	ListenAddr string
	// DataDir is the root directory for clusterv2 data files.
	DataDir string

	// Control contains ControllerV2 adapter configuration.
	Control ControlConfig
	// Join contains dynamic data-node join bootstrap settings.
	Join JoinConfig
	// Slots contains Slot runtime sizing and placement defaults.
	Slots SlotConfig
	// Channel contains ChannelV2 service configuration.
	Channel ChannelConfig
	// ChannelRetention contains node-owned ChannelV2 physical retention cleanup settings.
	ChannelRetention ChannelRetentionConfig
	// HealthReport controls low-frequency node health reporting to ControllerV2.
	HealthReport HealthReportConfig
	// Storage contains node-local storage tuning.
	Storage StorageConfig
	// Transport contains default clusterv2 node-to-node transport tuning and observation hooks.
	Transport TransportConfig
	// Timeouts contains lifecycle timeout budgets.
	Timeouts TimeoutConfig
	// Goroutines is the optional goroutine registry for lifecycle tracking across all cluster subsystems.
	Goroutines *gorutine.Registry
}

// ControlConfig contains ControllerV2 adapter configuration.
type ControlConfig struct {
	// StateDir stores ControllerV2 cluster-state files for this node.
	StateDir string
	// ClusterID is the stable cluster identity used by ControllerV2 state and sync.
	ClusterID string
	// Role declares whether this node is a Controller voter or state mirror.
	Role ControlRole
	// Voters lists Controller voter node IDs and Controller RPC addresses.
	Voters []ControlVoter
	// AllowBootstrap permits this node to initialize an empty ControllerV2 Raft log.
	AllowBootstrap bool
	// RaftObserver receives local ControllerV2 Raft queue metrics.
	RaftObserver ControllerRaftObserver
	// TaskTransitionObserver receives ControllerV2 task edges after applied metadata is persisted.
	TaskTransitionObserver cv2.TaskTransitionObserver
	// SnapshotObserver receives low-frequency locally visible control snapshots.
	SnapshotObserver ControlSnapshotObserver
}

// JoinConfig contains dynamic data-node join bootstrap settings.
type JoinConfig struct {
	// Seeds lists reachable existing node addresses used before membership discovery is available.
	Seeds []string
	// AdvertiseAddr is the stable RPC address this node asks the cluster to store for membership.
	AdvertiseAddr string
	// Token authenticates the join request before the node becomes a durable member.
	Token string
}

// ControllerRaftObserver receives low-cardinality local ControllerV2 Raft runtime metrics.
type ControllerRaftObserver interface {
	SetStepQueueDepth(depth int, capacity int)
	ObserveStepEnqueue(result string, d time.Duration)
}

// ControlSnapshotObserver receives low-frequency control-plane state snapshots.
type ControlSnapshotObserver interface {
	ObserveControlSnapshot(control.Snapshot)
}

// SlotReplicaMoveObserver receives low-cardinality local Slot replica move phase observations.
type SlotReplicaMoveObserver interface {
	ObserveSlotReplicaMovePhase(step, result string, d time.Duration)
}

// ControlRole declares how this node participates in ControllerV2.
type ControlRole string

const (
	// ControlRoleVoter runs ControllerV2 Raft and serves authoritative state.
	ControlRoleVoter ControlRole = "voter"
	// ControlRoleMirror mirrors ControllerV2 state from Controller voters.
	ControlRoleMirror ControlRole = "mirror"
)

// ControlVoter identifies a ControllerV2 Raft voter endpoint.
type ControlVoter struct {
	// NodeID is the stable non-zero node identity of the Controller voter.
	NodeID uint64
	// Addr is the cluster RPC address used to reach this Controller voter.
	Addr string
}

// SlotConfig contains Slot runtime sizing and placement defaults.
type SlotConfig struct {
	// InitialSlotCount is the number of physical Slots created by the initial control snapshot.
	InitialSlotCount uint32
	// HashSlotCount is the number of logical hash slots in the route table.
	HashSlotCount uint16
	// ReplicaCount is the desired replica count for each physical Slot.
	ReplicaCount uint16
	// TickInterval controls how often Slot Raft groups receive local ticks.
	TickInterval time.Duration
	// ElectionTick is the Slot Raft election timeout measured in TickInterval units.
	ElectionTick int
	// HeartbeatTick is the Slot Raft heartbeat interval measured in TickInterval units.
	HeartbeatTick int
	// LogCompaction controls local Slot Raft snapshot compaction.
	LogCompaction multiraft.LogCompactionConfig
	// Observer receives low-cardinality Slot scheduler pressure observations.
	Observer multiraft.SchedulerObserver
	// ReplicaMoveObserver receives low-cardinality Slot replica move phase observations.
	ReplicaMoveObserver SlotReplicaMoveObserver
}

// ChannelConfig contains ChannelV2 service configuration.
type ChannelConfig struct {
	// ReplicaCount is the desired ChannelV2 data replica count for newly created channels. Zero defaults to Slots.ReplicaCount.
	ReplicaCount uint16
	// ReactorCount is the number of ChannelV2 reactor partitions. Zero derives a CPU-aware default.
	ReactorCount int
	// StoreAppendWorkers caps blocking leader append store workers. Zero keeps the ChannelV2 runtime default.
	StoreAppendWorkers int
	// StoreAppendBatchMaxWait overrides store-append worker cross-channel coalescing wait. Zero keeps the ChannelV2 worker default.
	StoreAppendBatchMaxWait time.Duration
	// StoreApplyWorkers caps blocking follower apply store workers. Zero keeps the ChannelV2 runtime default.
	StoreApplyWorkers int
	// RPCWorkers caps blocking ChannelV2 replication RPC workers. Zero keeps the ChannelV2 runtime default.
	RPCWorkers int
	// MailboxSize bounds each ChannelV2 reactor mailbox.
	MailboxSize int
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps unlimited behavior.
	MaxChannels int
	// AppendBatchMaxRecords is the queued ChannelV2 record count that triggers a store append flush. Zero keeps the runtime default.
	AppendBatchMaxRecords int
	// AppendBatchMaxWait is the maximum age of the oldest queued ChannelV2 append before flushing. Zero keeps the runtime default.
	AppendBatchMaxWait time.Duration
	// AppendBatchAdaptiveFlush enables a shorter cold-channel flush delay before the normal append batch window.
	AppendBatchAdaptiveFlush bool
	// AppendBatchColdMaxWait is the cold-channel flush delay used when AppendBatchAdaptiveFlush is enabled. Zero keeps the normal batch window.
	AppendBatchColdMaxWait time.Duration
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero keeps the ChannelV2 runtime default.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window. Zero keeps the ChannelV2 runtime default.
	FollowerRecoveryProbeJitter time.Duration
	// TickInterval controls how often Node-owned loops call ChannelV2 Tick.
	TickInterval time.Duration
	// Observer receives lightweight ChannelV2 reactor and worker metrics.
	Observer reactor.Observer
}

// ChannelRetentionConfig contains node-owned ChannelV2 physical cleanup settings.
type ChannelRetentionConfig struct {
	// PhysicalGCEnabled enables the background local physical retention cleanup loop.
	PhysicalGCEnabled bool
	// ScanInterval controls how often the background cleanup loop scans one catalog page.
	ScanInterval time.Duration
	// ChannelBatchSize caps the number of channel catalog entries processed per cleanup pass.
	ChannelBatchSize int
	// MaxTrimMessages caps message rows deleted per channel apply attempt. Zero uses the default bound.
	MaxTrimMessages int
	// MaxTrimBytes caps payload bytes deleted per channel apply attempt. Zero means unlimited by bytes.
	MaxTrimBytes int
}

// HealthReportConfig controls low-frequency node health reporting to ControllerV2.
type HealthReportConfig struct {
	// Interval controls how often a node reports compact health evidence.
	Interval time.Duration
	// TTL bounds how long the control plane may trust the latest report.
	TTL time.Duration
}

// StorageConfig contains node-local store tuning for clusterv2-owned runtimes.
type StorageConfig struct {
	// CommitFlushWindow is the maximum delay for grouping adjacent channel append commits.
	CommitFlushWindow time.Duration
	// CommitMaxRequests caps logical append requests in one grouped physical commit.
	CommitMaxRequests int
	// CommitMaxRecords caps message records in one grouped physical commit.
	CommitMaxRecords int
	// CommitMaxBytes caps approximate payload bytes in one grouped physical commit.
	CommitMaxBytes int
	// CommitShards routes message DB commit requests across independent coordinators. Zero keeps one coordinator.
	CommitShards int
	// CommitObserver receives message DB group-commit measurements.
	CommitObserver messagedb.CommitCoordinatorObserver
}

// TransportConfig contains default clusterv2 node-to-node transport observation.
type TransportConfig struct {
	// Observer receives transportv2 queue, peer, service, and pending-RPC pressure observations for the default node RPC transport.
	Observer transportv2.Observer
}

// TimeoutConfig contains lifecycle timeout budgets.
type TimeoutConfig struct {
	// Start is the maximum duration allowed for Start readiness gates.
	Start time.Duration
	// Stop is the maximum duration allowed for Stop cleanup.
	Stop time.Duration
}

func (c *Config) applyDefaults() {
	if c.Timeouts.Start == 0 {
		c.Timeouts.Start = 30 * time.Second
	}
	if c.Timeouts.Stop == 0 {
		c.Timeouts.Stop = 5 * time.Second
	}
	if c.Channel.TickInterval == 0 {
		c.Channel.TickInterval = 20 * time.Millisecond
	}
	if c.Channel.ReactorCount == 0 {
		c.Channel.ReactorCount = defaultChannelReactorCount()
	}
	c.applyControlDefaults()
	c.applySlotDefaults()
	if c.Channel.ReplicaCount == 0 {
		c.Channel.ReplicaCount = c.Slots.ReplicaCount
	}
	c.applyChannelRetentionDefaults()
	c.applyHealthReportDefaults()
}

func defaultChannelReactorCount() int {
	return max(minDefaultChannelReactorCount, runtime.GOMAXPROCS(0))
}

func (c *Config) applyControlDefaults() {
	if c.Control.StateDir == "" && c.DataDir != "" {
		c.Control.StateDir = filepath.Join(c.DataDir, "controller")
	}
	if c.seedJoinMode() {
		if c.Control.Role == "" {
			c.Control.Role = ControlRoleMirror
		}
		c.Control.AllowBootstrap = false
		return
	}
	if c.Control.Role == "" {
		c.Control.Role = ControlRoleVoter
	}
	implicitSingleNode := len(c.Control.Voters) == 0
	if implicitSingleNode && c.NodeID != 0 && c.ListenAddr != "" {
		c.Control.Voters = []ControlVoter{{NodeID: c.NodeID, Addr: c.ListenAddr}}
		if c.Control.ClusterID == "" {
			c.Control.ClusterID = fmt.Sprintf("wk-clusterv2-single-node-%d", c.NodeID)
		}
		c.Control.AllowBootstrap = true
	}
}

func (c Config) seedJoinMode() bool {
	return c.Join.Seeds != nil || c.Join.AdvertiseAddr != ""
}

func (c *Config) applySlotDefaults() {
	if c.Slots.InitialSlotCount == 0 {
		c.Slots.InitialSlotCount = 1
	}
	if c.Slots.HashSlotCount == 0 {
		c.Slots.HashSlotCount = 16
	}
	if c.Slots.ReplicaCount == 0 {
		c.Slots.ReplicaCount = uint16(len(c.Control.Voters))
		if c.Slots.ReplicaCount == 0 {
			c.Slots.ReplicaCount = 1
		}
	}
	if c.Slots.TickInterval == 0 {
		c.Slots.TickInterval = defaultSlotTickInterval
	}
	if c.Slots.ElectionTick == 0 {
		c.Slots.ElectionTick = defaultSlotElectionTick
	}
	if c.Slots.HeartbeatTick == 0 {
		c.Slots.HeartbeatTick = defaultSlotHeartbeatTick
	}
	c.Slots.LogCompaction = multiraft.NormalizeLogCompactionConfig(c.Slots.LogCompaction)
}

func (c *Config) applyChannelRetentionDefaults() {
	if c.ChannelRetention.ScanInterval == 0 {
		c.ChannelRetention.ScanInterval = time.Minute
	}
	if c.ChannelRetention.ChannelBatchSize == 0 {
		c.ChannelRetention.ChannelBatchSize = 128
	}
	if c.ChannelRetention.MaxTrimMessages == 0 {
		c.ChannelRetention.MaxTrimMessages = 1000
	}
}

func (c *Config) applyHealthReportDefaults() {
	if c.HealthReport.Interval == 0 {
		c.HealthReport.Interval = 5 * time.Second
	}
	if c.HealthReport.TTL == 0 {
		c.HealthReport.TTL = 30 * time.Second
	}
}

func (c Config) validate() error {
	if c.NodeID == 0 || c.ListenAddr == "" || c.DataDir == "" {
		return ErrInvalidConfig
	}
	if c.Channel.TickInterval < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.ReactorCount < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.StoreAppendWorkers < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.StoreAppendBatchMaxWait < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.StoreApplyWorkers < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.RPCWorkers < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.MailboxSize < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.MaxChannels < 0 {
		return ErrInvalidConfig
	}
	if c.Storage.CommitShards < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.AppendBatchMaxRecords < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.AppendBatchMaxWait < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.AppendBatchColdMaxWait < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.ReplicaCount == 0 {
		return ErrInvalidConfig
	}
	if c.Channel.FollowerRecoveryProbeInterval < 0 {
		return ErrInvalidConfig
	}
	if c.Channel.FollowerRecoveryProbeJitter < 0 {
		return ErrInvalidConfig
	}
	if c.ChannelRetention.ScanInterval < 0 {
		return ErrInvalidConfig
	}
	if c.ChannelRetention.ChannelBatchSize < 0 {
		return ErrInvalidConfig
	}
	if c.ChannelRetention.MaxTrimMessages < 0 {
		return ErrInvalidConfig
	}
	if c.ChannelRetention.MaxTrimBytes < 0 {
		return ErrInvalidConfig
	}
	if c.HealthReport.Interval <= 0 || c.HealthReport.TTL <= 0 || c.HealthReport.TTL < c.HealthReport.Interval {
		return ErrInvalidConfig
	}
	if err := c.validateControl(); err != nil {
		return err
	}
	if err := c.validateSlots(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateControl() error {
	if c.Control.StateDir == "" || c.Control.ClusterID == "" {
		return ErrInvalidConfig
	}
	if c.seedJoinMode() {
		if c.Control.Role != ControlRoleMirror || c.Control.AllowBootstrap || len(c.Control.Voters) != 0 {
			return ErrInvalidConfig
		}
		if len(c.Join.Seeds) == 0 || strings.TrimSpace(c.Join.AdvertiseAddr) == "" || strings.TrimSpace(c.Join.Token) == "" {
			return ErrInvalidConfig
		}
		for _, seed := range c.Join.Seeds {
			if strings.TrimSpace(seed) == "" {
				return ErrInvalidConfig
			}
		}
		return nil
	}
	if c.Control.Role != ControlRoleVoter && c.Control.Role != ControlRoleMirror {
		return ErrInvalidConfig
	}
	if len(c.Control.Voters) == 0 {
		return ErrInvalidConfig
	}
	seen := make(map[uint64]struct{}, len(c.Control.Voters))
	localFound := false
	for _, voter := range c.Control.Voters {
		if voter.NodeID == 0 || voter.Addr == "" {
			return ErrInvalidConfig
		}
		if _, ok := seen[voter.NodeID]; ok {
			return ErrInvalidConfig
		}
		seen[voter.NodeID] = struct{}{}
		if voter.NodeID == c.NodeID {
			localFound = true
		}
	}
	if c.Control.Role == ControlRoleVoter && !localFound {
		return ErrInvalidConfig
	}
	return nil
}

func (c Config) validateSlots() error {
	if c.Slots.InitialSlotCount == 0 || c.Slots.HashSlotCount == 0 || c.Slots.ReplicaCount == 0 {
		return ErrInvalidConfig
	}
	if !c.seedJoinMode() && int(c.Slots.ReplicaCount) > len(c.Control.Voters) {
		return ErrInvalidConfig
	}
	if c.Slots.TickInterval <= 0 || c.Slots.ElectionTick <= 0 || c.Slots.HeartbeatTick <= 0 || c.Slots.ElectionTick <= c.Slots.HeartbeatTick {
		return ErrInvalidConfig
	}
	if err := multiraft.ValidateLogCompactionConfig(c.Slots.LogCompaction); err != nil {
		return err
	}
	return nil
}
