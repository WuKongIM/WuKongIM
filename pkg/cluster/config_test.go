package cluster

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestConfigValidate_Valid(t *testing.T) {
	cfg := validTestConfig()
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidate_SlotCountZero(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 0
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_RejectsHashSlotCountBelowInitialSlotCount(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 0
	cfg.InitialSlotCount = 4
	cfg.HashSlotCount = 3
	cfg.Slots = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_RejectsMismatchedLegacyAndInitialSlotCount(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 2
	cfg.InitialSlotCount = 3
	cfg.HashSlotCount = 3
	cfg.Slots = nil
	cfg.NewStateMachineWithHashSlots = func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
		return nil, nil
	}
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingHashSlotAwareStateMachineFactory(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 0
	cfg.InitialSlotCount = 2
	cfg.HashSlotCount = 2
	cfg.Slots = nil
	cfg.NewStateMachineWithHashSlots = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_AllowsExplicitHashAndInitialSlotCounts(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 0
	cfg.InitialSlotCount = 2
	cfg.HashSlotCount = 8
	cfg.Slots = nil
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidate_AllowsLegacySlotsBelowSlotCount(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 5
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidate_PeerNotInNodes(t *testing.T) {
	cfg := validTestConfig()
	cfg.Slots[0].Peers = append(cfg.Slots[0].Peers, 99)
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_SelfNotPeer(t *testing.T) {
	cfg := validTestConfig()
	cfg.NodeID = 99
	cfg.Nodes = append(cfg.Nodes, NodeConfig{NodeID: 99, Addr: ":9999"})
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigApplyDefaults(t *testing.T) {
	cfg := validTestConfig()
	cfg.applyDefaults()
	if cfg.ForwardTimeout != defaultForwardTimeout {
		t.Fatalf("expected default ForwardTimeout")
	}
	if cfg.PoolSize != defaultPoolSize {
		t.Fatalf("expected default PoolSize")
	}
	if cfg.TickInterval != defaultTickInterval {
		t.Fatalf("expected default TickInterval")
	}
	if cfg.RaftWorkers != defaultRaftWorkers {
		t.Fatalf("expected default RaftWorkers")
	}
	if cfg.ElectionTick != defaultElectionTick {
		t.Fatalf("expected default ElectionTick")
	}
	if cfg.HeartbeatTick != defaultHeartbeatTick {
		t.Fatalf("expected default HeartbeatTick")
	}
	if cfg.DialTimeout != defaultDialTimeout {
		t.Fatalf("expected default DialTimeout")
	}
	if cfg.Timeouts.ControllerObservation != defaultControllerObservationTimeout {
		t.Fatalf("expected default ControllerObservation timeout")
	}
	if cfg.Timeouts.ControllerRequest != defaultControllerRequestTimeout {
		t.Fatalf("expected default ControllerRequest timeout")
	}
	if cfg.Timeouts.ControllerLeaderWait != defaultControllerLeaderWaitTimeout {
		t.Fatalf("expected default ControllerLeaderWait timeout")
	}
	if cfg.Timeouts.ManagedSlotLeaderWait != defaultManagedSlotLeaderWaitTimeout {
		t.Fatalf("expected default ManagedSlotLeaderWait timeout")
	}
	if cfg.Timeouts.ManagedSlotCatchUp != defaultManagedSlotCatchUpTimeout {
		t.Fatalf("expected default ManagedSlotCatchUp timeout")
	}
	if cfg.Timeouts.ManagedSlotLeaderMove != defaultManagedSlotLeaderMoveTimeout {
		t.Fatalf("expected default ManagedSlotLeaderMove timeout")
	}
	if cfg.Timeouts.ForwardRetryBudget != defaultForwardRetryBudget {
		t.Fatalf("expected default ForwardRetryBudget")
	}
	if cfg.Timeouts.ConfigChangeRetryBudget != defaultConfigChangeRetryBudget {
		t.Fatalf("expected default ConfigChangeRetryBudget")
	}
	if cfg.Timeouts.LeaderTransferRetryBudget != defaultLeaderTransferRetryBudget {
		t.Fatalf("expected default LeaderTransferRetryBudget")
	}
}

func TestConfigApplyDefaultsPreservesExplicitTimeouts(t *testing.T) {
	cfg := validTestConfig()
	cfg.Timeouts = Timeouts{
		ControllerObservation:     350 * time.Millisecond,
		ControllerRequest:         3 * time.Second,
		ControllerLeaderWait:      9 * time.Second,
		ForwardRetryBudget:        600 * time.Millisecond,
		ManagedSlotLeaderWait:     6 * time.Second,
		ManagedSlotCatchUp:        7 * time.Second,
		ManagedSlotLeaderMove:     8 * time.Second,
		ConfigChangeRetryBudget:   700 * time.Millisecond,
		LeaderTransferRetryBudget: 800 * time.Millisecond,
	}

	cfg.applyDefaults()

	if cfg.Timeouts.ControllerObservation != 350*time.Millisecond {
		t.Fatalf("expected explicit ControllerObservation timeout")
	}
	if cfg.Timeouts.ControllerRequest != 3*time.Second {
		t.Fatalf("expected explicit ControllerRequest timeout")
	}
	if cfg.Timeouts.ControllerLeaderWait != 9*time.Second {
		t.Fatalf("expected explicit ControllerLeaderWait timeout")
	}
	if cfg.Timeouts.ForwardRetryBudget != 600*time.Millisecond {
		t.Fatalf("expected explicit ForwardRetryBudget")
	}
}

func TestConfigApplyDefaultsIncludesObservationCadence(t *testing.T) {
	cfg := validTestConfig()

	cfg.applyDefaults()

	if cfg.Timeouts.ObservationHeartbeatInterval != 2*time.Second {
		t.Fatalf("ObservationHeartbeatInterval = %v, want %v", cfg.Timeouts.ObservationHeartbeatInterval, 2*time.Second)
	}
	if cfg.Timeouts.ObservationRuntimeScanInterval != time.Second {
		t.Fatalf("ObservationRuntimeScanInterval = %v, want %v", cfg.Timeouts.ObservationRuntimeScanInterval, time.Second)
	}
	if cfg.Timeouts.ObservationRuntimeFlushDebounce != 200*time.Millisecond {
		t.Fatalf("ObservationRuntimeFlushDebounce = %v, want %v", cfg.Timeouts.ObservationRuntimeFlushDebounce, 200*time.Millisecond)
	}
	if cfg.Timeouts.ObservationRuntimeFullSyncInterval != 60*time.Second {
		t.Fatalf("ObservationRuntimeFullSyncInterval = %v, want %v", cfg.Timeouts.ObservationRuntimeFullSyncInterval, 60*time.Second)
	}
}

func TestConfigApplyDefaultsPreservesExplicitObservationCadence(t *testing.T) {
	cfg := validTestConfig()
	cfg.Timeouts = Timeouts{
		ObservationHeartbeatInterval:       3 * time.Second,
		ObservationRuntimeScanInterval:     1500 * time.Millisecond,
		ObservationRuntimeFlushDebounce:    125 * time.Millisecond,
		ObservationRuntimeFullSyncInterval: 90 * time.Second,
	}

	cfg.applyDefaults()

	if cfg.Timeouts.ObservationHeartbeatInterval != 3*time.Second {
		t.Fatalf("ObservationHeartbeatInterval = %v, want %v", cfg.Timeouts.ObservationHeartbeatInterval, 3*time.Second)
	}
	if cfg.Timeouts.ObservationRuntimeScanInterval != 1500*time.Millisecond {
		t.Fatalf("ObservationRuntimeScanInterval = %v, want %v", cfg.Timeouts.ObservationRuntimeScanInterval, 1500*time.Millisecond)
	}
	if cfg.Timeouts.ObservationRuntimeFlushDebounce != 125*time.Millisecond {
		t.Fatalf("ObservationRuntimeFlushDebounce = %v, want %v", cfg.Timeouts.ObservationRuntimeFlushDebounce, 125*time.Millisecond)
	}
	if cfg.Timeouts.ObservationRuntimeFullSyncInterval != 90*time.Second {
		t.Fatalf("ObservationRuntimeFullSyncInterval = %v, want %v", cfg.Timeouts.ObservationRuntimeFullSyncInterval, 90*time.Second)
	}
}

func TestClusterTimeoutDefaultsIncludeSlowSyncAndPlannerSafetyIntervals(t *testing.T) {
	cluster := &Cluster{cfg: validTestConfig()}
	cluster.cfg.applyDefaults()

	if got, want := cluster.observationSlowSyncInterval(), defaultObservationSlowSyncInterval; got != want {
		t.Fatalf("observationSlowSyncInterval() = %v, want %v", got, want)
	}
	if got, want := cluster.plannerSafetyInterval(), defaultPlannerSafetyInterval; got != want {
		t.Fatalf("plannerSafetyInterval() = %v, want %v", got, want)
	}
	if got, want := cluster.plannerWakeDebounce(), defaultPlannerWakeDebounce; got != want {
		t.Fatalf("plannerWakeDebounce() = %v, want %v", got, want)
	}

	cluster.cfg.Timeouts.ObservationSlowSyncInterval = 4 * time.Second
	cluster.cfg.Timeouts.PlannerSafetyInterval = 3 * time.Second
	cluster.cfg.Timeouts.PlannerWakeDebounce = 250 * time.Millisecond

	if got, want := cluster.observationSlowSyncInterval(), 4*time.Second; got != want {
		t.Fatalf("observationSlowSyncInterval() override = %v, want %v", got, want)
	}
	if got, want := cluster.plannerSafetyInterval(), 3*time.Second; got != want {
		t.Fatalf("plannerSafetyInterval() override = %v, want %v", got, want)
	}
	if got, want := cluster.plannerWakeDebounce(), 250*time.Millisecond; got != want {
		t.Fatalf("plannerWakeDebounce() override = %v, want %v", got, want)
	}
}

func TestConfigValidate_NodeIDZero(t *testing.T) {
	cfg := validTestConfig()
	cfg.NodeID = 0
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_ListenAddrEmpty(t *testing.T) {
	cfg := validTestConfig()
	cfg.ListenAddr = ""
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_NewStorageNil(t *testing.T) {
	cfg := validTestConfig()
	cfg.NewStorage = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_NewStateMachineNil(t *testing.T) {
	cfg := validTestConfig()
	cfg.NewStateMachine = nil
	cfg.NewStateMachineWithHashSlots = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_DuplicateNodeID(t *testing.T) {
	cfg := validTestConfig()
	cfg.Nodes = append(cfg.Nodes, NodeConfig{NodeID: 1, Addr: "127.0.0.1:9004"})
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_DuplicateSlotID(t *testing.T) {
	cfg := validTestConfig()
	cfg.SlotCount = 2
	cfg.Slots = append(cfg.Slots, SlotConfig{SlotID: 1, Peers: []multiraft.NodeID{1, 2, 3}})
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidateAllowsControllerConfigWithLegacySlots(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerReplicaN = 3
	cfg.SlotReplicaN = 3

	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateAllowsNilSlotsWithExplicitSlotCount(t *testing.T) {
	cfg := validTestConfig()
	cfg.Slots = nil
	cfg.ControllerReplicaN = 3
	cfg.SlotReplicaN = 3

	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateRejectsLocalNodeMissingWithLegacySlots(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerReplicaN = 3
	cfg.SlotReplicaN = 3
	cfg.NodeID = 99

	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidateRejectsOnlyOneControllerPath(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerRaftPath = ""

	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigDerivedControllerNodesSortsAndTruncates(t *testing.T) {
	cfg := validTestConfig()
	cfg.Nodes = []NodeConfig{
		{NodeID: 3, Addr: "127.0.0.1:9003"},
		{NodeID: 1, Addr: "127.0.0.1:9001"},
		{NodeID: 2, Addr: "127.0.0.1:9002"},
	}
	cfg.ControllerReplicaN = 2

	derived := cfg.DerivedControllerNodes()
	if len(derived) != 2 {
		t.Fatalf("len(derived) = %d", len(derived))
	}
	if derived[0].NodeID != 1 || derived[1].NodeID != 2 {
		t.Fatalf("derived controller nodes = %+v", derived)
	}
}

func validTestConfig() Config {
	return Config{
		NodeID:             1,
		ListenAddr:         ":9001",
		SlotCount:          1,
		ControllerMetaPath: "/tmp/controller-meta",
		ControllerRaftPath: "/tmp/controller-raft",
		ControllerReplicaN: 3,
		SlotReplicaN:       3,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return nil, nil
		},
		NewStateMachine: func(slotID multiraft.SlotID) (multiraft.StateMachine, error) {
			return nil, nil
		},
		NewStateMachineWithHashSlots: func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
			return nil, nil
		},
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:9001"},
			{NodeID: 2, Addr: "127.0.0.1:9002"},
			{NodeID: 3, Addr: "127.0.0.1:9003"},
		},
		Slots: []SlotConfig{
			{SlotID: 1, Peers: []multiraft.NodeID{1, 2, 3}},
		},
	}
}
