package cluster

import (
	"fmt"
	"math"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultForwardTimeout               = 5 * time.Second
	defaultPoolSize                     = 4
	defaultTickInterval                 = 100 * time.Millisecond
	defaultRaftWorkers                  = 2
	defaultElectionTick                 = 10
	defaultHeartbeatTick                = 1
	defaultDialTimeout                  = 5 * time.Second
	defaultControllerObservationTimeout = 200 * time.Millisecond
	defaultControllerRequestTimeout     = 2 * time.Second
	defaultControllerLeaderWaitTimeout  = 10 * time.Second
	defaultForwardRetryBudget           = 300 * time.Millisecond
	defaultManagedSlotLeaderWaitTimeout = 5 * time.Second
	defaultManagedSlotCatchUpTimeout    = 5 * time.Second
	defaultManagedSlotLeaderMoveTimeout = 5 * time.Second
	defaultConfigChangeRetryBudget      = 300 * time.Millisecond
	defaultLeaderTransferRetryBudget    = 300 * time.Millisecond
	defaultObservationHeartbeatInterval = 2 * time.Second
	defaultObservationRuntimeScan       = 1 * time.Second
	defaultObservationFlushDebounce     = 200 * time.Millisecond
	defaultObservationFullSyncInterval  = 60 * time.Second
	defaultObservationSlowSyncInterval  = 2 * time.Second
	defaultPlannerSafetyInterval        = 1 * time.Second
	defaultPlannerWakeDebounce          = 100 * time.Millisecond
)

type Config struct {
	NodeID                       multiraft.NodeID
	ListenAddr                   string
	SlotCount                    uint32
	HashSlotCount                uint16
	InitialSlotCount             uint32
	ControllerMetaPath           string
	ControllerRaftPath           string
	ControllerReplicaN           int
	SlotReplicaN                 int
	NewStorage                   func(slotID multiraft.SlotID) (multiraft.Storage, error)
	NewStateMachine              func(slotID multiraft.SlotID) (multiraft.StateMachine, error)
	NewStateMachineWithHashSlots func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error)
	Nodes                        []NodeConfig
	Slots                        []SlotConfig
	ForwardTimeout               time.Duration
	PoolSize                     int
	TickInterval                 time.Duration
	RaftWorkers                  int
	ElectionTick                 int
	HeartbeatTick                int
	DialTimeout                  time.Duration
	Timeouts                     Timeouts
	Observer                     ObserverHooks
	TransportObserver            transport.ObserverHooks
	Logger                       wklog.Logger
}

type Timeouts struct {
	ControllerObservation              time.Duration
	ControllerRequest                  time.Duration
	ControllerLeaderWait               time.Duration
	ForwardRetryBudget                 time.Duration
	ManagedSlotLeaderWait              time.Duration
	ManagedSlotCatchUp                 time.Duration
	ManagedSlotLeaderMove              time.Duration
	ConfigChangeRetryBudget            time.Duration
	LeaderTransferRetryBudget          time.Duration
	ObservationHeartbeatInterval       time.Duration
	ObservationRuntimeScanInterval     time.Duration
	ObservationRuntimeFlushDebounce    time.Duration
	ObservationRuntimeFullSyncInterval time.Duration
	// ObservationSlowSyncInterval controls the fallback full-scope observation sync cadence when hint wakes are lost.
	ObservationSlowSyncInterval time.Duration
	// PlannerSafetyInterval controls the minimum planner reevaluation cadence even when no dirty wake is queued.
	PlannerSafetyInterval time.Duration
	// PlannerWakeDebounce coalesces bursts of controller dirty signals before waking the planner loop.
	PlannerWakeDebounce time.Duration
}

type ObserverHooks struct {
	OnControllerCall     func(kind string, dur time.Duration, err error)
	OnControllerDecision func(slotID uint32, kind string, dur time.Duration)
	OnReconcileStep      func(slotID uint32, step string, dur time.Duration, err error)
	OnForwardPropose     func(slotID uint32, attempts int, dur time.Duration, err error)
	OnSlotEnsure         func(slotID uint32, action string, err error)
	OnTaskResult         func(slotID uint32, kind string, result string)
	OnHashSlotMigration  func(hashSlot uint16, source, target multiraft.SlotID, result string)
	OnLeaderChange       func(slotID uint32, from, to multiraft.NodeID)
	OnNodeStatusChange   func(nodeID uint64, from, to controllermeta.NodeStatus)
}

type NodeConfig struct {
	NodeID multiraft.NodeID
	Addr   string
}

type SlotConfig struct {
	SlotID multiraft.SlotID
	Peers  []multiraft.NodeID
}

func (c *Config) validate() error {
	if c.NodeID == 0 {
		return fmt.Errorf("%w: NodeID must be > 0", ErrInvalidConfig)
	}
	if c.ListenAddr == "" {
		return fmt.Errorf("%w: ListenAddr must be set", ErrInvalidConfig)
	}
	if c.NewStorage == nil {
		return fmt.Errorf("%w: NewStorage must be set", ErrInvalidConfig)
	}
	if c.NewStateMachine == nil && c.NewStateMachineWithHashSlots == nil {
		return fmt.Errorf("%w: NewStateMachine must be set", ErrInvalidConfig)
	}
	if c.SlotCount > 0 && c.InitialSlotCount > 0 && c.SlotCount != c.InitialSlotCount {
		return fmt.Errorf("%w: SlotCount=%d must match InitialSlotCount=%d when both are set", ErrInvalidConfig, c.SlotCount, c.InitialSlotCount)
	}

	initialSlotCount := c.effectiveInitialSlotCount()
	if initialSlotCount == 0 {
		return fmt.Errorf("%w: InitialSlotCount must be > 0", ErrInvalidConfig)
	}
	hashSlotCount := c.effectiveHashSlotCount()
	if hashSlotCount == 0 {
		return fmt.Errorf("%w: HashSlotCount must be > 0", ErrInvalidConfig)
	}
	if initialSlotCount > math.MaxUint16 {
		return fmt.Errorf("%w: InitialSlotCount=%d exceeds max supported hash slot count", ErrInvalidConfig, initialSlotCount)
	}
	if uint32(hashSlotCount) < initialSlotCount {
		return fmt.Errorf("%w: HashSlotCount=%d must be >= InitialSlotCount=%d", ErrInvalidConfig, hashSlotCount, initialSlotCount)
	}
	if hashSlotCount > 1 && c.NewStateMachineWithHashSlots == nil {
		return fmt.Errorf("%w: NewStateMachineWithHashSlots must be set when HashSlotCount=%d", ErrInvalidConfig, hashSlotCount)
	}
	if c.ControllerReplicaN <= 0 {
		return fmt.Errorf("%w: ControllerReplicaN must be > 0", ErrInvalidConfig)
	}
	if c.SlotReplicaN <= 0 {
		return fmt.Errorf("%w: SlotReplicaN must be > 0", ErrInvalidConfig)
	}
	if c.ControllerReplicaN > len(c.Nodes) {
		return fmt.Errorf("%w: ControllerReplicaN=%d exceeds Nodes=%d", ErrInvalidConfig, c.ControllerReplicaN, len(c.Nodes))
	}
	if c.SlotReplicaN > len(c.Nodes) {
		return fmt.Errorf("%w: SlotReplicaN=%d exceeds Nodes=%d", ErrInvalidConfig, c.SlotReplicaN, len(c.Nodes))
	}
	if (c.ControllerMetaPath == "") != (c.ControllerRaftPath == "") {
		return fmt.Errorf("%w: ControllerMetaPath and ControllerRaftPath must be set together", ErrInvalidConfig)
	}

	nodeSet := make(map[multiraft.NodeID]bool, len(c.Nodes))
	selfFound := false
	for _, n := range c.Nodes {
		if nodeSet[n.NodeID] {
			return fmt.Errorf("%w: duplicate NodeID %d in Nodes", ErrInvalidConfig, n.NodeID)
		}
		nodeSet[n.NodeID] = true
		if n.NodeID == c.NodeID {
			selfFound = true
		}
	}
	if !selfFound {
		return fmt.Errorf("%w: NodeID %d not found in Nodes", ErrInvalidConfig, c.NodeID)
	}

	slotSet := make(map[multiraft.SlotID]bool, len(c.Slots))
	slotSelfFound := false
	for _, g := range c.Slots {
		if slotSet[g.SlotID] {
			return fmt.Errorf("%w: duplicate SlotID %d", ErrInvalidConfig, g.SlotID)
		}
		if g.SlotID == 0 || uint32(g.SlotID) > initialSlotCount {
			return fmt.Errorf("%w: SlotID %d exceeds InitialSlotCount=%d", ErrInvalidConfig, g.SlotID, initialSlotCount)
		}
		slotSet[g.SlotID] = true
		for _, peer := range g.Peers {
			if !nodeSet[peer] {
				return fmt.Errorf("%w: peer %d in slot %d not found in Nodes", ErrInvalidConfig, peer, g.SlotID)
			}
			if peer == c.NodeID {
				slotSelfFound = true
			}
		}
	}
	if len(c.Slots) > 0 && !slotSelfFound {
		return fmt.Errorf("%w: NodeID %d not found as peer in any slot", ErrInvalidConfig, c.NodeID)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.InitialSlotCount == 0 {
		switch {
		case c.SlotCount > 0:
			c.InitialSlotCount = c.SlotCount
		case len(c.Slots) > 0:
			c.InitialSlotCount = uint32(len(c.Slots))
		}
	}
	if c.SlotCount == 0 && c.InitialSlotCount > 0 {
		c.SlotCount = c.InitialSlotCount
	}
	if c.HashSlotCount == 0 && c.InitialSlotCount > 0 && c.InitialSlotCount <= math.MaxUint16 {
		c.HashSlotCount = uint16(c.InitialSlotCount)
	}
	if c.ControllerReplicaN == 0 {
		c.ControllerReplicaN = len(c.Nodes)
	}
	if c.SlotReplicaN == 0 {
		c.SlotReplicaN = len(c.Nodes)
	}
	if c.ForwardTimeout == 0 {
		c.ForwardTimeout = defaultForwardTimeout
	}
	if c.PoolSize == 0 {
		c.PoolSize = defaultPoolSize
	}
	if c.TickInterval == 0 {
		c.TickInterval = defaultTickInterval
	}
	if c.RaftWorkers == 0 {
		c.RaftWorkers = defaultRaftWorkers
	}
	if c.ElectionTick == 0 {
		c.ElectionTick = defaultElectionTick
	}
	if c.HeartbeatTick == 0 {
		c.HeartbeatTick = defaultHeartbeatTick
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = defaultDialTimeout
	}
	c.Timeouts.applyDefaults()
}

func (t *Timeouts) applyDefaults() {
	if t == nil {
		return
	}
	if t.ControllerObservation == 0 {
		t.ControllerObservation = defaultControllerObservationTimeout
	}
	if t.ControllerRequest == 0 {
		t.ControllerRequest = defaultControllerRequestTimeout
	}
	if t.ControllerLeaderWait == 0 {
		t.ControllerLeaderWait = defaultControllerLeaderWaitTimeout
	}
	if t.ForwardRetryBudget == 0 {
		t.ForwardRetryBudget = defaultForwardRetryBudget
	}
	if t.ManagedSlotLeaderWait == 0 {
		t.ManagedSlotLeaderWait = defaultManagedSlotLeaderWaitTimeout
	}
	if t.ManagedSlotCatchUp == 0 {
		t.ManagedSlotCatchUp = defaultManagedSlotCatchUpTimeout
	}
	if t.ManagedSlotLeaderMove == 0 {
		t.ManagedSlotLeaderMove = defaultManagedSlotLeaderMoveTimeout
	}
	if t.ConfigChangeRetryBudget == 0 {
		t.ConfigChangeRetryBudget = defaultConfigChangeRetryBudget
	}
	if t.LeaderTransferRetryBudget == 0 {
		t.LeaderTransferRetryBudget = defaultLeaderTransferRetryBudget
	}
	if t.ObservationHeartbeatInterval == 0 {
		t.ObservationHeartbeatInterval = defaultObservationHeartbeatInterval
	}
	if t.ObservationRuntimeScanInterval == 0 {
		t.ObservationRuntimeScanInterval = defaultObservationRuntimeScan
	}
	if t.ObservationRuntimeFlushDebounce == 0 {
		t.ObservationRuntimeFlushDebounce = defaultObservationFlushDebounce
	}
	if t.ObservationRuntimeFullSyncInterval == 0 {
		t.ObservationRuntimeFullSyncInterval = defaultObservationFullSyncInterval
	}
	if t.ObservationSlowSyncInterval == 0 {
		t.ObservationSlowSyncInterval = defaultObservationSlowSyncInterval
	}
	if t.PlannerSafetyInterval == 0 {
		t.PlannerSafetyInterval = defaultPlannerSafetyInterval
	}
	if t.PlannerWakeDebounce == 0 {
		t.PlannerWakeDebounce = defaultPlannerWakeDebounce
	}
}

func (c Config) DerivedControllerNodes() []NodeConfig {
	nodes := append([]NodeConfig(nil), c.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	if c.ControllerReplicaN > 0 && c.ControllerReplicaN < len(nodes) {
		nodes = nodes[:c.ControllerReplicaN]
	}
	return nodes
}

func (c Config) HasLocalControllerPeer() bool {
	for _, node := range c.DerivedControllerNodes() {
		if node.NodeID == c.NodeID {
			return true
		}
	}
	return false
}

func (c Config) ControllerEnabled() bool {
	return c.ControllerMetaPath != "" && c.ControllerRaftPath != ""
}

func (c Config) effectiveInitialSlotCount() uint32 {
	if c.InitialSlotCount > 0 {
		return c.InitialSlotCount
	}
	return c.SlotCount
}

func (c Config) effectiveHashSlotCount() uint16 {
	if c.HashSlotCount > 0 {
		return c.HashSlotCount
	}
	initialSlotCount := c.effectiveInitialSlotCount()
	if initialSlotCount > math.MaxUint16 {
		return 0
	}
	return uint16(initialSlotCount)
}
