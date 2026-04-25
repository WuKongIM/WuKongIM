package app

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/runtime/messageid"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

type Config struct {
	Node          NodeConfig
	Storage       StorageConfig
	Cluster       ClusterConfig
	API           APIConfig
	Manager       ManagerConfig
	Gateway       GatewayConfig
	Conversation  ConversationConfig
	Observability ObservabilityConfig
	Log           LogConfig
}

type ObservabilityConfig struct {
	MetricsEnabled      bool
	HealthDetailEnabled bool
	HealthDebugEnabled  bool

	metricsEnabledSet      bool
	healthDetailEnabledSet bool
	healthDebugEnabledSet  bool
}

func (c *ObservabilityConfig) SetExplicitFlags(metricsSet, detailSet, debugSet bool) {
	if c == nil {
		return
	}
	c.metricsEnabledSet = metricsSet
	c.healthDetailEnabledSet = detailSet
	c.healthDebugEnabledSet = debugSet
}

type LogConfig struct {
	Level      string
	Dir        string
	MaxSize    int
	MaxAge     int
	MaxBackups int
	Compress   bool
	Console    bool
	Format     string

	compressSet bool
	consoleSet  bool
}

func (c *LogConfig) SetExplicitFlags(compressSet, consoleSet bool) {
	if c == nil {
		return
	}
	c.compressSet = compressSet
	c.consoleSet = consoleSet
}

type NodeConfig struct {
	ID      uint64
	Name    string
	DataDir string
}

type StorageConfig struct {
	DBPath             string
	RaftPath           string
	ChannelLogPath     string
	ControllerMetaPath string
	ControllerRaftPath string
}

type ClusterConfig struct {
	ListenAddr                       string
	SlotCount                        uint32
	HashSlotCount                    uint16
	InitialSlotCount                 uint32
	ChannelBootstrapDefaultMinISR    int
	LongPollLaneCount                int
	LongPollMaxWait                  time.Duration
	LongPollMaxBytes                 int
	LongPollMaxChannels              int
	FollowerReplicationRetryInterval time.Duration
	AppendGroupCommitMaxWait         time.Duration
	AppendGroupCommitMaxRecords      int
	AppendGroupCommitMaxBytes        int
	Nodes                            []NodeConfigRef
	Slots                            []SlotConfig
	ControllerReplicaN               int
	SlotReplicaN                     int
	ForwardTimeout                   time.Duration
	PoolSize                         int
	DataPlanePoolSize                int
	TickInterval                     time.Duration
	RaftWorkers                      int
	ElectionTick                     int
	HeartbeatTick                    int
	DialTimeout                      time.Duration
	Timeouts                         raftcluster.Timeouts
	DataPlaneRPCTimeout              time.Duration
	DataPlaneMaxFetchInflight        int
	DataPlaneMaxPendingFetch         int

	channelBootstrapDefaultMinISRSet    bool
	followerReplicationRetryIntervalSet bool
	appendGroupCommitMaxWaitSet         bool
	appendGroupCommitMaxRecordsSet      bool
	appendGroupCommitMaxBytesSet        bool
	longPollLaneCountSet                bool
	longPollMaxWaitSet                  bool
	longPollMaxBytesSet                 bool
	longPollMaxChannelsSet              bool
}

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

func (c *ClusterConfig) SetReplicationExplicitFlags(longPollLaneCountSet, longPollMaxWaitSet, longPollMaxBytesSet, longPollMaxChannelsSet bool) {
	if c == nil {
		return
	}
	c.longPollLaneCountSet = longPollLaneCountSet
	c.longPollMaxWaitSet = longPollMaxWaitSet
	c.longPollMaxBytesSet = longPollMaxBytesSet
	c.longPollMaxChannelsSet = longPollMaxChannelsSet
}

type NodeConfigRef struct {
	ID   uint64
	Addr string
}

type SlotConfig struct {
	ID    uint32
	Peers []uint64
}

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

type GatewayConfig struct {
	TokenAuthOn    bool
	SendTimeout    time.Duration
	DefaultSession gateway.SessionOptions
	Listeners      []gateway.ListenerOptions

	sendTimeoutSet bool
}

func (c *GatewayConfig) SetExplicitFlags(sendTimeoutSet bool) {
	if c == nil {
		return
	}
	c.sendTimeoutSet = sendTimeoutSet
}

type APIConfig struct {
	ListenAddr      string
	ExternalTCPAddr string
	ExternalWSAddr  string
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

type ConversationConfig struct {
	ColdThreshold         time.Duration
	ActiveScanLimit       int
	ChannelProbeBatchSize int
	SyncDefaultLimit      int
	SyncMaxLimit          int
	FlushInterval         time.Duration
	FlushDirtyLimit       int
	SubscriberPageSize    int
}

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
	if len(c.Cluster.Nodes) == 0 {
		return fmt.Errorf("%w: cluster nodes must be set", ErrInvalidConfig)
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
	if c.Cluster.ControllerReplicaN == 0 {
		c.Cluster.ControllerReplicaN = len(c.Cluster.Nodes)
	}
	if c.Cluster.ControllerReplicaN <= 0 {
		return fmt.Errorf("%w: controller replica count must be positive", ErrInvalidConfig)
	}
	if c.Cluster.ControllerReplicaN > len(c.Cluster.Nodes) {
		return fmt.Errorf("%w: controller replica count %d exceeds cluster nodes %d", ErrInvalidConfig, c.Cluster.ControllerReplicaN, len(c.Cluster.Nodes))
	}
	if c.Cluster.SlotReplicaN == 0 {
		c.Cluster.SlotReplicaN = len(c.Cluster.Nodes)
	}
	if c.Cluster.SlotReplicaN <= 0 {
		return fmt.Errorf("%w: slot replica count must be positive", ErrInvalidConfig)
	}
	if c.Cluster.SlotReplicaN > len(c.Cluster.Nodes) {
		return fmt.Errorf("%w: slot replica count %d exceeds cluster nodes %d", ErrInvalidConfig, c.Cluster.SlotReplicaN, len(c.Cluster.Nodes))
	}
	if c.Cluster.ChannelBootstrapDefaultMinISR <= 0 {
		if c.Cluster.channelBootstrapDefaultMinISRSet {
			return fmt.Errorf("%w: channel bootstrap default min isr must be positive", ErrInvalidConfig)
		}
		c.Cluster.ChannelBootstrapDefaultMinISR = 2
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
	if dbPath == raftPath {
		return fmt.Errorf("%w: storage db path and raft path must differ", ErrInvalidConfig)
	}
	if dbPath == channelLogPath || raftPath == channelLogPath {
		return fmt.Errorf("%w: channel log path must differ from db and raft paths", ErrInvalidConfig)
	}
	if controllerMetaPath == dbPath || controllerMetaPath == raftPath || controllerMetaPath == channelLogPath {
		return fmt.Errorf("%w: controller meta path must differ from db, raft and channel log paths", ErrInvalidConfig)
	}
	if controllerRaftPath == dbPath || controllerRaftPath == raftPath || controllerRaftPath == channelLogPath {
		return fmt.Errorf("%w: controller raft path must differ from db, raft and channel log paths", ErrInvalidConfig)
	}
	if controllerMetaPath == controllerRaftPath {
		return fmt.Errorf("%w: controller meta path and controller raft path must differ", ErrInvalidConfig)
	}

	c.Gateway.DefaultSession = gateway.NormalizeSessionOptions(c.Gateway.DefaultSession)
	if c.Gateway.SendTimeout <= 0 {
		c.Gateway.SendTimeout = defaultGatewaySendTimeout
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
	if !c.Observability.healthDetailEnabledSet {
		c.Observability.HealthDetailEnabled = true
	}
	if !c.Observability.healthDebugEnabledSet {
		c.Observability.HealthDebugEnabled = false
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

	return nil
}

func normalizeStoragePath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
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

func effectiveDataPlaneRPCTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return time.Second
}

const defaultGatewaySendTimeout = 20 * time.Second
