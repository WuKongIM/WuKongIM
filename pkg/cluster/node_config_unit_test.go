package cluster

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNodeRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestNodeRejectsInvalidChannelTickInterval(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = -time.Millisecond
	if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestStorageConfigDoesNotExposeCommitNoSync(t *testing.T) {
	if _, ok := reflect.TypeOf(StorageConfig{}).FieldByName("CommitNoSync"); ok {
		t.Fatal("StorageConfig exposes CommitNoSync, want durable sync fixed on")
	}
}

func TestChannelReplicaCountDefaultsToSlotReplicaCount(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 3 {
		t.Fatalf("Channel.ReplicaCount = %d, want slot replica count 3", cfg.Channel.ReplicaCount)
	}
}

func TestChannelReplicaCountPreservesExplicitValue(t *testing.T) {
	cfg := Config{}
	cfg.Control.Voters = []ControlVoter{
		{NodeID: 1, Addr: "127.0.0.1:1001"},
		{NodeID: 2, Addr: "127.0.0.1:1002"},
		{NodeID: 3, Addr: "127.0.0.1:1003"},
	}
	cfg.Slots.ReplicaCount = 3
	cfg.Channel.ReplicaCount = 2
	cfg.applyDefaults()
	if cfg.Channel.ReplicaCount != 2 {
		t.Fatalf("Channel.ReplicaCount = %d, want explicit value 2", cfg.Channel.ReplicaCount)
	}
}

func TestChannelMigrationDefaultsEnabledWithBoundedWork(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	if !cfg.ChannelMigration.Enabled {
		t.Fatal("ChannelMigration.Enabled = false, want enabled by default")
	}
	if cfg.ChannelMigration.ScanInterval <= 0 {
		t.Fatalf("ChannelMigration.ScanInterval = %v, want positive", cfg.ChannelMigration.ScanInterval)
	}
	if cfg.ChannelMigration.ScanLimit <= 0 {
		t.Fatalf("ChannelMigration.ScanLimit = %d, want positive", cfg.ChannelMigration.ScanLimit)
	}
	if cfg.ChannelMigration.MaxTasksPerTick <= 0 {
		t.Fatalf("ChannelMigration.MaxTasksPerTick = %d, want positive", cfg.ChannelMigration.MaxTasksPerTick)
	}
}

func TestChannelMigrationConfigRejectsNegativeBounds(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.ChannelMigration.ScanInterval = -time.Second

	if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}
