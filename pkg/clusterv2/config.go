package clusterv2

import (
	"fmt"
	"path/filepath"
	"time"
)

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
	// ClusterID is the stable cluster identity used by ControllerV2 state and sync.
	ClusterID string
	// Role declares whether this node is a Controller voter or state mirror.
	Role ControlRole
	// Voters lists Controller voter node IDs and Controller RPC addresses.
	Voters []ControlVoter
	// AllowBootstrap permits this node to initialize an empty ControllerV2 Raft log.
	AllowBootstrap bool
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
	c.applyControlDefaults()
	c.applySlotDefaults()
}

func (c *Config) applyControlDefaults() {
	if c.Control.StateDir == "" && c.DataDir != "" {
		c.Control.StateDir = filepath.Join(c.DataDir, "controller")
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
}

func (c Config) validate() error {
	if c.NodeID == 0 || c.ListenAddr == "" || c.DataDir == "" {
		return ErrInvalidConfig
	}
	if c.Channel.TickInterval < 0 {
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
	if int(c.Slots.ReplicaCount) > len(c.Control.Voters) {
		return ErrInvalidConfig
	}
	return nil
}
