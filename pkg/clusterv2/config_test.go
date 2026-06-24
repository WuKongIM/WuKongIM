package clusterv2

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestConfigDefaultsSingleNodeControl(t *testing.T) {
	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
	cfg.applyDefaults()

	if cfg.Control.StateDir != filepath.Join(cfg.DataDir, "controller") {
		t.Fatalf("Control.StateDir = %q", cfg.Control.StateDir)
	}
	if cfg.Control.Role != ControlRoleVoter {
		t.Fatalf("Control.Role = %q, want voter", cfg.Control.Role)
	}
	if cfg.Control.ClusterID != "wk-clusterv2-single-node-1" {
		t.Fatalf("Control.ClusterID = %q", cfg.Control.ClusterID)
	}
	if len(cfg.Control.Voters) != 1 || cfg.Control.Voters[0].NodeID != 1 || cfg.Control.Voters[0].Addr != cfg.ListenAddr {
		t.Fatalf("Control.Voters = %#v, want local single voter", cfg.Control.Voters)
	}
	if !cfg.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = false, want true for implicit single-node cluster")
	}
	if cfg.Slots.InitialSlotCount == 0 || cfg.Slots.HashSlotCount == 0 || cfg.Slots.ReplicaCount == 0 {
		t.Fatalf("Slots defaults = %#v, want non-zero", cfg.Slots)
	}
	if cfg.Slots.TickInterval != defaultSlotTickInterval || cfg.Slots.ElectionTick != defaultSlotElectionTick || cfg.Slots.HeartbeatTick != defaultSlotHeartbeatTick {
		t.Fatalf("Slot Raft timing defaults = %s/%d/%d, want %s/%d/%d",
			cfg.Slots.TickInterval,
			cfg.Slots.ElectionTick,
			cfg.Slots.HeartbeatTick,
			defaultSlotTickInterval,
			defaultSlotElectionTick,
			defaultSlotHeartbeatTick,
		)
	}
}

func TestConfigSeedJoinModeUsesMirrorAndDisablesBootstrap(t *testing.T) {
	cfg := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID: "dev-three",
		},
		Join: JoinConfig{
			Seeds:         []string{"127.0.0.1:7011", "127.0.0.1:7012"},
			AdvertiseAddr: "127.0.0.1:7014",
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 3},
	}
	cfg.applyDefaults()

	if cfg.Control.Role != ControlRoleMirror {
		t.Fatalf("Control.Role = %q, want mirror", cfg.Control.Role)
	}
	if cfg.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = true, want false for seed-join mode")
	}
	if len(cfg.Control.Voters) != 0 {
		t.Fatalf("Control.Voters = %#v, want no implicit voters in seed-join mode", cfg.Control.Voters)
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestConfigRejectsSeedJoinMissingAdvertiseAddr(t *testing.T) {
	cfg := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "dev-three"},
		Join:       JoinConfig{Seeds: []string{"127.0.0.1:7011"}, Token: "join-secret"},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsSeedJoinWhitespaceValues(t *testing.T) {
	base := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "dev-three"},
		Join: JoinConfig{
			Seeds:         []string{"127.0.0.1:7011"},
			AdvertiseAddr: "127.0.0.1:7014",
			Token:         "join-secret",
		},
	}
	for _, tt := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "advertise addr", mutate: func(cfg *Config) { cfg.Join.AdvertiseAddr = " \t " }},
		{name: "token", mutate: func(cfg *Config) { cfg.Join.Token = " \t " }},
		{name: "seed", mutate: func(cfg *Config) { cfg.Join.Seeds = []string{" \t "} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.DataDir = t.TempDir()
			tt.mutate(&cfg)
			cfg.applyDefaults()
			if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigRejectsSeedJoinEmptySeedsIntentDoesNotBootstrap(t *testing.T) {
	cfg := Config{
		NodeID:     4,
		ListenAddr: "127.0.0.1:7014",
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "dev-three"},
		Join:       JoinConfig{AdvertiseAddr: "127.0.0.1:7014", Token: "join-secret"},
	}
	cfg.applyDefaults()

	if cfg.Control.Role != ControlRoleMirror {
		t.Fatalf("Control.Role = %q, want mirror for explicit seed-join intent", cfg.Control.Role)
	}
	if cfg.Control.AllowBootstrap {
		t.Fatal("Control.AllowBootstrap = true, want false for invalid seed-join intent")
	}
	if len(cfg.Control.Voters) != 0 {
		t.Fatalf("Control.Voters = %#v, want no implicit voters for invalid seed-join intent", cfg.Control.Voters)
	}
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigDefaultsChannelReactorCountFromGOMAXPROCS(t *testing.T) {
	old := runtime.GOMAXPROCS(6)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })

	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
	cfg.applyDefaults()

	if cfg.Channel.ReactorCount != 6 {
		t.Fatalf("Channel.ReactorCount = %d, want 6", cfg.Channel.ReactorCount)
	}
}

func TestConfigDefaultsChannelReactorCountHasFloor(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })

	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
	cfg.applyDefaults()

	if cfg.Channel.ReactorCount != 4 {
		t.Fatalf("Channel.ReactorCount = %d, want 4", cfg.Channel.ReactorCount)
	}
}

func TestConfigPreservesExplicitChannelReactorCount(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })

	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Channel:    ChannelConfig{ReactorCount: 2},
	}
	cfg.applyDefaults()

	if cfg.Channel.ReactorCount != 2 {
		t.Fatalf("Channel.ReactorCount = %d, want explicit 2", cfg.Channel.ReactorCount)
	}
}

func TestConfigRejectsNegativeChannelReactorCount(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Channel:    ChannelConfig{ReactorCount: -1},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsNegativeChannelStoreWorkers(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config ChannelConfig
	}{
		{name: "append", config: ChannelConfig{StoreAppendWorkers: -1}},
		{name: "apply", config: ChannelConfig{StoreApplyWorkers: -1}},
		{name: "rpc", config: ChannelConfig{RPCWorkers: -1}},
		{name: "append batch wait", config: ChannelConfig{StoreAppendBatchMaxWait: -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				NodeID:     1,
				ListenAddr: "127.0.0.1:0",
				DataDir:    t.TempDir(),
				Channel:    tt.config,
			}
			cfg.applyDefaults()
			if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigRejectsNegativeChannelAppendBatchTuning(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config ChannelConfig
	}{
		{name: "records", config: ChannelConfig{AppendBatchMaxRecords: -1}},
		{name: "wait", config: ChannelConfig{AppendBatchMaxWait: -1}},
		{name: "cold wait", config: ChannelConfig{AppendBatchColdMaxWait: -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				NodeID:     1,
				ListenAddr: "127.0.0.1:0",
				DataDir:    t.TempDir(),
				Channel:    tt.config,
			}
			cfg.applyDefaults()
			if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigRejectsNegativeStorageCommitShards(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Storage:    StorageConfig{CommitShards: -1},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigPreservesSlotLogCompactionConfig(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Slots: SlotConfig{
			LogCompaction: multiraft.LogCompactionConfig{
				Enabled:        true,
				EnabledSet:     true,
				TriggerEntries: 1000,
				CheckInterval:  5 * time.Second,
			},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	if cfg.Slots.LogCompaction.TriggerEntries != 1000 || cfg.Slots.LogCompaction.CheckInterval != 5*time.Second {
		t.Fatalf("Slots.LogCompaction = %#v, want explicit tuning preserved", cfg.Slots.LogCompaction)
	}
}

func TestConfigPreservesExplicitSlotRaftTiming(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Slots: SlotConfig{
			TickInterval:  25 * time.Millisecond,
			ElectionTick:  20,
			HeartbeatTick: 2,
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	if cfg.Slots.TickInterval != 25*time.Millisecond || cfg.Slots.ElectionTick != 20 || cfg.Slots.HeartbeatTick != 2 {
		t.Fatalf("Slot Raft timing = %s/%d/%d, want 25ms/20/2", cfg.Slots.TickInterval, cfg.Slots.ElectionTick, cfg.Slots.HeartbeatTick)
	}
}

func TestConfigRejectsInvalidSlotRaftTiming(t *testing.T) {
	for _, tt := range []struct {
		name  string
		slots SlotConfig
	}{
		{name: "negative tick interval", slots: SlotConfig{TickInterval: -time.Millisecond}},
		{name: "negative election", slots: SlotConfig{ElectionTick: -1}},
		{name: "negative heartbeat", slots: SlotConfig{HeartbeatTick: -1}},
		{name: "election equals heartbeat", slots: SlotConfig{ElectionTick: 1, HeartbeatTick: 1}},
		{name: "election below heartbeat", slots: SlotConfig{ElectionTick: 1, HeartbeatTick: 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				NodeID:     1,
				ListenAddr: "127.0.0.1:0",
				DataDir:    t.TempDir(),
				Slots:      tt.slots,
			}
			cfg.applyDefaults()
			if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigRejectsInvalidSlotLogCompactionConfig(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Slots: SlotConfig{
			LogCompaction: multiraft.LogCompactionConfig{
				Enabled:        true,
				EnabledSet:     true,
				TriggerEntries: 1000,
				CheckInterval:  -time.Second,
			},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, multiraft.ErrInvalidOptions) {
		t.Fatalf("validate() error = %v, want multiraft.ErrInvalidOptions", err)
	}
}

func TestConfigRejectsExplicitVotersWithoutClusterID(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			Voters: []ControlVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsDuplicateControlVoters(t *testing.T) {
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID: "cluster-a",
			Voters: []ControlVoter{
				{NodeID: 1, Addr: "127.0.0.1:10001"},
				{NodeID: 1, Addr: "127.0.0.1:10001"},
			},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsVoterRoleMissingLocalNode(t *testing.T) {
	cfg := Config{
		NodeID:     2,
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID: "cluster-a",
			Role:      ControlRoleVoter,
			Voters:    []ControlVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
	}
}
