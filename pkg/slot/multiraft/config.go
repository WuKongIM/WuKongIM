package multiraft

import (
	"fmt"
	"time"
)

const (
	defaultLogCompactionTriggerEntries = uint64(10000)
	defaultLogCompactionCheckInterval  = 30 * time.Second
)

// LogCompactionConfig controls local Slot Raft snapshot compaction.
type LogCompactionConfig struct {
	// Enabled controls whether this node creates local Slot Raft snapshots.
	Enabled bool
	// EnabledSet records whether Enabled was explicitly configured.
	EnabledSet bool
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between compaction checks.
	CheckInterval time.Duration
}

// NormalizeLogCompactionConfig applies Slot Raft snapshot compaction defaults.
func NormalizeLogCompactionConfig(cfg LogCompactionConfig) LogCompactionConfig {
	if !cfg.EnabledSet {
		cfg.Enabled = true
	}
	if cfg.TriggerEntries == 0 {
		cfg.TriggerEntries = defaultLogCompactionTriggerEntries
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = defaultLogCompactionCheckInterval
	}
	return cfg
}

// ValidateLogCompactionConfig checks Slot Raft snapshot compaction settings.
func ValidateLogCompactionConfig(cfg LogCompactionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.TriggerEntries == 0 {
		return fmt.Errorf("%w: slot log compaction trigger entries must be > 0", ErrInvalidOptions)
	}
	if cfg.CheckInterval <= 0 {
		return fmt.Errorf("%w: slot log compaction check interval must be > 0", ErrInvalidOptions)
	}
	return nil
}
