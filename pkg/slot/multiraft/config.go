package multiraft

import (
	"fmt"
	"time"
)

const (
	defaultLogCompactionTriggerEntries = uint64(10000)
	defaultLogCompactionCheckInterval  = 30 * time.Second
	defaultMaxQueuedRequests           = 8192
	defaultMaxQueuedControls           = 8192
	defaultMaxQueuedBackgroundControls = defaultMaxQueuedControls / 2
	defaultMaxApplyingTasks            = 1024
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

// NormalizeRaftOptions applies multiraft runtime defaults.
func NormalizeRaftOptions(opts RaftOptions) RaftOptions {
	opts.LogCompaction = NormalizeLogCompactionConfig(opts.LogCompaction)
	if opts.MaxQueuedRequests == 0 {
		opts.MaxQueuedRequests = defaultMaxQueuedRequests
	}
	if opts.MaxQueuedControls == 0 {
		opts.MaxQueuedControls = defaultMaxQueuedControls
	}
	if opts.MaxQueuedBackgroundControls == 0 {
		opts.MaxQueuedBackgroundControls = defaultMaxQueuedBackgroundControlsFor(opts.MaxQueuedControls)
	}
	if opts.MaxApplyingTasks == 0 {
		opts.MaxApplyingTasks = defaultMaxApplyingTasks
	}
	return opts
}

// ValidateRaftOptions checks multiraft runtime settings after defaults are applied.
func ValidateRaftOptions(opts RaftOptions) error {
	if opts.MaxQueuedRequests < 0 {
		return fmt.Errorf("%w: max queued requests must be >= 0", ErrInvalidOptions)
	}
	if opts.MaxQueuedControls < 0 {
		return fmt.Errorf("%w: max queued controls must be >= 0", ErrInvalidOptions)
	}
	if opts.MaxQueuedBackgroundControls < 0 {
		return fmt.Errorf("%w: max queued background controls must be >= 0", ErrInvalidOptions)
	}
	if opts.MaxApplyingTasks < 0 {
		return fmt.Errorf("%w: max applying tasks must be >= 0", ErrInvalidOptions)
	}
	return ValidateLogCompactionConfig(opts.LogCompaction)
}

func defaultMaxQueuedBackgroundControlsFor(maxQueuedControls int) int {
	if maxQueuedControls <= 0 {
		return defaultMaxQueuedBackgroundControls
	}
	background := maxQueuedControls / 2
	if background == 0 {
		return 1
	}
	return background
}
