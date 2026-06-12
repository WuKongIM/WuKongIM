package clusterv2

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
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
