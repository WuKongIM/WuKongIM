package clusterv2

import "time"

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
	// Slots contains Slot runtime sizing and placement defaults.
	Slots SlotConfig
	// Channel contains ChannelV2 service configuration.
	Channel ChannelConfig
	// Timeouts contains lifecycle timeout budgets.
	Timeouts TimeoutConfig
}

// ControlConfig contains ControllerV2 adapter configuration.
type ControlConfig struct {
	// StateDir stores ControllerV2 cluster-state files for this node.
	StateDir string
}

// SlotConfig contains Slot runtime sizing and placement defaults.
type SlotConfig struct {
	// InitialSlotCount is the number of physical Slots created by the initial control snapshot.
	InitialSlotCount uint32
	// HashSlotCount is the number of logical hash slots in the route table.
	HashSlotCount uint16
	// ReplicaCount is the desired replica count for each physical Slot.
	ReplicaCount uint16
}

// ChannelConfig contains ChannelV2 service configuration.
type ChannelConfig struct {
	// ReactorCount is the number of ChannelV2 reactor partitions.
	ReactorCount int
	// MailboxSize bounds each ChannelV2 reactor mailbox.
	MailboxSize int
	// TickInterval controls how often Node-owned loops call ChannelV2 Tick.
	TickInterval time.Duration
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
		c.Timeouts.Start = 10 * time.Second
	}
	if c.Timeouts.Stop == 0 {
		c.Timeouts.Stop = 5 * time.Second
	}
	if c.Channel.TickInterval == 0 {
		c.Channel.TickInterval = 20 * time.Millisecond
	}
}

func (c Config) validate() error {
	if c.NodeID == 0 || c.ListenAddr == "" || c.DataDir == "" {
		return ErrInvalidConfig
	}
	if c.Channel.TickInterval < 0 {
		return ErrInvalidConfig
	}
	return nil
}
